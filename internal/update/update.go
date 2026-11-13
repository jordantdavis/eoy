// Package update replaces the running eoy binary with a release from GitHub.
// It does the same steps as install.sh. The asset names must match
// .github/workflows/release.yml.
package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// RepoURL is the GitHub repository that install.sh and the release workflow use.
const RepoURL = "https://github.com/jordantdavis/eoy"

// Binary is the name of the executable inside each release archive.
const Binary = "eoy"

// maxDownload limits each download, so that a bad server cannot fill memory.
const maxDownload = 100 << 20

// ErrUpdateAvailable is returned by a check when a newer release exists.
var ErrUpdateAvailable = errors.New("a newer release is available")

// Updater replaces Exe with a release. Its fields are separate so that tests
// can use a fake server and a temporary file.
type Updater struct {
	Client  *http.Client
	BaseURL string // the repository URL, normally RepoURL
	GOOS    string
	GOARCH  string
	Exe     string // the path of the running binary
	Current string // the version of the running binary
	Out     io.Writer
}

// Options select what Run does.
type Options struct {
	Tag   string // install this tag; empty means the latest release
	Check bool   // only print the current and latest versions
	Force bool   // install even if the version is the same, or Current is "dev"
}

// Run updates the binary. With Check, it returns ErrUpdateAvailable if a
// newer release exists, and nil if the binary is up to date.
func (u *Updater) Run(ctx context.Context, o Options) error {
	if !SupportedPlatform(u.GOOS, u.GOARCH) {
		return fmt.Errorf("no release builds for %s/%s", u.GOOS, u.GOARCH)
	}
	tag := o.Tag
	explicit := tag != ""
	if !explicit {
		var err error
		if tag, err = u.latestTag(ctx); err != nil {
			return err
		}
	}

	if o.Check {
		fmt.Fprintf(u.Out, "current: %s\nlatest:  %s\n", u.Current, tag)
		if newer, err := isNewer(tag, u.Current); err != nil || newer {
			return ErrUpdateAvailable
		}
		return nil
	}

	if !o.Force && !explicit {
		if u.Current == "dev" {
			return errors.New("this is a development build; use --force or --version to replace it")
		}
		newer, err := isNewer(tag, u.Current)
		if err != nil {
			return err
		}
		if !newer {
			fmt.Fprintf(u.Out, "eoy %s is up to date\n", u.Current)
			return nil
		}
	}

	name := ArchiveName(tag, u.GOOS, u.GOARCH)
	fmt.Fprintf(u.Out, "Downloading eoy %s for %s/%s\n", tag, u.GOOS, u.GOARCH)
	archive, err := u.download(ctx, tag, name+".tar.gz")
	if err != nil {
		return err
	}
	bin, err := extractBinary(archive, name)
	if err != nil {
		return err
	}
	path, err := replaceExecutable(u.Exe, bin, tag)
	if err != nil {
		return err
	}
	fmt.Fprintf(u.Out, "Updated eoy %s -> %s at %s\n", u.Current, tag, path)
	return nil
}

// ArchiveName returns the archive name without ".tar.gz", for example
// "eoy_v1.0.0_linux_amd64". It is also the folder name inside the archive.
func ArchiveName(tag, goos, goarch string) string {
	return fmt.Sprintf("%s_%s_%s_%s", Binary, tag, goos, goarch)
}

// SupportedPlatform reports whether the release workflow builds goos/goarch.
func SupportedPlatform(goos, goarch string) bool {
	return (goos == "linux" || goos == "darwin") && (goarch == "amd64" || goarch == "arm64")
}

// latestTag reads the tag from the redirect of the releases/latest page, as
// install.sh does. This avoids the GitHub API rate limit.
func (u *Updater) latestTag(ctx context.Context) (string, error) {
	client := *u.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.BaseURL+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not find the latest release: %w", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode/100 != 3 || loc == "" {
		return "", fmt.Errorf("could not find the latest release: %s", resp.Status)
	}
	tag := loc[strings.LastIndex(loc, "/")+1:]
	if !strings.HasPrefix(tag, "v") {
		return "", errors.New("no release found")
	}
	return tag, nil
}

// download gets the archive and checksums.txt for tag. It returns the
// archive if its SHA-256 sum matches the line for it in checksums.txt.
func (u *Updater) download(ctx context.Context, tag, file string) ([]byte, error) {
	base := u.BaseURL + "/releases/download/" + tag + "/"
	archive, err := u.get(ctx, base+file)
	if err != nil {
		return nil, err
	}
	sums, err := u.get(ctx, base+"checksums.txt")
	if err != nil {
		return nil, err
	}
	var want string
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == file {
			want = f[0]
			break
		}
	}
	if want == "" {
		return nil, fmt.Errorf("no checksum for %s in checksums.txt", file)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != strings.ToLower(want) {
		return nil, fmt.Errorf("checksum mismatch for %s", file)
	}
	return archive, nil
}

func (u *Updater) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, fmt.Errorf("download failed: %s: %w", url, err)
	}
	if len(body) > maxDownload {
		return nil, fmt.Errorf("download failed: %s: larger than %d bytes", url, maxDownload)
	}
	return body, nil
}

// extractBinary returns the file <name>/eoy from the gzip tar archive. It
// reads no other entry, so a path such as ../x in the archive has no effect.
func extractBinary(archive []byte, name string) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	tr := tar.NewReader(zr)
	want := name + "/" + Binary
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("archive has no %s", want)
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if h.Name != want || h.Typeflag != tar.TypeReg {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, maxDownload))
	}
}

// replaceExecutable writes bin next to exe and runs "<bin> version" to make
// sure that it works and reports tag. Then it renames bin over exe. A running
// process keeps the old file open, so it is not affected. It returns the path
// that it replaced.
func replaceExecutable(exe string, bin []byte, tag string) (string, error) {
	path, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".eoy-update-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return "", fmt.Errorf("cannot write to %s; run the command with permission to write there, or reinstall with install.sh and INSTALL_DIR", dir)
		}
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return "", err
	}
	out, err := exec.Command(tmp.Name(), "version").Output()
	if err != nil {
		return "", fmt.Errorf("new binary does not run: %w", err)
	}
	if got := strings.TrimSpace(string(out)); !strings.HasPrefix(got, tag) {
		return "", fmt.Errorf("new binary reports version %q, want %s", got, tag)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

// isNewer reports whether release tag a is newer than b.
func isNewer(a, b string) (bool, error) {
	c, err := compareVersions(a, b)
	return c > 0, err
}

// compareVersions compares two tags of the form vMAJOR.MINOR.PATCH with an
// optional -suffix. A tag without a suffix is newer than the same tag with
// one. Two suffixes compare as strings.
func compareVersions(a, b string) (int, error) {
	pa, sa, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	pb, sb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	switch {
	case sa == sb:
		return 0, nil
	case sa == "":
		return 1, nil
	case sb == "":
		return -1, nil
	}
	return strings.Compare(sa, sb), nil
}

func parseVersion(v string) ([3]int, string, error) {
	var n [3]int
	s, ok := strings.CutPrefix(v, "v")
	if !ok {
		return n, "", fmt.Errorf("bad version %q", v)
	}
	s, suffix, _ := strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return n, "", fmt.Errorf("bad version %q", v)
	}
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 {
			return n, "", fmt.Errorf("bad version %q", v)
		}
		n[i] = x
	}
	return n, suffix, nil
}
