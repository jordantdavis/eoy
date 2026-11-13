// Package version reports the release tag and git commit of the eoy binary.
package version

import (
	"regexp"
	"runtime"
	"runtime/debug"
)

// These are set at build time by the release workflow:
//
//	-ldflags "-X github.com/jordantdavis/eoy/internal/version.version=v1.2.3
//	          -X github.com/jordantdavis/eoy/internal/version.commit=<sha>"
var (
	version = "dev"
	commit  = ""
)

// Info describes one build of eoy.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Dirty     bool   `json:"dirty,omitempty"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns the Info of the running binary.
func Get() Info {
	info, _ := debug.ReadBuildInfo()
	return resolve(version, commit, info)
}

// Version returns the release tag, or "dev" for a local build.
func Version() string { return Get().Version }

// releaseTag matches a release tag such as v1.2.3 or v1.2.3-rc1.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)

// pseudoVersion matches the end of a Go pseudo-version, such as
// v0.0.0-20261113182150-bced5ae7ac95. Go stamps one into local builds.
var pseudoVersion = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)

// resolve fills Info from the ldflags values, then from the build info that
// Go embeds. "go build" in a checkout embeds vcs.revision and vcs.modified.
// "go install ...@vX.Y.Z" and a build at a clean tag embed the module
// version. A pseudo-version or a "+dirty" version is not a release, so
// resolve keeps "dev" for them.
func resolve(v, c string, bi *debug.BuildInfo) Info {
	i := Info{Version: v, Commit: c, GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if bi == nil {
		i.Commit = short(i.Commit)
		return i
	}
	if i.Version == "dev" && releaseTag.MatchString(bi.Main.Version) && !pseudoVersion.MatchString(bi.Main.Version) {
		i.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if i.Commit == "" {
				i.Commit = s.Value
			}
		case "vcs.modified":
			i.Dirty = s.Value == "true"
		}
	}
	i.Commit = short(i.Commit)
	return i
}

func short(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

// String returns the version with the short commit, for example
// "v0.1.0 (8a2d7ee)" or "dev (8a2d7ee-dirty)". The update command checks
// that the output of "eoy version" starts with the release tag, so the tag
// must stay first.
func (i Info) String() string {
	if i.Commit == "" {
		return i.Version
	}
	rev := i.Commit
	if i.Dirty {
		rev += "-dirty"
	}
	return i.Version + " (" + rev + ")"
}
