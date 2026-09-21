package guard

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"

	"github.com/rprimmer/fsb/internal/rules"
)

// A differential test against the real operating system. It does not trust any
// of fsb's own ideas about names: it builds many spellings of the path of each
// secret (case, folding aliases, decomposed accents, separators, dot segments,
// firmlink and symlink aliases, named forks) and lets the OS decide which of
// them really reach the secret (same inode). Every one that does must be refused
// by every operation of the guard.

var aliasSubs = [][2]string{
	{"s", "ſ"}, {"ss", "ß"}, {"ss", "ẞ"}, {"st", "ﬅ"}, {"st", "ﬆ"}, {"k", "K"},
	{"ff", "ﬀ"}, {"fi", "ﬁ"}, {"fl", "ﬂ"}, {"ffi", "ﬃ"}, {"ffl", "ﬄ"},
}

func mutateComponent(r *rand.Rand, c string) string {
	switch r.Intn(6) {
	case 0:
		return strings.ToUpper(c)
	case 1:
		var b strings.Builder
		for _, ch := range c {
			if r.Intn(2) == 0 {
				b.WriteString(strings.ToUpper(string(ch)))
			} else {
				b.WriteRune(ch)
			}
		}
		return b.String()
	case 2:
		s := aliasSubs[r.Intn(len(aliasSubs))]
		return strings.Replace(c, s[0], s[1], -1)
	case 3:
		return norm.NFD.String(c)
	case 4:
		// apply several alias substitutions at once
		out := c
		for i := 0; i < 3; i++ {
			s := aliasSubs[r.Intn(len(aliasSubs))]
			if r.Intn(2) == 0 {
				out = strings.Replace(out, s[0], s[1], -1)
			}
		}
		return out
	}
	return c
}

