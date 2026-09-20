package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rprimmer/fsb/internal/rules"
)

func TestResolveRoots(t *testing.T) {
	got, err := resolveRoots("/Users/u", "", nil, false)
	if err != nil || len(got) != 1 || got[0] != "/Users/u" {
		t.Fatalf("default root: %v %v", got, err)
	}
	got, err = resolveRoots("/Users/u", "/Users/u/proj/", []string{"/Volumes/X"}, false)
	if err != nil || strings.Join(got, ",") != "/Users/u/proj,/Volumes/X" {
		t.Fatalf("explicit roots: %v %v", got, err)
	}
	for _, args := range [][]string{{"/"}, {"/", ""}, {"//"}, {"/.."}} {
		if _, err := resolveRoots("/Users/u", args[0], nil, false); err == nil {
			t.Errorf("%q must require --allow-system-root", args[0])
		}
	}
	if _, err := resolveRoots("/Users/u", "", []string{"/"}, false); err == nil {
		t.Error("--root / must require --allow-system-root")
	}
	if _, err := resolveRoots("/Users/u", "/", nil, true); err != nil {
		t.Errorf("/ should be allowed with the flag: %v", err)
	}
}

func TestInitWritesActiveCoreRulesAndNeverOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fsb")
	var out bytes.Buffer
	if err := initConfig(dir, &out); err != nil {
		t.Fatal(err)
	}
	// The written deny file must load cleanly with the core rules active.
	set, missing, err := rules.LoadDeny(filepath.Join(dir, "deny"), "/Users/u")
	if err != nil || len(missing) != 0 {
		t.Fatalf("LoadDeny: missing=%v err=%v", missing, err)
	}
	if !set.Match("/Users/u/.ssh/id_rsa", false).Matched {
		t.Error("core rules must be active in the generated file")
	}
	if !set.Match("/Users/u/proj/.env", false).Matched || !set.Match("/Users/u/keys/server.pem", false).Matched {
		t.Error("the secret-file patterns are core rules and must be active in the generated file")
	}
	if set.Match("/Users/u/.npmrc", false).Matched {
		t.Error("optional rules must be commented out in the generated file")
	}

	// A user's edits survive a second --init.
	deny := filepath.Join(dir, "deny")
	if err := os.WriteFile(deny, []byte("~/.aws/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := initConfig(dir, &out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(deny); string(b) != "~/.aws/\n" {
		t.Errorf("existing deny file was overwritten: %q", b)
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Errorf("expected an 'already exists' message, got %q", out.String())
	}
}
