package version

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	const sha = "8a2d7ee5014cd90f8c482934db941f51cfd3a286"
	rev := debug.BuildSetting{Key: "vcs.revision", Value: sha}
	dirty := debug.BuildSetting{Key: "vcs.modified", Value: "true"}
	clean := debug.BuildSetting{Key: "vcs.modified", Value: "false"}
	devel := debug.Module{Version: "(devel)"}

	tests := []struct {
		name   string
		v, c   string
		bi     *debug.BuildInfo
		want   string
		commit string
	}{
		{"ldflags tag and commit", "v0.1.0", sha, &debug.BuildInfo{Main: devel}, "v0.1.0 (8a2d7ee)", "8a2d7ee"},
		{"ldflags tag, vcs commit", "v0.1.0", "", &debug.BuildInfo{Main: devel, Settings: []debug.BuildSetting{rev, clean}}, "v0.1.0 (8a2d7ee)", "8a2d7ee"},
		{"ldflags commit wins over vcs", "v0.1.0", "1234567890", &debug.BuildInfo{Settings: []debug.BuildSetting{rev}}, "v0.1.0 (1234567)", "1234567"},
		{"local dirty build", "dev", "", &debug.BuildInfo{Main: devel, Settings: []debug.BuildSetting{rev, dirty}}, "dev (8a2d7ee-dirty)", "8a2d7ee"},
		{"go install at a tag", "dev", "", &debug.BuildInfo{Main: debug.Module{Version: "v1.4.0"}}, "v1.4.0", ""},
		{"local pseudo-version", "dev", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261113182150-bced5ae7ac95+dirty"}, Settings: []debug.BuildSetting{rev, dirty}}, "dev (8a2d7ee-dirty)", "8a2d7ee"},
		{"pseudo-version after a tag", "dev", "", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.1-0.20261113182150-bced5ae7ac95"}}, "dev", ""},
		{"no build info", "dev", "", nil, "dev", ""},
		{"short commit", "dev", "abc", nil, "dev (abc)", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := resolve(tt.v, tt.c, tt.bi)
			if got := i.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
			if i.Commit != tt.commit {
				t.Errorf("Commit = %q, want %q", i.Commit, tt.commit)
			}
		})
	}
}
