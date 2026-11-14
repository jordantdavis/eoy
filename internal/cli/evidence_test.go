package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jordantdavis/eoy/internal/config"
	"github.com/jordantdavis/eoy/internal/gh"
	"github.com/jordantdavis/eoy/internal/store"
)

// env is a test database and configuration file with a fixed clock.
type env struct {
	t        *testing.T
	db, conf string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	orig := now
	t.Cleanup(func() { now = orig })
	now = func() time.Time { return time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC) }
	dir := t.TempDir()
	return &env{t: t, db: filepath.Join(dir, "eoy.db"), conf: filepath.Join(dir, "config.toml")}
}

func (e *env) run(args ...string) (int, string, string) {
	e.t.Helper()
	return run(e.t, Resources(), append([]string{"--db", e.db, "--config", e.conf}, args...)...)
}

// ok runs a command that must succeed and returns its stdout.
func (e *env) ok(args ...string) string {
	e.t.Helper()
	code, out, errOut := e.run(args...)
	if code != ExitOK {
		e.t.Fatalf("%v: code %d, stderr %q", args, code, errOut)
	}
	return out
}

func (e *env) file(name, content string) string {
	e.t.Helper()
	path := filepath.Join(e.t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return path
}

func lines(s string) []string { return strings.Split(strings.TrimSpace(s), "\n") }

func TestCreateAndGetEvidence(t *testing.T) {
	e := newEnv(t)
	out := e.ok("create", "evidence", "--source", "Slack", "--date", "2026-03-04", "--url", "https://x/1", "--tag", "kudos", "Thanked", "by", "infra")
	if !strings.HasPrefix(out, "evidence/slack:") || !strings.HasSuffix(out, " created\n") {
		t.Fatalf("create output %q", out)
	}
	id := strings.TrimSpace(e.ok("get", "ev", "-o", "name"))

	got := e.ok("get", "evidence", "--no-headers")
	if f := strings.Fields(got); len(f) != 8 || f[0] != id || f[1] != "2026-03-04" || f[2] != "slack" || f[6] != "kudos" || f[7] != "0" {
		t.Errorf("get output %q", got)
	}
	if got := e.ok("get", "evidence", "-o", "wide"); !strings.Contains(got, "URL") || !strings.Contains(got, "https://x/1") {
		t.Errorf("wide output %q", got)
	}

	desc := e.ok("describe", "evidence", id)
	for _, want := range []string{"Source:      slack", "Title:       Thanked by infra", "Tags:        kudos", "Notes:       <none>"} {
		if !strings.Contains(desc, want) {
			t.Errorf("describe has no %q:\n%s", want, desc)
		}
	}
}

func TestCreateEvidenceFromFile(t *testing.T) {
	e := newEnv(t)
	jsonl := e.file("a.jsonl", `{"external_id":"r1","title":"Received from Alex","occurred_at":"2026-10-31T22:44:31","tags":["received"]}
{"source":"email","external_id":"m1","title":"Launch recap","body":"It went well","occurred_at":"2026-03-01"}
`)
	if out := e.ok("create", "evidence", "-f", jsonl, "--source", "ace", "--tag", "imported"); out != "evidence: 2 added, 0 updated, 0 unchanged\n" {
		t.Fatalf("first import %q", out)
	}
	if out := e.ok("create", "evidence", "-f", jsonl, "--source", "ace"); out != "evidence: 0 added, 0 updated, 2 unchanged\n" {
		t.Fatalf("second import %q", out)
	}
	arr := e.file("b.json", `[{"source":"email","external_id":"m1","title":"Launch recap v2"}]`)
	if out := e.ok("create", "evidence", "-f", arr); out != "evidence: 0 added, 1 updated, 0 unchanged\n" {
		t.Fatalf("array import %q", out)
	}

	var list []store.Evidence
	if err := json.Unmarshal([]byte(e.ok("get", "evidence", "--tag", "imported", "--source", "ace", "-o", "json")), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "Received from Alex" || strings.Join(list[0].Tags, ",") != "imported,received" {
		t.Fatalf("filtered list %+v", list)
	}

	code, _, errOut := e.run("create", "evidence", "-f", e.file("bad.json", `{"source":"x","title":"t","color":"red"}`))
	if code != ExitError || !strings.Contains(errOut, `unknown field "color"`) {
		t.Errorf("unknown field: code %d, stderr %q", code, errOut)
	}
}

func TestCreateEvidenceFromStdin(t *testing.T) {
	e := newEnv(t)
	root := NewRootCmd(Resources())
	root.SetIn(strings.NewReader(`{"source":"manual","title":"Ran the offsite"}`))
	var out, errOut strings.Builder
	code := Execute(context.Background(), root, []string{"--db", e.db, "create", "evidence", "-f", "-", "-o", "name"}, &out, &errOut)
	if code != ExitOK || !strings.HasPrefix(out.String(), "manual:") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
}

func TestNotesAndTags(t *testing.T) {
	e := newEnv(t)
	id := strings.TrimSpace(e.ok("create", "evidence", "--source", "slack", "-o", "name", "Kudos"))
	if out := e.ok("create", "note", "--evidence", id, "--tag", "draft", "Use", "for", "collaboration"); out != "note/1 created\n" {
		t.Fatalf("create note %q", out)
	}
	e.ok("create", "note", "-e", id, "-f", e.file("n.md", "Second\nline\n"))
	e.ok("tag", "evidence", id, "mentoring", "Infra")
	e.ok("tag", "evidence", id, "infra-")
	e.ok("tag", "note", "1", "draft-", "final")

	if got := e.ok("get", "notes", "--tag", "final", "-o", "name"); got != "1\n" {
		t.Errorf("notes tagged final %q", got)
	}
	if got := e.ok("get", "notes", "--evidence", id, "--no-headers"); len(lines(got)) != 2 || !strings.Contains(got, "Second line") {
		t.Errorf("notes on evidence %q", got)
	}
	desc := e.ok("describe", "ev", id)
	if !strings.Contains(desc, "Tags:        mentoring") || !strings.Contains(desc, "1  2026-11-11  tags: final") || !strings.Contains(desc, "    Second\n    line") {
		t.Errorf("describe:\n%s", desc)
	}
	if got := e.ok("describe", "note", "2"); !strings.Contains(got, "Evidence: "+id+"  Kudos") {
		t.Errorf("describe note:\n%s", got)
	}
	if got := e.ok("get", "tags", "--no-headers"); strings.Join(strings.Fields(got), " ") != "final 0 1 mentoring 1 0" {
		t.Errorf("get tags %q", got)
	}

	var list []store.Evidence
	json.Unmarshal([]byte(e.ok("get", "evidence", "-o", "json")), &list)
	if len(list) != 1 || len(list[0].Notes) != 2 || list[0].NoteCount != 2 {
		t.Errorf("json notes %+v", list)
	}

	e.ok("delete", "note", "1", "--force")
	if got := e.ok("get", "notes", "-o", "name"); got != "2\n" {
		t.Errorf("notes after delete %q", got)
	}
}

func TestDeleteNeedsForce(t *testing.T) {
	e := newEnv(t)
	id := strings.TrimSpace(e.ok("create", "evidence", "--source", "slack", "-o", "name", "Kudos"))
	e.ok("create", "note", "-e", id, "keep")

	code, out, errOut := e.run("delete", "evidence", id)
	if code != ExitOK || !strings.Contains(out, id) || !strings.Contains(errOut, "not deleted") {
		t.Fatalf("preview: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	if got := e.ok("get", "ev", "-o", "name"); got != id+"\n" {
		t.Fatalf("preview deleted the item: %q", got)
	}
	if got := e.ok("delete", "evidence", "--all", "--force"); got != "deleted 1 item\n" {
		t.Fatalf("delete --all %q", got)
	}
	if got := e.ok("get", "notes", "-o", "name"); got != "" {
		t.Errorf("notes left after delete: %q", got)
	}
}

func TestEvidenceErrors(t *testing.T) {
	e := newEnv(t)
	id := strings.TrimSpace(e.ok("create", "evidence", "--source", "slack", "-o", "name", "Kudos"))
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"create", "evidence", "Kudos"}, ExitUsage, "give --source"},
		{[]string{"create", "evidence", "--source", "slack"}, ExitUsage, "give a title"},
		{[]string{"create", "evidence", "--source", "pr", "x"}, ExitError, "eoy sync prs"},
		{[]string{"create", "evidence", "--source", "slack", "--date", "soon", "x"}, ExitError, `time "soon"`},
		{[]string{"create", "evidence", "-f", "x.json", "title"}, ExitUsage, "-f takes no title"},
		{[]string{"get", "evidence", "--from", "2026-13-01"}, ExitUsage, "YYYY-MM-DD"},
		{[]string{"get", "evidence", "-o", "yaml"}, ExitUsage, `unknown output format "yaml"`},
		{[]string{"describe", "evidence", "slack:nope"}, ExitError, "not found"},
		{[]string{"describe", "pr", id}, ExitError, "is not in pullrequests"},
		{[]string{"delete", "evidence"}, ExitUsage, "give one or more IDs, or --all"},
		{[]string{"create", "note", "x"}, ExitUsage, "give --evidence"},
		{[]string{"create", "note", "-e", "slack:nope", "x"}, ExitError, "not found"},
		{[]string{"get", "notes", "abc"}, ExitUsage, "want a number"},
		{[]string{"tag", "evidence", id, "Bad Tag"}, ExitError, `tag "Bad Tag"`},
		{[]string{"tag", "evidence", id}, ExitUsage, "requires at least 2 arg"},
		{[]string{"get", "summary", "--by", "person"}, ExitUsage, `--by "person"`},
	}
	for _, c := range cases {
		code, _, errOut := e.run(c.args...)
		if code != c.code || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: code %d, stderr %q; want %d and %q", c.args, code, errOut, c.code, c.want)
		}
	}
}

