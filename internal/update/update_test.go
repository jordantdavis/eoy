package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const oldBinary = "#!/bin/sh\necho v1.1.0\n"

// scriptFor returns a fake binary whose "version" output starts with tag.
func scriptFor(tag string) string { return "#!/bin/sh\necho '" + tag + " (abc1234)'\n" }

// fakeRelease is a fake GitHub that serves one release. Tests change its
// fields before they run the updater.
type fakeRelease struct {
	tag     string
	entries map[string]string // tar entry name -> content
	sums    func(file string, archive []byte) string
}

func newFakeRelease(tag string) *fakeRelease {
	return &fakeRelease{
		tag:     tag,
		entries: map[string]string{ArchiveName(tag, "linux", "amd64") + "/eoy": scriptFor(tag)},
		sums: func(file string, archive []byte) string {
			s := sha256.Sum256(archive)
			return hex.EncodeToString(s[:]) + "  " + file + "\n"
		},
	}
}

func (f *fakeRelease) archive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range f.entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func (f *fakeRelease) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dl := "/releases/download/" + f.tag + "/"
		file := ArchiveName(f.tag, "linux", "amd64") + ".tar.gz"
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/"+f.tag, http.StatusFound)
		case dl + file:
			w.Write(f.archive(t))
		case dl + "checksums.txt":
			w.Write([]byte(f.sums(file, f.archive(t))))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTestUpdater returns an Updater for srv and an old binary in a temp dir.
func newTestUpdater(t *testing.T, srv *httptest.Server, current string) (*Updater, *bytes.Buffer) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "eoy")
	if err := os.WriteFile(exe, []byte(oldBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	return &Updater{Client: srv.Client(), BaseURL: srv.URL, GOOS: "linux", GOARCH: "amd64", Exe: exe, Current: current, Out: out}, out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertUnchanged makes sure that exe still holds the old binary and that no
// temp file is left in its folder.
func assertUnchanged(t *testing.T, exe string) {
	t.Helper()
	if got := readFile(t, exe); got != oldBinary {
		t.Errorf("binary changed: %q", got)
	}
	ents, _ := os.ReadDir(filepath.Dir(exe))
	if len(ents) != 1 {
		t.Errorf("folder has %d entries, want 1", len(ents))
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.0", "v1.1.9", 1},
		{"v1.2.0", "v1.2.0", 0},
		{"v1.9.0", "v1.10.0", -1},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.2.0", "v1.2.0-rc1", 1},
		{"v1.2.0-rc1", "v1.2.0-rc2", -1},
	}
	for _, c := range cases {
		got, err := compareVersions(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, %v; want %d", c.a, c.b, got, err, c.want)
		}
	}
	for _, bad := range []string{"dev", "1.2.0", "v1.2", "v1.x.0", "v1.2.3.4"} {
		if _, err := compareVersions(bad, "v1.0.0"); err == nil {
			t.Errorf("compareVersions(%q) gave no error", bad)
		}
	}
}

func TestUpdate(t *testing.T) {
	srv := newFakeRelease("v1.2.0").serve(t)
	u, out := newTestUpdater(t, srv, "v1.1.0")
	if err := u.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, u.Exe); got != scriptFor("v1.2.0") {
		t.Errorf("binary = %q", got)
	}
	fi, _ := os.Stat(u.Exe)
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", fi.Mode())
	}
	if !strings.Contains(out.String(), "v1.1.0 -> v1.2.0") {
		t.Errorf("output = %q", out)
	}
}

func TestUpdateFollowsSymlink(t *testing.T) {
	srv := newFakeRelease("v1.2.0").serve(t)
	u, _ := newTestUpdater(t, srv, "v1.1.0")
	link := filepath.Join(t.TempDir(), "eoy")
	if err := os.Symlink(u.Exe, link); err != nil {
		t.Fatal(err)
	}
	real := u.Exe
	u.Exe = link
	if err := u.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, real); got != scriptFor("v1.2.0") {
		t.Errorf("target = %q", got)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink replaced: %v, %v", fi, err)
	}
}

func TestUpdateUpToDate(t *testing.T) {
	srv := newFakeRelease("v1.1.0").serve(t)
	u, out := newTestUpdater(t, srv, "v1.1.0")
	if err := u.Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	assertUnchanged(t, u.Exe)
	if !strings.Contains(out.String(), "up to date") {
		t.Errorf("output = %q", out)
	}
}

func TestUpdateCheck(t *testing.T) {
	srv := newFakeRelease("v1.2.0").serve(t)
	for current, want := range map[string]error{"v1.1.0": ErrUpdateAvailable, "v1.2.0": nil, "dev": ErrUpdateAvailable} {
		u, out := newTestUpdater(t, srv, current)
		if err := u.Run(context.Background(), Options{Check: true}); !errors.Is(err, want) {
			t.Errorf("%s: Run = %v; want %v", current, err, want)
		}
		assertUnchanged(t, u.Exe)
		if !strings.Contains(out.String(), "latest:  v1.2.0") {
			t.Errorf("%s: output = %q", current, out)
		}
	}
}

func TestUpdateDevBuild(t *testing.T) {
	srv := newFakeRelease("v1.2.0").serve(t)
	u, _ := newTestUpdater(t, srv, "dev")
	if err := u.Run(context.Background(), Options{}); err == nil {
		t.Fatal("Run gave no error for a dev build")
	}
	assertUnchanged(t, u.Exe)

	if err := u.Run(context.Background(), Options{Tag: "v1.2.0"}); err != nil {
		t.Fatalf("Run with a tag = %v", err)
	}
	if got := readFile(t, u.Exe); got != scriptFor("v1.2.0") {
		t.Errorf("binary = %q", got)
	}
}

func TestUpdateFailures(t *testing.T) {
	bin := func(tag string) string { return ArchiveName(tag, "linux", "amd64") + "/eoy" }
	cases := map[string]func(f *fakeRelease){
		"checksum mismatch": func(f *fakeRelease) {
			f.sums = func(file string, _ []byte) string { return strings.Repeat("0", 64) + "  " + file + "\n" }
		},
		"no checksum line": func(f *fakeRelease) {
			f.sums = func(string, []byte) string { return strings.Repeat("0", 64) + "  other.tar.gz\n" }
		},
		"no binary in archive": func(f *fakeRelease) {
			f.entries = map[string]string{ArchiveName(f.tag, "linux", "amd64") + "/README.md": "hi"}
		},
		"path outside the archive folder": func(f *fakeRelease) {
			f.entries = map[string]string{"../eoy": scriptFor(f.tag)}
		},
		"binary does not run": func(f *fakeRelease) {
			f.entries = map[string]string{bin(f.tag): "#!/bin/sh\nexit 1\n"}
		},
		"binary reports wrong version": func(f *fakeRelease) {
			f.entries = map[string]string{bin(f.tag): scriptFor("v0.0.1")}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeRelease("v1.2.0")
			change(f)
			u, _ := newTestUpdater(t, f.serve(t), "v1.1.0")
			if err := u.Run(context.Background(), Options{}); err == nil {
				t.Fatal("Run gave no error")
			}
			assertUnchanged(t, u.Exe)
		})
	}
}

func TestUpdateUnsupportedPlatform(t *testing.T) {
	srv := newFakeRelease("v1.2.0").serve(t)
	u, _ := newTestUpdater(t, srv, "v1.1.0")
	u.GOOS = "windows"
	if err := u.Run(context.Background(), Options{}); err == nil {
		t.Fatal("Run gave no error")
	}
}
