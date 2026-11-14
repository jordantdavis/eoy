package config

import (
	"path/filepath"
	"testing"
)

func TestLoadMissingIsEmpty(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil || c.Mannequin != "" {
		t.Fatalf("Load missing = %+v, %v", c, err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eoy", "config.toml")
	want := Config{GitHubUser: "me", Mannequin: "mq1", ExtraAuthors: []string{"old-me"}, Org: "acme"}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.GitHubUser != want.GitHubUser || got.Mannequin != want.Mannequin || got.Org != want.Org || len(got.ExtraAuthors) != 1 {
		t.Fatalf("round trip = %+v", got)
	}
}