func TestSummary(t *testing.T) {
	e := newEnv(t)
	e.ok("create", "evidence", "--source", "slack", "--date", "2026-03-04", "--tag", "kudos", "a")
	e.ok("create", "evidence", "--source", "slack", "--date", "2026-03-20", "b")
	e.ok("create", "evidence", "--source", "email", "--date", "2026-04-01", "--tag", "kudos", "c")

	out := e.ok("get", "summary", "--from", "2026-03-01", "--to", "2026-04-30")
	want := `By month  2026-03-01 to 2026-04-30
MONTH    SOURCE  ITEMS
2026-03  slack       2  ██
         email       0
2026-04  slack       0
         email       1  █

By source
SOURCE  ITEMS
slack       2  ██
email       1  █

PRs by repo
none

By tag
TAG    ITEMS
kudos      2  ██
`
	if out != want {
		t.Errorf("summary:\n%s\nwant:\n%s", out, want)
	}

	var res summaryOut
	if err := json.Unmarshal([]byte(e.ok("get", "sum", "--source", "email", "--by", "month,tag", "-o", "json")), &res); err != nil {
		t.Fatal(err)
	}
	if res.From != "2026-01-01" || res.To != "2026-11-11" || len(res.Months) != 11 || res.Sources != nil || len(res.Tags.Rows) != 1 {
		t.Errorf("json summary %+v", res)
	}
}

