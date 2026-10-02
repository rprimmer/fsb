package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionString(t *testing.T) {
	settings := func(kv ...string) []debug.BuildSetting {
		var s []debug.BuildSetting
		for i := 0; i < len(kv); i += 2 {
			s = append(s, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return s
	}
	for _, c := range []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"built from a clean checkout of a tag",
			&debug.BuildInfo{GoVersion: "go1.27.1", Main: debug.Module{Version: "v1.0.0"},
				Settings: settings("vcs.revision", "d6d4f34aa1b2c3d4e5f60718293a4b5c6d7e8f90", "vcs.modified", "false")},
			"fsb v1.0.0 (commit d6d4f34aa1b2, go1.27.1)"},
		{"built from a checkout with uncommitted changes",
			&debug.BuildInfo{GoVersion: "go1.27.1", Main: debug.Module{Version: "v1.0.1-0.20261002120000-d6d4f34aa1b2+dirty"},
				Settings: settings("vcs.revision", "d6d4f34aa1b2c3d4e5f60718293a4b5c6d7e8f90", "vcs.modified", "true")},
			"fsb v1.0.1-0.20261002120000-d6d4f34aa1b2+dirty (commit d6d4f34aa1b2, modified, go1.27.1)"},
		{"installed with go install from the module proxy",
			&debug.BuildInfo{GoVersion: "go1.27.1", Main: debug.Module{Version: "v1.0.0"}},
			"fsb v1.0.0 (commit of tag v1.0.0, go1.27.1)"},
		{"no build information at all", nil, "fsb (devel) (commit unknown)"},
		{"built without version control",
			&debug.BuildInfo{GoVersion: "go1.27.1", Main: debug.Module{Version: "(devel)"}},
			"fsb (devel) (commit unknown, go1.27.1)"},
	} {
		if got := versionString(c.info); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestVersionFlagPrintsAndExits(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"--version"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "fsb ") || !strings.Contains(out.String(), "commit") {
		t.Errorf("stdout = %q", out.String())
	}
}
