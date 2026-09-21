package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveEndpointListsAndObeysDenyRules(t *testing.T) {
	e := newEnv(t, false, nil)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("inner/a.txt")
	w.Write([]byte("SECRET-INSIDE"))
	zw.Close()
	write(t, filepath.Join(e.home, "proj", "a.zip"), buf.String())
	write(t, filepath.Join(e.home, "proj", "fake.zip"), "<html>")
	write(t, filepath.Join(e.home, ".ssh", "keys.zip"), buf.String())
	write(t, filepath.Join(e.home, "proj", "cert.pem"), buf.String())

	code, body := e.get(t, "/api/archive", filepath.Join(e.home, "proj", "a.zip"))
	var l struct {
		Format  string
		Total   int
		Entries []struct{ Name string }
	}
	if code != 200 || json.Unmarshal([]byte(body), &l) != nil || l.Format != "zip" || l.Total != 1 || l.Entries[0].Name != "inner/a.txt" {
		t.Fatalf("status=%d body=%s", code, body)
	}
	if strings.Contains(body, "SECRET-INSIDE") {
		t.Error("archive contents must never be returned, only the table of contents")
	}
	if code, _ := e.get(t, "/api/archive", filepath.Join(e.home, "proj", "fake.zip")); code != 415 {
		t.Errorf("fake: %d, want 415", code)
	}
	missing, _ := e.get(t, "/api/archive", filepath.Join(e.home, "proj", "nope.zip"))
	for _, p := range []string{filepath.Join(e.home, ".ssh", "keys.zip"), filepath.Join(e.home, "proj", "cert.pem")} {
		if code, _ := e.get(t, "/api/archive", p); code != 404 || code != missing {
			t.Errorf("%s: %d", p, code)
		}
	}
}
