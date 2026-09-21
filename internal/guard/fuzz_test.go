package guard

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzGuardNeverExposesSecrets throws arbitrary path strings at every guarded
// operation and checks the result against an oracle that does NOT rely on the
// guard's own rules: whatever was returned must not be the same inode as a
// known secret, must not contain a secret marker, and a listing or search
// result must not name a secret, however the path was spelled.
func FuzzGuardNeverExposesSecrets(f *testing.F) {
	fx := newFixture(f)
	put(f, filepath.Join(fx.home, ".ssh", "nested", "deeper.txt"), []byte("SECRET-NESTED"))
	put(f, filepath.Join(fx.home, "proj", "public.txt"), []byte("public"))
	// Shapes an independent review found the first version of this fixture lacked:
	// symlinks to directories that CONTAIN denied entries (a listing or search
	// reached through them must still be judged by where the entries really are),
	// and an unreadable file inside a denied directory.
	for _, l := range []struct{ target, link string }{
		{fx.home, filepath.Join(fx.home, "homelink")},
		{filepath.Join(fx.home, "proj"), filepath.Join(fx.home, "projlink")},
		{filepath.Join(fx.home, ".."), filepath.Join(fx.home, "proj", "up")},
	} {
		if err := os.Symlink(l.target, l.link); err != nil {
			f.Fatal(err)
		}
	}
	locked := filepath.Join(fx.home, ".ssh", "locked")
	put(f, locked, []byte("SECRET-LOCKED"))
	if err := os.Chmod(locked, 0); err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { os.Chmod(locked, 0o600) })

	// The independent oracle: identities of everything that must never surface.
	var secrets []fs.FileInfo
	for _, p := range []string{
		filepath.Join(fx.home, ".ssh"),
		filepath.Join(fx.home, ".ssh", "id_ed25519"),
		filepath.Join(fx.home, ".ssh", "nested"),
		filepath.Join(fx.home, ".ssh", "nested", "deeper.txt"),
		fx.outside,
		filepath.Join(fx.outside, "secret.txt"),
	} {
		fi, err := os.Stat(p)
		if err != nil {
			f.Fatal(err)
		}
		secrets = append(secrets, fi)
	}
	isSecret := func(fi fs.FileInfo) bool {
		for _, s := range secrets {
			if os.SameFile(fi, s) {
				return true
			}
		}
		return false
	}
	leaks := func(b []byte) bool {
		s := string(b)
		return strings.Contains(s, "SECRET") || strings.Contains(s, "OUTSIDE")
	}

	home := fx.home
	for _, seed := range []string{
		home + "/.ssh/id_ed25519",
		home + "/.ssh",
		home + "/.ssh/nested/deeper.txt",
		home + "/proj/../.ssh/id_ed25519",
		home + "/proj/./../.ssh//id_ed25519",
		home + "/proj/shortcut/id_ed25519", // symlink into a denied directory
		home + "/proj/shortcut",
		home + "/escape/secret.txt", // symlink out of the root
		home + "/../outside/secret.txt",
		home + "/.SSH/id_ed25519",
		home + "/.ssh/",
		home + "/.ssh/.",
		home + "/.ssh/id_ed25519/..",
		home + "/.ssh/id_ed25519/..namedfork/rsrc",
		home + "/.ssh/id_ed25519/..namedfork/data",
		"/System/Volumes/Data" + home + "/.ssh/id_ed25519",
		"/system/volumes/DATA" + home + "/.ssh/id_ed25519",
		"/private" + home + "/.ssh/id_ed25519",
		home + "/.ssh/id_ed25519\x00.png",
		home + "/.ssh/id_ed25519%00",
		home + "/.sssh/id_ed25519",
		home + "/.ssh /id_ed25519",
		home + "/.ssh./id_ed25519",
		home + "/homelink/.ssh/id_ed25519",
		home + "/homelink/.ssh",
		home + "/homelink",
		home + "/homelink/homelink/.ssh/locked",
		home + "/proj/up/home/.ssh/id_ed25519",
		home + "/projlink/../.ssh/id_ed25519",
		home + "/.ssh/locked",
		home + "/proj/hello.txt",
		home,
		"/",
		"",
		"relative/.ssh/id_ed25519",
		strings.Repeat("/..", 200) + home + "/.ssh/id_ed25519",
		home + strings.Repeat("/proj/..", 300) + "/.ssh/id_ed25519",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, p string) {
		// Open, and read what it returns.
		if file, fi, err := fx.g.Open(p); err == nil {
			if isSecret(fi) {
				file.Close()
				t.Fatalf("Open(%q) returned a secret", p)
			}
			if !fi.IsDir() {
				b, _ := io.ReadAll(io.LimitReader(file, 4096))
				if leaks(b) {
					file.Close()
					t.Fatalf("Open(%q) served secret content: %q", p, b)
				}
			}
			file.Close()
		}

		// List: no entry may be a secret.
		if es, err := fx.g.List(p); err == nil {
			dir := filepath.Clean(p)
			for _, e := range es {
				if st, err := os.Stat(filepath.Join(dir, e.Name)); err == nil && isSecret(st) {
					t.Fatalf("List(%q) exposed the secret %q", p, e.Name)
				}
			}
		}

		// Head, Meta, OpenImage.
		if h, err := fx.g.Head(p, 4096); err == nil && leaks([]byte(h.Text)) {
			t.Fatalf("Head(%q) leaked: %q", p, h.Text)
		}
		if m, err := fx.g.Meta(p, true); err == nil {
			for _, x := range m.XAttrs {
				if leaks([]byte(x.Value)) {
					t.Fatalf("Meta(%q) leaked in an attribute: %q", p, x.Value)
				}
			}
		}
		if file, fi, _, err := fx.g.OpenPDF(p); err == nil {
			if isSecret(fi) {
				t.Fatalf("OpenPDF(%q) returned a secret", p)
			}
			file.Close()
		}
		if file, fi, _, err := fx.g.OpenImage(p); err == nil {
			if isSecret(fi) {
				t.Fatalf("OpenImage(%q) returned a secret", p)
			}
			file.Close()
		}

		// Search rooted at the fuzzed path: no result may be a secret.
		lim := SearchLimits{MaxResults: 20, MaxVisited: 500}
		for _, q := range []string{"id", "secret", "deeper", "public", "."} {
			_, _, _ = fx.g.Search(context.Background(), p, q, lim, func(m SearchMatch) error {
				if st, err := os.Stat(m.Path); err == nil && isSecret(st) {
					t.Fatalf("Search(%q, %q) reported the secret %q", p, q, m.Path)
				}
				return nil
			})
		}
	})
}
