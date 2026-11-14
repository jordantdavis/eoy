// Package gh reads merged pull requests and mannequin accounts from GitHub.
package gh

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"

	"github.com/jordantdavis/eoy/internal/daterange"
	"github.com/jordantdavis/eoy/internal/store"
)

// searchCap is the most results that GitHub search returns for one query.
const searchCap = 1000

// Client wraps a go-github client.
type Client struct {
	api     *github.Client
	cap     int
	perPage int
	logf    func(format string, args ...any)
}

// Option changes a Client. Tests use these to point at a fake server.
type Option func(*Client)

// WithBaseURL sends requests to another API root, for example a test server.
func WithBaseURL(base string) Option {
	return func(c *Client) {
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		c.api, _ = github.NewClient(github.WithAuthToken("test"), github.WithURLs(&base, &base))
	}
}

// WithPaging changes the search result cap and the page size.
func WithPaging(resultCap, perPage int) Option {
	return func(c *Client) { c.cap, c.perPage = resultCap, perPage }
}

// WithLog sets where progress messages go.
func WithLog(logf func(string, ...any)) Option {
	return func(c *Client) { c.logf = logf }
}

// New makes a client with a token from GH_TOKEN, GITHUB_TOKEN, or `gh auth token`.
func New(ctx context.Context, opts ...Option) (*Client, error) {
	c := &Client{cap: searchCap, perPage: 100, logf: func(string, ...any) {}}
	for _, o := range opts {
		o(c)
	}
	if c.api != nil {
		return c, nil
	}
	token, err := findToken(ctx)
	if err != nil {
		return nil, err
	}
	api, err := github.NewClient(github.WithAuthToken(token), github.WithTimeout(60*time.Second))
	if err != nil {
		return nil, err
	}
	c.api = api
	return c, nil
}

func findToken(ctx context.Context) (string, error) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("get a GitHub token: run `gh auth login`, or set GH_TOKEN (%w)", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Login returns the login of the account that owns the token.
func (c *Client) Login(ctx context.Context) (string, error) {
	var u *github.User
	err := c.retry(ctx, func() error {
		var err error
		u, _, err = c.api.Users.Get(ctx, "")
		return err
	})
	if err != nil {
		return "", fmt.Errorf("read the authenticated GitHub user: %w", err)
	}
	return u.GetLogin(), nil
}

// MergedPRs returns the PRs that each author merged in the range. Both ends
// of the range must be set. If a query matches more results than GitHub
// search returns, the range splits in half until each part fits.
func (c *Client) MergedPRs(ctx context.Context, authors []string, r daterange.Range) ([]store.PR, error) {
	if r.From == "" || r.To == "" {
		return nil, errors.New("MergedPRs needs both ends of the date range")
	}
	from, _ := time.Parse(daterange.Layout, r.From)
	to, _ := time.Parse(daterange.Layout, r.To)

	seen := map[int64]bool{}
	var all []store.PR
	for _, author := range authors {
		prs, err := c.mergedInRange(ctx, author, from, to)
		if err != nil {
			return nil, fmt.Errorf("search PRs by %s: %w", author, err)
		}
		c.logf("%s: %d merged PRs\n", author, len(prs))
		for _, p := range prs {
			if !seen[p.GitHubID] {
				seen[p.GitHubID] = true
				all = append(all, p)
			}
		}
	}
	return all, nil
}

func (c *Client) mergedInRange(ctx context.Context, author string, from, to time.Time) ([]store.PR, error) {
	query := fmt.Sprintf("is:pr is:merged author:%s merged:%s..%s",
		author, from.Format(daterange.Layout), to.Format(daterange.Layout))

	var prs []store.PR
	opts := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: c.perPage}}
	for page := 1; ; page++ {
		opts.Page = page
		var res *github.IssuesSearchResult
		var resp *github.Response
		err := c.retry(ctx, func() error {
			var err error
			res, resp, err = c.api.Search.Issues(ctx, query, opts)
			return err
		})
		if err != nil {
			return nil, err
		}

		tooMany := page == 1 && res.GetTotal() > c.cap
		if tooMany && to.After(from) {
			mid := from.Add(to.Sub(from) / 2).Truncate(24 * time.Hour)
			first, err := c.mergedInRange(ctx, author, from, mid)
			if err != nil {
				return nil, err
			}
			second, err := c.mergedInRange(ctx, author, mid.AddDate(0, 0, 1), to)
			if err != nil {
				return nil, err
			}
			return append(first, second...), nil
		}
		if tooMany {
			c.logf("warning: %s merged more than %d PRs on %s; only the first %d are stored\n",
				author, c.cap, from.Format(daterange.Layout), c.cap)
		}

		for _, is := range res.Issues {
			prs = append(prs, toPR(is, author))
		}
		if resp.NextPage == 0 || len(prs) >= c.cap {
			return prs, nil
		}
	}
}

