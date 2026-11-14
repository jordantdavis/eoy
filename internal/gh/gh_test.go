package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"

	"github.com/jordantdavis/eoy/internal/daterange"
)

type fakePR struct {
	id     int64
	author string
	merged string // YYYY-MM-DD
}

var mergedQuery = regexp.MustCompile(`author:(\S+) merged:(\S+)\.\.(\S+)`)

// fakeGitHub serves /search/issues from a fixed PR list and records queries.
func fakeGitHub(t *testing.T, prs []fakePR, queries *[]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/search/issues", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		*queries = append(*queries, q)
		m := mergedQuery.FindStringSubmatch(q)
		var hits []map[string]any
		for _, p := range prs {
			if p.author == m[1] && p.merged >= m[2] && p.merged <= m[3] {
				hits = append(hits, map[string]any{
					"id": p.id, "number": p.id, "title": fmt.Sprintf("PR %d", p.id),
					"html_url":       fmt.Sprintf("https://github.com/org/app/pull/%d", p.id),
					"repository_url": "https://api.github.com/repos/org/app",
					"pull_request":   map[string]any{"merged_at": p.merged + "T12:00:00Z"},
				})
			}
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		start := min((page-1)*perPage, len(hits))
		end := min(start+perPage, len(hits))
		if end < len(hits) {
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=%d>; rel="next"`, r.URL.Path, page+1))
		}
		json.NewEncoder(w).Encode(map[string]any{"total_count": len(hits), "items": hits[start:end]})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"login": "me"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMergedPRsPagesAndSplits(t *testing.T) {
	var prs []fakePR
	for i := 1; i <= 7; i++ {
		prs = append(prs, fakePR{id: int64(i), author: "me", merged: fmt.Sprintf("2026-0%d-15", i)})
	}
	prs = append(prs, fakePR{id: 99, author: "mannequin123", merged: "2026-02-01"})

	var queries []string
	srv := fakeGitHub(t, prs, &queries)
	// A cap of 3 results per query forces the range to split.
	c, err := New(context.Background(), WithBaseURL(srv.URL), WithPaging(3, 2))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := daterange.Parse("2026-01-01", "2026-12-31")
	got, err := c.MergedPRs(context.Background(), []string{"me", "mannequin123"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 8 {
		t.Fatalf("got %d PRs, want 8 (queries: %v)", len(got), queries)
	}
	seen := map[int64]bool{}
	for _, p := range got {
		if seen[p.GitHubID] {
			t.Errorf("duplicate PR %d", p.GitHubID)
		}
		seen[p.GitHubID] = true
		if p.Repo != "org/app" || p.MergedAt.IsZero() {
			t.Errorf("bad PR fields: %+v", p)
		}
	}
	if len(queries) < 4 {
		t.Errorf("expected the range to split, queries: %v", queries)
	}
	if got[len(got)-1].Author != "mannequin123" {
		t.Errorf("mannequin PR must keep the matching author: %+v", got[len(got)-1])
	}
}

func TestLogin(t *testing.T) {
	var queries []string
	srv := fakeGitHub(t, nil, &queries)
	c, _ := New(context.Background(), WithBaseURL(srv.URL))
	login, err := c.Login(context.Background())
	if err != nil || login != "me" {
		t.Fatalf("Login = %q, %v", login, err)
	}
}

func TestFindMannequins(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/commits", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "author:old-me org:acme" {
			t.Errorf("commit query = %q", got)
		}
		repo := map[string]any{"name": "app", "owner": map[string]any{"login": "acme"}}
		json.NewEncoder(w).Encode(map[string]any{"total_count": 3, "items": []any{
			map[string]any{"sha": "a1", "repository": repo},
			map[string]any{"sha": "b2", "repository": repo},
			map[string]any{"sha": "c3", "repository": repo},
		}})
	})
	pullsFor := map[string][]map[string]any{
		"a1": {{"user": map[string]any{"login": "mq1", "type": "Mannequin"}}},
		"b2": {{"user": map[string]any{"login": "mq1", "type": "Mannequin"}}},
		"c3": {{"user": map[string]any{"login": "old-me", "type": "User"}}, {"user": map[string]any{"login": "mq2", "type": "Mannequin"}}},
	}
	mux.HandleFunc("/repos/acme/app/commits/{sha}/pulls", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(pullsFor[r.PathValue("sha")])
	})
	mux.HandleFunc("/users/{login}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"login": r.PathValue("login"), "name": "Old Me"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, _ := New(context.Background(), WithBaseURL(srv.URL))
	got, err := c.FindMannequins(context.Background(), "old-me", "acme", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Login != "mq1" || got[0].PRs != 2 || got[0].Name != "Old Me" {
		t.Fatalf("candidates = %+v", got)
	}
}

func TestRateLimitWaitIgnoresOtherErrors(t *testing.T) {
	if _, limited := rateLimitWait(fmt.Errorf("boom")); limited {
		t.Error("plain errors must not count as rate limits")
	}
}