// spellings returns n spellings of real that the OS resolves to the same object.
func spellings(t *testing.T, r *rand.Rand, real string, target os.FileInfo, n int, aliasDirs map[string]string) []string {
	comps := strings.Split(strings.Trim(real, "/"), "/")
	seen := map[string]bool{}
	var out []string
	for try := 0; try < n*8 && len(out) < n; try++ {
		parts := make([]string, len(comps))
		for i, c := range comps {
			if r.Intn(2) == 0 {
				parts[i] = mutateComponent(r, c)
			} else {
				parts[i] = c
			}
		}
		p := "/" + strings.Join(parts, "/")
		switch r.Intn(9) {
		case 0:
			p = strings.Replace(p, "/", "//", -1)
		case 1:
			p += "/."
		case 2:
			p = filepath.Dir(p) + "/./" + filepath.Base(p)
		case 3:
			p = filepath.Dir(p) + "/" + filepath.Base(p) + "/../" + filepath.Base(p)
		case 4:
			p = "/System/Volumes/Data" + p
		case 5:
			p = "/system/volumes/DATA" + p
		case 6:
			p += "/"
		case 7:
			for from, to := range aliasDirs { // e.g. /private/var -> /var
				if strings.HasPrefix(p, from+"/") {
					p = to + strings.TrimPrefix(p, from)
				}
			}
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		if fi, err := os.Stat(p); err == nil && os.SameFile(fi, target) {
			out = append(out, p)
		}
	}
	return out
}

func TestDifferentialEverySpellingOfASecretIsRefused(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "home")
	secrets := []struct {
		rel   string
		isDir bool
	}{
		{".ssh/id_ed25519", false}, {".ssh", true}, {".aws/credentials", false}, {".gnupg/private-keys", false},
		{".config/gh/hosts.yml", false}, {".netrc", false}, {".kube/config", false},
		{"Library/Keychains/login.keychain-db", false},
		{"Library/Application Support/Google/Chrome/Default/Login Data", false},
		{"Library/Application Support/Firefox/Profiles/x/logins.json", false},
		{"Library/Safari/History.db", false}, {"Library/Cookies/Cookies.binarycookies", false},
		{"work/.env", false}, {"work/.env.local", false}, {"work/server.pem", false}, {"work/id.key", false},
		{"Café/cert.pem", false}, {"Straße/token.key", false},
	}
	for _, s := range secrets {
		if s.isDir {
			os.MkdirAll(filepath.Join(home, s.rel), 0o755)
		} else {
			write(t, filepath.Join(home, s.rel), "TOP-SECRET-MARKER")
		}
	}
	// A benign twin for each secret directory, reached through a symlink alias.
	if err := os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(home, "innocent-link")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, "public", "readme.txt"), "public")
	deny, err := rules.Parse(strings.NewReader(strings.Join(rules.CoreDeny, "\n")), rules.ParseOptions{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	hide, _ := rules.Parse(strings.NewReader(strings.Join(rules.DefaultIgnore, "\n")), rules.ParseOptions{Home: home, AllowNegation: true})

	aliasDirs := map[string]string{"/private/var": "/var", "/private/tmp": "/tmp", "/private/etc": "/etc"}
	roots := map[string][]string{"home": {home}, "slash": {"/"}}
	total := 0
	for rname, rl := range roots {
		g, err := New(rl, deny, hide)
		if err != nil {
			t.Fatal(err)
		}
		r := rand.New(rand.NewSource(7))
		for _, s := range secrets {
			real := filepath.Join(home, s.rel)
			target, err := os.Stat(real)
			if err != nil {
				t.Fatal(err)
			}
			paths := spellings(t, r, real, target, 45, aliasDirs)
			paths = append(paths, real)
			// Also every path through a symlink alias of the directory, and named forks.
			if strings.HasPrefix(s.rel, ".ssh") {
				paths = append(paths, filepath.Join(home, "innocent-link", strings.TrimPrefix(strings.TrimPrefix(s.rel, ".ssh"), "/")))
			}
			if !s.isDir {
				for _, ext := range []string{"/..namedfork/rsrc", "/..namedfork/data"} {
					paths = append(paths, real+ext)
				}
			}
			for i, p := range paths {
				total++
				check := func(what string, err error) {
					if err == nil {
						t.Errorf("[%s] %s(%q) succeeded for a secret (%s)", rname, what, p, s.rel)
					}
				}
				f, _, err := g.Open(p)
				if err == nil {
					f.Close()
				}
				check("Open", err)
				// Every other operation opens through the same chokepoint, so Open is
				// checked for every spelling and the rest for a sample (they are slow
				// under the race detector).
				if i%10 != 0 {
					continue
				}
				_, err = g.Head(p, 64)
				check("Head", err)
				_, err = g.Meta(p, true)
				check("Meta", err)
				_, _, _, err = g.OpenImage(p)
				check("OpenImage", err)
				_, _, _, err = g.OpenPDF(p)
				check("OpenPDF", err)
				_, err = g.ListArchive(p)
				check("ListArchive", err)
				if s.isDir {
					_, err = g.List(p)
					check("List", err)
					_, _, err = g.Search(context.Background(), p, "id", DefaultSearchLimits, func(SearchMatch) error { return nil })
					check("Search", err)
				}
			}
		}
	}
	if total < 150 {
		t.Fatalf("only %d spellings reached the secrets; the generator is not exercising the OS", total)
	}
	t.Logf("checked %d spellings that the OS resolves to a secret", total)
}

// The other direction: a spelling of an ordinary file that the OS resolves to
// it must be served (and it must not become a way to find a secret). This
// catches allow-side comparisons that assume a spelling.
func TestDifferentialEverySpellingOfAnOrdinaryFileIsServed(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "home")
	write(t, filepath.Join(home, "Straße", "Ünï cödé", "fast stuff.txt"), "ordinary")
	deny, _ := rules.Parse(strings.NewReader(strings.Join(rules.CoreDeny, "\n")), rules.ParseOptions{Home: home})
	hide, _ := rules.Parse(strings.NewReader(""), rules.ParseOptions{Home: home, AllowNegation: true})
	real := filepath.Join(home, "Straße", "Ünï cödé", "fast stuff.txt")
	target, _ := os.Stat(real)
	g, err := New([]string{home}, deny, hide)
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewSource(11))
	paths := spellings(t, r, real, target, 300, map[string]string{"/private/var": "/var"})
	if len(paths) < 50 {
		t.Fatalf("only %d spellings", len(paths))
	}
	refused := 0
	for _, p := range paths {
		f, _, err := g.Open(p)
		if err != nil {
			refused++
			if refused <= 5 {
				t.Errorf("Open(%q) refused an ordinary file that the OS resolves to %q: %v", p, real, err)
			}
			continue
		}
		f.Close()
	}
}

