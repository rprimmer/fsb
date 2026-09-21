package guard

import (
	"errors"
	"io"
	"path/filepath"
	"testing"
)

func TestOpenPDFIdentifiesPDFsByContentOnly(t *testing.T) {
	fx := newFixture(t)
	good := filepath.Join(fx.home, "docs", "misnamed.txt") // the extension is irrelevant
	put(t, good, []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n"))
	f, _, ct, err := fx.g.OpenPDF(good)
	if err != nil || ct != "application/pdf" {
		t.Fatalf("ct=%q err=%v", ct, err)
	}
	b := make([]byte, 5)
	if _, err := io.ReadFull(f, b); err != nil || string(b) != "%PDF-" {
		t.Errorf("the file must be rewound after sniffing: %q %v", b, err)
	}
	f.Close()

	for name, data := range map[string][]byte{
		"fake.pdf":     []byte("<html><script>alert(1)</script></html>"),
		"lead.pdf":     []byte(" %PDF-1.4"),
		"lower.pdf":    []byte("%pdf-1.4"),
		"short.pdf":    []byte("%PDF"),
		"empty.pdf":    {},
		"image.pdf":    pngHead,
		"polyglot.pdf": []byte("<script>x</script>%PDF-1.4"),
	} {
		p := filepath.Join(fx.home, "docs", name)
		put(t, p, data)
		if f, _, _, err := fx.g.OpenPDF(p); err == nil {
			f.Close()
			t.Errorf("%s was accepted as a PDF", name)
		} else if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
	// A directory is not a PDF.
	if f, _, _, err := fx.g.OpenPDF(filepath.Join(fx.home, "docs")); err == nil {
		f.Close()
		t.Error("a directory was accepted")
	}
}