func TestBar(t *testing.T) {
	cases := []struct{ n, most, want int }{{0, 5, 0}, {3, 5, 3}, {1, 300, 1}, {150, 300, 15}, {300, 300, 30}}
	for _, c := range cases {
		if got := len([]rune(bar(c.n, c.most, 30))); got != c.want {
			t.Errorf("bar(%d, %d) has %d cells, want %d", c.n, c.most, got, c.want)
		}
	}
}

// fakeGitHub serves PR search, the current user, and mannequin lookups.
func fakeGitHub(t *testing.T) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/search/issues", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		var items []map[string]any
		if strings.Contains(q, "author:me ") {
			for i := 1; i <= 2; i++ {
				items = append(items, map[string]any{
					"id": 100 + i, "number": i, "title": fmt.Sprintf("PR %d", i),
					"html_url":       fmt.Sprintf("https://github.com/org/app/pull/%d", i),
					"repository_url": "https://api.github.com/repos/org/app",
					"pull_request":   map[string]any{"merged_at": "2026-05-0" + fmt.Sprint(i) + "T12:00:00Z"},
				})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"total_count": len(items), "items": items})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"login": "me"})
	})
	mux.HandleFunc("/search/commits", func(w http.ResponseWriter, r *http.Request) {
		repo := map[string]any{"name": "app", "owner": map[string]any{"login": "acme"}}
		json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "items": []any{map[string]any{"sha": "a1", "repository": repo}}})
	})
	mux.HandleFunc("/repos/acme/app/commits/a1/pulls", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{map[string]any{"user": map[string]any{"login": "mq1", "type": "Mannequin"}}})
	})
	mux.HandleFunc("/users/{login}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"login": r.PathValue("login"), "name": "Old Me"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	orig := newGitHub
	t.Cleanup(func() { newGitHub = orig })
	newGitHub = func(ctx context.Context, logf func(string, ...any)) (*gh.Client, error) {
		return gh.New(ctx, gh.WithBaseURL(srv.URL), gh.WithLog(logf))
	}
}

