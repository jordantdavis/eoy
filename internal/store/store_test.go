package store

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jordantdavis/eoy/internal/daterange"
)

var (
	ctx = context.Background()
	now = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "nested", "eoy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleEvidence() []NewEvidence {
	return []NewEvidence{
		{Source: "Slack", ExternalID: "C1/p100", Title: "Thanks for the migration help", OccurredAt: "2026-10-31T22:44:31", Tags: []string{"Kudos"}},
		{Source: "email", ExternalID: "msg-1", Title: "Launch recap", Body: "The launch went well.", URL: "https://mail.example/1", OccurredAt: "2026-03-01"},
	}
}

func samplePRs() []PR {
	return []PR{
		{GitHubID: 101, Repo: "org/app", Number: 7, Title: "Add thing", URL: "https://github.com/org/app/pull/7", Author: "me", Body: "body", MergedAt: time.Date(2026, 5, 2, 15, 0, 0, 0, time.UTC)},
	}
}

func mustUpsert(t *testing.T, s *Store, items []NewEvidence) (Counts, []string) {
	t.Helper()
	c, ids, err := s.UpsertEvidence(ctx, items, now)
	if err != nil {
		t.Fatal(err)
	}
	return c, ids
}

func TestUpsertEvidenceDeduplicates(t *testing.T) {
	s := openTemp(t)
	c, ids := mustUpsert(t, s, sampleEvidence())
	if c != (Counts{Added: 2}) {
		t.Fatalf("first import = %+v, want 2 added", c)
	}
	if !strings.HasPrefix(ids[0], "slack:") || !strings.HasPrefix(ids[1], "email:") {
		t.Fatalf("ids = %v", ids)
	}

	c, again := mustUpsert(t, s, sampleEvidence())
	if c != (Counts{Unchanged: 2}) || !slices.Equal(ids, again) {
		t.Fatalf("second import = %+v %v, want 2 unchanged with the same IDs", c, again)
	}

	changed := sampleEvidence()
	changed[1].Title = "Launch recap, final"
	if c, _ := mustUpsert(t, s, changed); c != (Counts{Updated: 1, Unchanged: 1}) {
		t.Fatalf("changed import = %+v, want 1 updated 1 unchanged", c)
	}
	e, err := s.Get(ctx, ids[1])
	if err != nil || e.Title != "Launch recap, final" || e.Body != "The launch went well." {
		t.Fatalf("Get = %+v, %v", e, err)
	}
}

func TestUpsertEvidenceWithoutExternalIDAlwaysAdds(t *testing.T) {
	s := openTemp(t)
	item := []NewEvidence{{Source: "manual", Title: "Ran the offsite"}}
	mustUpsert(t, s, item)
	if c, _ := mustUpsert(t, s, item); c != (Counts{Added: 1}) {
		t.Fatalf("second add = %+v, want 1 added", c)
	}
	list, _ := s.List(ctx, Filter{})
	if len(list) != 2 || list[0].OccurredAt != now.Format(time.RFC3339) {
		t.Fatalf("list = %+v", list)
	}
}

func TestUpsertEvidenceRejectsBadInput(t *testing.T) {
	s := openTemp(t)
	cases := map[string]NewEvidence{
		"source":      {Source: "bad source", Title: "x"},
		"for GitHub":  {Source: "pr", Title: "x"},
		"title":       {Source: "slack"},
		"time":        {Source: "slack", Title: "x", OccurredAt: "last week"},
		"end with '-": {Source: "slack", Title: "x", Tags: []string{"bad-"}},
	}
	for want, in := range cases {
		_, _, err := s.UpsertEvidence(ctx, []NewEvidence{sampleEvidence()[0], in}, now)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "item 2") {
			t.Errorf("%+v: err = %v, want %q", in, err, want)
		}
	}
	if list, _ := s.List(ctx, Filter{}); len(list) != 0 {
		t.Errorf("a failed import must store nothing, got %d items", len(list))
	}
}

