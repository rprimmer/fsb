package quicklook

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// minimalDocx writes the smallest Word document Quick Look draws.
func minimalDocx(t *testing.T, path string) {
	t.Helper()
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello from fsb</w:t></w:r></w:p></w:body></w:document>`,
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailWithRealQuickLook(t *testing.T) {
	if _, err := exec.LookPath("qlmanage"); err != nil {
		t.Skip("qlmanage not available (not macOS)")
	}
	dir := t.TempDir()
	png, err := Thumbnail(context.Background(), 400, func(in string) (string, error) {
		doc := filepath.Join(in, "it's a \"test\" -s 1.docx") // odd characters reach qlmanage as one argument
		minimalDocx(t, doc)
		return doc, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) || len(png) < 100 {
		t.Errorf("not a PNG picture (%d bytes)", len(png))
	}

	// qlmanage never finishes on something it cannot draw; it is stopped.
	defer func(d time.Duration) { timeout = d }(timeout)
	timeout = time.Second
	start := time.Now()
	if _, err := Thumbnail(context.Background(), 400, func(in string) (string, error) {
		junk := filepath.Join(in, "junk.xlsx")
		return junk, os.WriteFile(junk, []byte("not a spreadsheet"), 0o644)
	}); err == nil {
		t.Error("a junk document should have no picture")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v; qlmanage was not stopped at the time limit", d)
	}

	// Only a document inside the private folder is ever drawn.
	outside := filepath.Join(dir, "elsewhere.docx")
	minimalDocx(t, outside)
	if _, err := Thumbnail(context.Background(), 400, func(string) (string, error) { return outside, nil }); err == nil {
		t.Error("a document outside the private folder must be refused")
	}
	if _, err := Thumbnail(context.Background(), 400, func(string) (string, error) { return "relative.docx", nil }); err == nil {
		t.Error("a relative path must be refused")
	}
	// An error from prepare is returned as it is.
	sentinel := errors.New("refused by the guard")
	if _, err := Thumbnail(context.Background(), 400, func(string) (string, error) { return "", sentinel }); err != sentinel {
		t.Errorf("err = %v, want the prepare error", err)
	}
}