func toPR(is *github.Issue, author string) store.PR {
	p := store.PR{
		GitHubID: is.GetID(),
		Repo:     repoFromURL(is.GetRepositoryURL()),
		Number:   is.GetNumber(),
		Title:    is.GetTitle(),
		URL:      is.GetHTMLURL(),
		Author:   author,
		Body:     is.GetBody(),
	}
	if is.PullRequestLinks != nil && is.PullRequestLinks.MergedAt != nil {
		p.MergedAt = is.PullRequestLinks.MergedAt.Time
	} else {
		p.MergedAt = is.GetClosedAt().Time
	}
	return p
}

// repoFromURL turns https://api.github.com/repos/owner/name into owner/name.
func repoFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return raw
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

// Candidate is a mannequin account that authored PRs with a user's commits.
type Candidate struct {
	Login string `json:"login"`
	Name  string `json:"name,omitempty"`
	PRs   int    `json:"prs"`
}

// FindMannequins finds the mannequin that an org migration made for user.
// Commits keep the real login, but migrated PRs show the mannequin as their
// author. The method finds up to maxCommits commits by user in org, finds
// the PRs that contain each commit, and counts the PR authors whose account
// type is Mannequin. The result is sorted with the highest count first.
func (c *Client) FindMannequins(ctx context.Context, user, org string, maxCommits int) ([]Candidate, error) {
	query := fmt.Sprintf("author:%s org:%s", user, org)
	var commits []*github.CommitResult
	opts := &github.SearchOptions{ListOptions: github.ListOptions{PerPage: min(maxCommits, 100)}}
	for page := 1; len(commits) < maxCommits; page++ {
		opts.Page = page
		var res *github.CommitsSearchResult
		var resp *github.Response
		err := c.retry(ctx, func() error {
			var err error
			res, resp, err = c.api.Search.Commits(ctx, query, opts)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("search commits by %s in %s: %w", user, org, err)
		}
		commits = append(commits, res.Commits...)
		if resp.NextPage == 0 || len(res.Commits) == 0 {
			break
		}
	}
	if len(commits) > maxCommits {
		commits = commits[:maxCommits]
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("no commits by %s in %s. Make sure that the login is correct and that your account can read the repos", user, org)
	}
	c.logf("testing %d commits by %s in %s\n", len(commits), user, org)

	counts := map[string]int{}
	for _, cm := range commits {
		repo := cm.GetRepository()
		owner, name := repo.GetOwner().GetLogin(), repo.GetName()
		var prs []*github.PullRequest
		err := c.retry(ctx, func() error {
			var err error
			prs, _, err = c.api.PullRequests.ListPullRequestsWithCommit(ctx, owner, name, cm.GetSHA(), nil)
			return err
		})
		if err != nil {
			c.logf("skip %s/%s@%.7s: %v\n", owner, name, cm.GetSHA(), err)
			continue
		}
		for _, pr := range prs {
			if pr.GetUser().GetType() == "Mannequin" {
				counts[pr.GetUser().GetLogin()]++
			}
		}
	}

	var out []Candidate
	for login, n := range counts {
		cand := Candidate{Login: login, PRs: n}
		if u, _, err := c.api.Users.Get(ctx, login); err == nil {
			cand.Name = u.GetName()
		}
		out = append(out, cand)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PRs != out[j].PRs {
			return out[i].PRs > out[j].PRs
		}
		return out[i].Login < out[j].Login
	})
	return out, nil
}

// retry runs fn again after a rate limit error, up to three times.
func (c *Client) retry(ctx context.Context, fn func() error) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		wait, limited := rateLimitWait(err)
		if !limited || attempt == 3 {
			return err
		}
		c.logf("GitHub rate limit reached; waiting %s\n", wait.Round(time.Second))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func rateLimitWait(err error) (time.Duration, bool) {
	var primary *github.RateLimitError
	if errors.As(err, &primary) {
		wait := time.Until(primary.Rate.Reset.Time) + time.Second
		return min(max(wait, time.Second), 2*time.Minute), true
	}
	var secondary *github.AbuseRateLimitError
	if errors.As(err, &secondary) {
		if secondary.RetryAfter != nil {
			return *secondary.RetryAfter, true
		}
		return time.Minute, true
	}
	return 0, false
}