// Secrets do not only live in the current home directory: a clone or backup of
// it, another account's home, or a wrong $HOME (so that "~" names somewhere
// else) put the same files elsewhere. The core rules for credential folders
// must therefore apply wherever they appear.
func TestCredentialFoldersAreDeniedWhereverTheyAre(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "home")
	wrongHome := filepath.Join(base, "somewhere-else") // "~" as fsb was told it, e.g. HOME=/tmp
	secrets := []string{
		"Backup/Users/me/.ssh/id_ed25519", "otheruser/.aws/credentials", "clone/.gnupg/private-keys-v1.d/k",
		"clone/.kube/config", "clone/.netrc", "clone/.config/gh/hosts.yml",
		"Backup/Users/me/Library/Keychains/login.keychain-db",
		"Backup/Users/me/Library/Application Support/Google/Chrome/Default/Login Data",
		"Backup/Users/me/Library/Application Support/Firefox/Profiles/x/key4.db",
		"Backup/Users/me/Library/Safari/History.db", "Backup/Users/me/Library/Cookies/Cookies.binarycookies",
	}
	for _, s := range secrets {
		write(t, filepath.Join(home, s), "TOP-SECRET-MARKER")
	}
	write(t, filepath.Join(home, "docs", "readme.txt"), "public")
	write(t, filepath.Join(home, "config", "gh-notes.txt"), "public") // not the gh credentials folder
	deny, err := rules.Parse(strings.NewReader(strings.Join(rules.CoreDeny, "\n")), rules.ParseOptions{Home: wrongHome})
	if err != nil {
		t.Fatal(err)
	}
	hide, _ := rules.Parse(strings.NewReader(""), rules.ParseOptions{Home: wrongHome, AllowNegation: true})
	g, err := New([]string{home}, deny, hide)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range secrets {
		p := filepath.Join(home, s)
		if f, _, err := g.Open(p); err == nil {
			f.Close()
			t.Errorf("%s is served although it is a credential file", s)
		}
	}
	for _, ok := range []string{"docs/readme.txt", "config/gh-notes.txt"} {
		f, _, err := g.Open(filepath.Join(home, ok))
		if err != nil {
			t.Errorf("%s must stay reachable: %v", ok, err)
			continue
		}
		f.Close()
	}
	// And the folders do not appear in a listing of their parent.
	es, _ := g.List(filepath.Join(home, "clone"))
	for _, e := range es {
		if e.Name == ".ssh" || e.Name == ".gnupg" || e.Name == ".kube" || e.Name == ".netrc" {
			t.Errorf("listing shows %q", e.Name)
		}
	}
}

// Secrets are also copied out of their folders and stored under other names: a
// private key saved next to the notes it belongs to, a certificate bundle, a
// password database. The names that identify them are denied wherever they are.
func TestCommonSecretFileNamesAreDeniedAnywhere(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "home")
	secrets := []string{
		"Desktop/id_rsa", "Desktop/id_ed25519", "Desktop/id_ecdsa", "Desktop/id_dsa", "Desktop/ID_RSA", "Desktop/id_ed25519_sk",
		"work/cert.p12", "work/cert.pfx", "work/PuTTY.ppk", "work/release.jks", "work/app.keystore", "docs/passwords.kdbx",
		"Backups/login.keychain", "Backups/login.keychain-db", "proj/.git-credentials", "proj/.pgpass",
	}
	notSecret := []string{"Desktop/id_rsa.pub", "Desktop/id_ed25519.pub", "Desktop/idea.txt", "work/certificate.txt", "docs/keys.md", "proj/pgpass-notes.txt", "docs/id_rsa_notes.txt"}
	for _, s := range append(append([]string{}, secrets...), notSecret...) {
		write(t, filepath.Join(home, s), "CONTENT")
	}
	deny, err := rules.Parse(strings.NewReader(strings.Join(rules.CoreDeny, "\n")), rules.ParseOptions{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	hide, _ := rules.Parse(strings.NewReader(""), rules.ParseOptions{Home: home, AllowNegation: true})
	g, err := New([]string{home}, deny, hide)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range secrets {
		if f, _, err := g.Open(filepath.Join(home, s)); err == nil {
			f.Close()
			t.Errorf("%s is served", s)
		}
	}
	for _, s := range notSecret {
		f, _, err := g.Open(filepath.Join(home, s))
		if err != nil {
			t.Errorf("%s must stay reachable: %v", s, err)
			continue
		}
		f.Close()
	}
}