func TestNotesAndTagsSurviveReimport(t *testing.T) {
	s := openTemp(t)
	_, ids := mustUpsert(t, s, sampleEvidence())
	if err := s.TagEvidence(ctx, ids[0], []string{"mentoring", "Infra"}, []string{"kudos"}); err != nil {
		t.Fatal(err)
	}
	n1, err := s.AddNote(ctx, ids[0], "Use for collaboration", []string{"draft"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddNote(ctx, ids[0], "  Second thought  ", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, s, sampleEvidence())

	e, err := s.Get(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	// The import adds kudos back, because the item in the file has it.
	if !slices.Equal(e.Tags, []string{"infra", "kudos", "mentoring"}) {
		t.Errorf("tags = %v", e.Tags)
	}
	if e.NoteCount != 2 || len(e.Notes) != 2 || e.Notes[0].ID != n1.ID || e.Notes[1].Body != "Second thought" {
		t.Errorf("notes = %+v", e.Notes)
	}
	if !slices.Equal(e.Notes[0].Tags, []string{"draft"}) {
		t.Errorf("note tags = %v", e.Notes[0].Tags)
	}
}

func TestListFilters(t *testing.T) {
	s := openTemp(t)
	_, ids := mustUpsert(t, s, sampleEvidence())
	if _, err := s.UpsertPRs(ctx, samplePRs(), now); err != nil {
		t.Fatal(err)
	}
	s.TagEvidence(ctx, ids[1], []string{"launch", "q1"}, nil)

	r, _ := daterange.Parse("2026-01-01", "2026-06-30")
	cases := []struct {
		f    Filter
		want []string
	}{
		{Filter{}, []string{ids[0], "pr:101", ids[1]}},
		{Filter{Sources: []string{"pr", "EMAIL"}}, []string{"pr:101", ids[1]}},
		{Filter{Range: r}, []string{"pr:101", ids[1]}},
		{Filter{Tags: []string{"launch", "q1"}}, []string{ids[1]}},
		{Filter{Tags: []string{"launch", "kudos"}}, nil},
		{Filter{IDs: []string{"pr:101"}}, []string{"pr:101"}},
	}
	for _, c := range cases {
		list, err := s.List(ctx, c.f)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range list {
			got = append(got, e.ID)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%+v: got %v, want %v", c.f, got, c.want)
		}
	}
}

func TestUpsertPRs(t *testing.T) {
	s := openTemp(t)
	if c, err := s.UpsertPRs(ctx, samplePRs(), now); err != nil || c != (Counts{Added: 1}) {
		t.Fatalf("first sync = %+v, %v", c, err)
	}
	if c, _ := s.UpsertPRs(ctx, samplePRs(), now); c != (Counts{Unchanged: 1}) {
		t.Fatalf("second sync = %+v", c)
	}
	moved := samplePRs()
	moved[0].Repo = "org/renamed"
	if c, _ := s.UpsertPRs(ctx, moved, now); c != (Counts{Updated: 1}) {
		t.Fatalf("renamed repo sync = %+v", c)
	}
	e, err := s.Get(ctx, "pr:101")
	if err != nil {
		t.Fatal(err)
	}
	if e.Source != PRSource || e.PR == nil || e.PR.Repo != "org/renamed" || e.PR.Number != 7 || e.OccurredAt != "2026-05-02T15:00:00Z" {
		t.Fatalf("Get = %+v %+v", e, e.PR)
	}
}

func TestDeleteCascades(t *testing.T) {
	s := openTemp(t)
	_, ids := mustUpsert(t, s, sampleEvidence())
	s.UpsertPRs(ctx, samplePRs(), now)
	s.AddNote(ctx, "pr:101", "keep?", []string{"x"}, now)

	if _, err := s.DeleteEvidence(ctx, []string{ids[0], "nope:1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ID: err = %v", err)
	}
	if list, _ := s.List(ctx, Filter{}); len(list) != 3 {
		t.Fatalf("a failed delete must remove nothing, have %d", len(list))
	}
	n, err := s.DeleteEvidence(ctx, []string{"pr:101", "pr:101", ids[0]})
	if err != nil || n != 2 {
		t.Fatalf("delete = %d, %v", n, err)
	}
	var left int
	for _, table := range []string{"pull_requests", "notes", "note_tags", "evidence_tags"} {
		var c int
		s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&c)
		left += c
	}
	if left != 0 {
		t.Errorf("%d dependent rows left after delete", left)
	}
}

func TestNotes(t *testing.T) {
	s := openTemp(t)
	_, ids := mustUpsert(t, s, sampleEvidence())
	if _, err := s.AddNote(ctx, "nope:1", "x", nil, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("note on unknown evidence: %v", err)
	}
	if _, err := s.AddNote(ctx, ids[0], "  ", nil, now); err == nil {
		t.Error("empty note must fail")
	}
	a, _ := s.AddNote(ctx, ids[0], "a", []string{"draft"}, now)
	b, _ := s.AddNote(ctx, ids[1], "b", nil, now)

	if err := s.TagNote(ctx, b.ID, []string{"draft", "final"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.TagNote(ctx, a.ID, nil, []string{"draft"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListNotes(ctx, NoteFilter{Tags: []string{"draft"}})
	if len(got) != 1 || got[0].ID != b.ID {
		t.Errorf("notes tagged draft = %+v", got)
	}
	got, _ = s.ListNotes(ctx, NoteFilter{EvidenceID: ids[0]})
	if len(got) != 1 || got[0].ID != a.ID || len(got[0].Tags) != 0 {
		t.Errorf("notes on %s = %+v", ids[0], got)
	}
	if _, err := s.ResolveNotes(ctx, []int64{a.ID, 999}); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResolveNotes unknown: %v", err)
	}
	if n, err := s.DeleteNotes(ctx, []int64{a.ID}); err != nil || n != 1 {
		t.Errorf("DeleteNotes = %d, %v", n, err)
	}

	tags, _ := s.Tags(ctx)
	want := []TagCount{{"draft", 0, 1}, {"final", 0, 1}, {"kudos", 1, 0}}
	if !slices.Equal(tags, want) {
		t.Errorf("Tags = %+v, want %+v", tags, want)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eoy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec("PRAGMA user_version = 99")
	s.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "eoy update") {
		t.Fatalf("Open newer schema: %v", err)
	}
}