func TestSyncPRs(t *testing.T) {
	e := newEnv(t)
	fakeGitHub(t)
	if out := e.ok("sync", "prs"); out != "pullrequests: 2 added, 0 updated, 0 unchanged\n" {
		t.Fatalf("first sync %q", out)
	}
	e.ok("tag", "pr", "pr:101", "infra")
	e.ok("create", "note", "-e", "pr:101", "big one")
	if out := e.ok("sync", "pullrequests"); out != "pullrequests: 0 added, 0 updated, 2 unchanged\n" {
		t.Fatalf("second sync %q", out)
	}
	e.ok("create", "evidence", "--source", "slack", "other")

	got := e.ok("get", "prs", "--no-headers")
	if l := lines(got); len(l) != 2 || strings.Join(strings.Fields(l[1]), " ") != "pr:101 2026-05-01 org/app#1 PR 1 infra 1" {
		t.Errorf("get prs %q", got)
	}
	if got := e.ok("get", "evidence", "--source", "pr", "-o", "name"); got != "pr:102\npr:101\n" {
		t.Errorf("PRs in the evidence list %q", got)
	}
	if got := e.ok("describe", "pr", "pr:101"); !strings.Contains(got, "Repo:        org/app") || !strings.Contains(got, "Author:      me") {
		t.Errorf("describe pr:\n%s", got)
	}
	if got := e.ok("delete", "prs", "--all", "--force"); got != "deleted 2 items\n" {
		t.Errorf("delete prs %q", got)
	}
	if got := e.ok("get", "ev", "--no-headers"); len(lines(got)) != 1 {
		t.Errorf("delete prs --all must keep other evidence: %q", got)
	}
}

func TestMannequins(t *testing.T) {
	e := newEnv(t)
	fakeGitHub(t)
	code, out, errOut := e.run("get", "mannequins", "--user", "old-me", "--org", "acme")
	if code != ExitOK || strings.Join(strings.Fields(lines(out)[1]), " ") != "mq1 1 Old Me" || !strings.Contains(errOut, "eoy create mannequin mq1 --org acme") {
		t.Fatalf("get mannequins: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	e.ok("create", "mannequin", "mq1", "--org", "acme")
	cfg, err := config.Load(e.conf)
	if err != nil || cfg.Mannequin != "mq1" || cfg.Org != "acme" {
		t.Fatalf("config %+v, %v", cfg, err)
	}
	if code, _, errOut := e.run("get", "mannequins", "--config", filepath.Join(t.TempDir(), "none.toml")); code != ExitUsage || !strings.Contains(errOut, "give --org") {
		t.Errorf("no org: code %d, stderr %q", code, errOut)
	}
}
