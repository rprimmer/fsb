package web

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"fmt"
)

//go:embed frame/mdframe.html
var mdFrameHTML []byte

var mdFrameCSP = mustFrameCSP(mdFrameHTML)

// MDFrame returns the fixed document that renders sanitised Markdown, and the
// Content-Security-Policy to serve it with.
//
// The document has exactly one inline script and one inline style, and the
// policy admits those two by hash and nothing else: no network, no other
// script, no inline event handlers. It is meant to be embedded in a sandboxed
// iframe (so it has no access to the app or its session) and to receive its
// content by postMessage.
func MDFrame() (doc []byte, csp string) { return mdFrameHTML, mdFrameCSP }

func mustFrameCSP(doc []byte) string {
	script := inlineBlock(doc, "script")
	style := inlineBlock(doc, "style")
	return fmt.Sprintf(
		"default-src 'none'; script-src 'sha256-%s'; style-src 'sha256-%s'; img-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'; sandbox allow-scripts",
		hash(script), hash(style))
}

// inlineBlock returns the text of the single <tag>...</tag> element in doc.
// It panics unless there is exactly one, so a change to the frame that would
// silently invalidate the policy fails at startup and in tests.
func inlineBlock(doc []byte, tag string) []byte {
	open, close := []byte("<"+tag+">"), []byte("</"+tag+">")
	if bytes.Count(doc, open) != 1 || bytes.Count(doc, close) != 1 {
		panic(fmt.Sprintf("web: the Markdown frame must contain exactly one inline <%s> element", tag))
	}
	start := bytes.Index(doc, open) + len(open)
	end := bytes.Index(doc, close)
	if end < start {
		panic("web: malformed Markdown frame")
	}
	return doc[start:end]
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return base64.StdEncoding.EncodeToString(sum[:])
}
