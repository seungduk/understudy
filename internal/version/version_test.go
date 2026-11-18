package version

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	vcs := func(rev, mod string) []debug.BuildSetting {
		return []debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: mod}}
	}
	cases := []struct {
		name                string
		ldVersion, ldCommit string
		bi                  *debug.BuildInfo
		want                Info
		line                string
	}{
		{"no build info", "", "", nil, Info{Version: Devel}, "understudy devel"},
		{"linker values win", "v0.1.0", "0123456789abcdef", &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}, Settings: vcs("ffff", "true")},
			Info{Version: "v0.1.0", Commit: "0123456789abcdef"}, "understudy v0.1.0 (commit 0123456789ab)"},
		{"linker version without v", "0.1.0", "", nil, Info{Version: "v0.1.0"}, "understudy v0.1.0"},
		{"go install of a tag", "", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}}, Info{Version: "v0.1.0"}, "understudy v0.1.0"},
		{"go install of a pseudo-version", "", "", &debug.BuildInfo{Main: debug.Module{Version: "v0.1.1-0.20261119010203-abcdef123456"}},
			Info{Version: "v0.1.1-0.20261119010203-abcdef123456", Commit: "abcdef123456"}, "understudy v0.1.1-0.20261119010203-abcdef123456 (commit abcdef123456)"},
		{"build in a checkout", "", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs("abcdef1234567890", "false")},
			Info{Version: Devel, Commit: "abcdef1234567890"}, "understudy devel (commit abcdef123456)"},
		{"dirty checkout", "", "", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs("abc", "true")},
			Info{Version: Devel, Commit: "abc", Modified: Dirty}, "understudy devel (commit abc, modified)"},
		{"empty module version", "", "", &debug.BuildInfo{}, Info{Version: Devel}, "understudy devel"},
	}
	for _, c := range cases {
		got := resolve(c.ldVersion, c.ldCommit, c.bi)
		if got != c.want {
			t.Errorf("%s: resolve = %+v, want %+v", c.name, got, c.want)
		}
		if l := got.Line(); l != c.line {
			t.Errorf("%s: Line = %q, want %q", c.name, l, c.line)
		}
	}
}

func TestPseudoCommit(t *testing.T) {
	for v, want := range map[string]string{
		"v0.0.0-20261119000000-abcdef123456":        "abcdef123456",
		"v0.1.1-0.20261119000000-abcdef123456":      "abcdef123456",
		"v0.1.0-rc.1.0.20261119000000-abcdef123456": "abcdef123456",
		"v0.0.0-20261119000000-abcdef123456+dirty":  "abcdef123456",
		"v0.1.0":                             "",
		"v0.1.0-rc.1":                        "",
		"v0.0.0-2026111900000-abcdef123456":  "",
		"v0.0.0-20261119000000-ABCDEF123456": "",
		"v0.0.0-20261119000000-abcdef12345":  "",
		"":                                   "",
	} {
		if got := pseudoCommit(v); got != want {
			t.Errorf("pseudoCommit(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestGet(t *testing.T) {
	if v := String(); v == "" {
		t.Fatal("empty version")
	}
}
