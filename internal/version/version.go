// Package version says which understudy this is: the release version and the
// commit it was built from.
//
// A release build sets both with the linker:
//
//	go build -ldflags "-X github.com/seungduk/understudy/internal/version.version=v0.1.0 -X github.com/seungduk/understudy/internal/version.commit=abc1234"
//
// Without them, the version comes from the module build information, so
// `go install github.com/seungduk/understudy/cmd/understudy@v0.1.0` reports
// v0.1.0, and the commit from the VCS stamp of a build inside a checkout.
package version

import (
	"runtime/debug"
	"strings"
)

// Set by -ldflags -X in release builds; empty otherwise.
var (
	version string
	commit  string
)

// Devel is the version of a build that has neither a release version nor a
// module version, such as go run or go build inside a checkout.
const Devel = "devel"

// Info is what a build knows about itself.
type Info struct {
	// Version is a semantic version with a leading v, such as v0.1.0, a Go
	// pseudo-version, or Devel.
	Version string
	// Commit is the full or short commit hash, "" when unknown. Modified says
	// the checkout had uncommitted changes.
	Commit   string
	Modified Modified
}

// Modified says whether a build's checkout had uncommitted changes.
type Modified int

const (
	// Clean: no uncommitted changes, or unknown.
	Clean Modified = iota
	// Dirty: the build had uncommitted changes.
	Dirty
)

// Get is this build's Info.
func Get() Info {
	bi, _ := debug.ReadBuildInfo()
	return resolve(version, commit, bi)
}

// String is this build's version, such as v0.1.0.
func String() string { return Get().Version }

// Line is the one-line form `understudy version` prints, such as
// "understudy v0.1.0 (commit abc1234)".
func (i Info) Line() string {
	s := "understudy " + i.Version
	if i.Commit != "" {
		c := i.Commit
		if len(c) > 12 {
			c = c[:12]
		}
		if i.Modified == Dirty {
			c += ", modified"
		}
		s += " (commit " + c + ")"
	}
	return s
}

// resolve picks the version and commit: the linker's values first, then the
// build information, then Devel.
func resolve(ldVersion, ldCommit string, bi *debug.BuildInfo) Info {
	i := Info{Version: ldVersion, Commit: ldCommit}
	if bi != nil {
		if i.Version == "" {
			if v := bi.Main.Version; v != "" && v != "(devel)" {
				i.Version = v
			}
		}
		if i.Commit == "" {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					i.Commit = s.Value
				case "vcs.modified":
					if s.Value == "true" {
						i.Modified = Dirty
					}
				}
			}
			if i.Commit == "" {
				i.Commit = pseudoCommit(i.Version)
			}
		}
	}
	if i.Version == "" {
		i.Version = Devel
	}
	if i.Version != Devel && !strings.HasPrefix(i.Version, "v") {
		i.Version = "v" + i.Version
	}
	return i
}

// pseudoCommit is the commit a Go pseudo-version names
// (v0.0.0-20261119000000-abcdef123456), "" for any other version.
func pseudoCommit(v string) string {
	v, _, _ = strings.Cut(v, "+")
	parts := strings.Split(v, "-")
	if len(parts) < 3 {
		return ""
	}
	last := parts[len(parts)-1]
	ts := parts[len(parts)-2]
	if i := strings.LastIndexByte(ts, '.'); i >= 0 {
		ts = ts[i+1:]
	}
	if len(last) != 12 || len(ts) != 14 || !isDigits(ts) || !isHex(last) {
		return ""
	}
	return last
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
