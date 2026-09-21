package web

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

func TestFrameCSPAdmitsExactlyTheTwoInlineBlocks(t *testing.T) {
	doc, csp := MDFrame()
	for _, tag := range []string{"script", "style"} {
		sum := sha256.Sum256(inlineBlock(doc, tag))
		want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !strings.Contains(csp, want) {
			t.Errorf("the policy does not contain the hash of the inline <%s>: %s", tag, csp)
		}
	}
	for _, forbidden := range []string{"unsafe-inline", "unsafe-eval", "'self'", "http:", "https:", "*"} {
		// 'self' appears only as the frame-ancestors source; nothing may allow loading.
		if forbidden == "'self'" {
			if strings.Count(csp, "'self'") != 1 || !strings.Contains(csp, "frame-ancestors 'self'") {
				t.Errorf("'self' may appear only in frame-ancestors: %s", csp)
			}
			continue
		}
		if strings.Contains(csp, forbidden) {
			t.Errorf("the policy must not contain %q: %s", forbidden, csp)
		}
	}
	for _, need := range []string{"default-src 'none'", "img-src data:", "base-uri 'none'", "form-action 'none'", "sandbox allow-scripts"} {
		if !strings.Contains(csp, need) {
			t.Errorf("the policy is missing %q: %s", need, csp)
		}
	}
}

func TestFrameDocumentShape(t *testing.T) {
	doc, _ := MDFrame()
	s := string(doc)
	if strings.Count(s, "<script") != 1 || strings.Count(s, "<style") != 1 {
		t.Error("exactly one script and one style are allowed")
	}
	// Nothing may load or run anything else: no external resources, no handlers.
	for _, bad := range []string{"src=", "href=", "<link", "<iframe", "<object", "<embed", "<form", "<img"} {
		if strings.Contains(s, bad) {
			t.Errorf("the frame must not contain %q", bad)
		}
	}
	if regexp.MustCompile(`(?i)\son[a-z]+\s*=`).MatchString(s) {
		t.Error("the frame must not contain inline event handler attributes")
	}
	if !strings.Contains(s, "e.source !== parent") {
		t.Error("the frame must only accept messages from its parent")
	}
}

func TestInlineBlockPanicsOnAmbiguity(t *testing.T) {
	for _, doc := range []string{"<style>a</style>", "<script>a</script><script>b</script>", "<script>a"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("expected a panic for %q", doc)
				}
			}()
			inlineBlock([]byte(doc), "script")
		}()
	}
}
