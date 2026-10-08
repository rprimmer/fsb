//go:build !darwin

package quicklook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Quick Look is part of macOS. Elsewhere no picture is attempted: the
// document is not copied, and a program that happens to be called qlmanage
// is not run.
func TestNoQuickLookOutsideMacOS(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\ntouch '" + marker + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "qlmanage"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	prepared := false
	_, err := Thumbnail(context.Background(), 400, func(in string) (string, error) {
		prepared = true
		doc := filepath.Join(in, "a.docx")
		return doc, os.WriteFile(doc, []byte("doc"), 0o644)
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
	if prepared {
		t.Error("the document was copied for a picture that cannot be made")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a program named qlmanage was run")
	}
}
