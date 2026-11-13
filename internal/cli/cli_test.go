package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jordantdavis/eoy/internal/update"
)

// fakeResource is a resource with get and delete commands that print their
// arguments.
func fakeResource() Resource {
	leaf := func(verb string) func() *cobra.Command {
		return func() *cobra.Command {
			return &cobra.Command{
				Use:   "[NAME...]",
				Short: verb + " widgets",
				RunE: func(cmd *cobra.Command, args []string) error {
					cmd.Println(verb, strings.Join(args, ","))
					return nil
				},
			}
		}
	}
	return Resource{
		Name: "widget", Plural: "widgets", ShortNames: []string{"wd"},
		Commands: map[string]func() *cobra.Command{"get": leaf("get"), "delete": leaf("delete")},
	}
}

func run(t *testing.T, resources []Resource, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Execute(context.Background(), NewRootCmd(resources), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestResourceNames(t *testing.T) {
	for _, name := range []string{"widgets", "widget", "wd"} {
		code, out, errOut := run(t, []Resource{fakeResource()}, "get", name, "a", "b")
		if code != ExitOK || out != "get a,b\n" {
			t.Errorf("get %s: code %d, stdout %q, stderr %q", name, code, out, errOut)
		}
	}
}

func TestVerbsWithoutResourcesAreHidden(t *testing.T) {
	root := NewRootCmd([]Resource{fakeResource()})
	for verb, want := range map[string]bool{"get": true, "delete": true, "create": false, "describe": false} {
		c, _, err := root.Find([]string{verb})
		if got := err == nil && c != root; got != want {
			t.Errorf("%s present = %v, want %v", verb, got, want)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"get"}, "you must specify the type of resource to get; valid types: widgets"},
		{[]string{"get", "gadgets"}, `unknown resource type "gadgets" for "eoy get"`},
		{[]string{"get", "widgts"}, "did you mean widgets?"},
		{[]string{"frobnicate"}, `unknown command "frobnicate" for "eoy"`},
		{[]string{"version", "--bogus"}, "unknown flag: --bogus"},
		{[]string{"version", "extra"}, `unknown command "extra" for "eoy version"`},
		{[]string{"version", "-o", "xml"}, `unknown output format "xml"`},
		{[]string{"update", "now"}, `unknown command "now" for "eoy update"`},
	}
	for _, c := range cases {
		code, _, errOut := run(t, []Resource{fakeResource()}, c.args...)
		if code != ExitUsage || !strings.Contains(errOut, c.want) || !strings.Contains(errOut, "--help' for usage") {
			t.Errorf("%v: code %d, stderr %q; want %d and %q", c.args, code, errOut, ExitUsage, c.want)
		}
	}
}

func TestRootHelp(t *testing.T) {
	code, out, _ := run(t, nil)
	if code != ExitOK || !strings.Contains(out, "version") || !strings.Contains(out, "update") {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, nil, "version")
	if code != ExitOK || !strings.HasPrefix(out, "dev") {
		t.Errorf("code %d, stdout %q", code, out)
	}

	code, out, _ = run(t, nil, "version", "-o", "json")
	var info map[string]any
	if code != ExitOK || json.Unmarshal([]byte(out), &info) != nil || info["version"] != "dev" || info["platform"] == "" {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

func TestAPIResources(t *testing.T) {
	code, out, _ := run(t, []Resource{fakeResource()}, "api-resources")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != ExitOK || len(lines) != 2 || strings.Join(strings.Fields(lines[1]), " ") != "widgets wd get,delete" {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, ExitOK},
		{update.ErrUpdateAvailable, ExitUpdateAvailable},
		{errors.New("checksum mismatch"), ExitError},
		{usageErrorf("bad"), ExitUsage},
	}
	for _, c := range cases {
		var errOut bytes.Buffer
		if got := exitCode(&cobra.Command{Use: "eoy"}, c.err, &errOut); got != c.want {
			t.Errorf("exitCode(%v) = %d, want %d", c.err, got, c.want)
		}
		if c.want == ExitUpdateAvailable && errOut.Len() > 0 {
			t.Errorf("exitCode(%v) printed %q", c.err, errOut.String())
		}
	}
}

func TestUpdateRunsUpdater(t *testing.T) {
	orig := newUpdater
	t.Cleanup(func() { newUpdater = orig })
	// plan9 has no release builds, so Run fails before any network call.
	newUpdater = func(*cobra.Command) (*update.Updater, error) { return &update.Updater{GOOS: "plan9"}, nil }
	code, _, errOut := run(t, nil, "update", "--check")
	if code != ExitError || !strings.Contains(errOut, "error: no release builds for plan9") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
}
