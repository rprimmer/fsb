package rules

import (
	"strings"
	"testing"
)

func BenchmarkMatchCoreDeny(b *testing.B) {
	s, _ := Parse(strings.NewReader(strings.Join(CoreDeny, "\n")), ParseOptions{Home: "/Users/me"})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.MatchBelow("/Users/me/Documents/projects", "/Users/me/Documents/projects/some-file-name.txt", false)
	}
}

func BenchmarkMatchCoreDenyNonASCII(b *testing.B) {
	s, _ := Parse(strings.NewReader(strings.Join(CoreDeny, "\n")), ParseOptions{Home: "/Users/me"})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.MatchBelow("/Users/me/Documents/projects", "/Users/me/Documents/projects/Café Straße résumé.txt", false)
	}
}
