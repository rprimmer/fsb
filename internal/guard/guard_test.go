package guard

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rprimmer/fsb/internal/rules"
)

// fixture builds a fake home:
//
//	home/.ssh/id_ed25519        (secret, denied by core rules)
//	home/proj/hello.txt
//	home/proj/shortcut -> home/.ssh        (symlink into a denied dir)
//	home/node_modules/pkg/index.js         (hidden, not denied)
//	home/.DS_Store                         (hidden)
//	outside/secret.txt                     (outside the root)
//	home/escape -> outside                 (symlink out of the root)
type fixture struct {
	home, outside string
	g             *Guard
}

func write(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "outside")
	write(t, filepath.Join(home, ".ssh", "id_ed25519"), "SECRET")
	write(t, filepath.Join(home, "proj", "hello.txt"), "hello")
	write(t, filepath.Join(home, "node_modules", "pkg", "index.js"), "js")
	write(t, filepath.Join(home, ".DS_Store"), "x")
	write(t, filepath.Join(outside, "secret.txt"), "OUTSIDE")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(home, "proj", "shortcut")))
	must(os.Symlink(outside, filepath.Join(home, "escape")))
	must(os.Symlink(filepath.Join(home, "proj", "hello.txt"), filepath.Join(home, "hello-link")))
	must(os.Symlink(filepath.Join(home, "nope"), filepath.Join(home, "broken")))

	deny, err := rules.Parse(strings.NewReader(strings.Join(rules.CoreDeny, "\n")), rules.ParseOptions{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	hide, err := rules.Parse(strings.NewReader(strings.Join(rules.DefaultIgnore, "\n")), rules.ParseOptions{Home: home, AllowNegation: true})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New([]string{home}, deny, hide)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{home: home, outside: outside, g: g}
}

func names(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func readAll(t *testing.T, g *Guard, path string) (string, error) {
	t.Helper()
	f, _, err := g.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), nil
}

func TestOpenAllowed(t *testing.T) {
	fx := newFixture(t)
	got, err := readAll(t, fx.g, filepath.Join(fx.home, "proj", "hello.txt"))
	if err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestOpenDenied(t *testing.T) {
	fx := newFixture(t)
	_, err := readAll(t, fx.g, filepath.Join(fx.home, ".ssh", "id_ed25519"))
	var de *DeniedError
	if !errors.As(err, &de) || de.Rule != ".ssh/" {
		t.Fatalf("err = %v, want DeniedError for .ssh/", err)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("a denied error must also satisfy ErrNotFound")
	}
}

func TestDeniedAndMissingLookAlike(t *testing.T) {
	fx := newFixture(t)
	_, denied := readAll(t, fx.g, filepath.Join(fx.home, ".ssh", "id_ed25519"))
	_, missing := readAll(t, fx.g, filepath.Join(fx.home, "proj", "nope"))
	if !errors.Is(denied, ErrNotFound) || !errors.Is(missing, ErrNotFound) {
		t.Fatalf("both should be ErrNotFound: denied=%v missing=%v", denied, missing)
	}
	var de *DeniedError
	if errors.As(missing, &de) {
		t.Fatal("a missing path must not be a DeniedError")
	}
}

func TestOpenDeniedDirectoryItself(t *testing.T) {
	fx := newFixture(t)
	if _, _, err := fx.g.Open(filepath.Join(fx.home, ".ssh")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("opening the denied directory: %v", err)
	}
	if _, err := fx.g.List(filepath.Join(fx.home, ".ssh")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("listing the denied directory: %v", err)
	}
}

func TestPathTraversal(t *testing.T) {
	fx := newFixture(t)
	for _, p := range []string{
		filepath.Join(fx.home, "proj", "..", ".ssh", "id_ed25519"), // Clean collapses to a denied path
		fx.home + "/proj/../../outside/secret.txt",                 // escapes the root
		fx.home + "/../outside/secret.txt",
		fx.home + "//proj//..//.ssh/id_ed25519",
		fx.home + "/./.ssh/./id_ed25519",
	} {
		if _, err := readAll(t, fx.g, p); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", p, err)
		}
	}
}

func TestLiteralEncodedTraversalIsJustAName(t *testing.T) {
	fx := newFixture(t)
	// The guard never URL-decodes; these are plain (nonexistent) file names.
	for _, p := range []string{
		fx.home + "/proj/%2e%2e/.ssh/id_ed25519",
		fx.home + "/proj/%252e%252e/.ssh/id_ed25519",
	} {
		if _, err := readAll(t, fx.g, p); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestMalformedPaths(t *testing.T) {
	fx := newFixture(t)
	for _, p := range []string{"", "relative/path", "proj/hello.txt", fx.home + "/proj/hello.txt\x00.png", "\x00"} {
		if _, err := readAll(t, fx.g, p); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: err = %v, want ErrNotFound", p, err)
		}
	}
}

func TestSymlinkIntoDeniedDirectory(t *testing.T) {
	fx := newFixture(t)
	// Lexically fine (proj/shortcut/...) but the real path is ~/.ssh.
	if got, err := readAll(t, fx.g, filepath.Join(fx.home, "proj", "shortcut", "id_ed25519")); err == nil {
		t.Fatalf("read the denied file through a symlink: %q", got)
	}
	if _, err := fx.g.List(filepath.Join(fx.home, "proj", "shortcut")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("listing via symlink into denied dir: %v", err)
	}
	// A file symlink pointing at a denied file.
	link := filepath.Join(fx.home, "proj", "key-link")
	if err := os.Symlink(filepath.Join(fx.home, ".ssh", "id_ed25519"), link); err != nil {
		t.Fatal(err)
	}
	if got, err := readAll(t, fx.g, link); err == nil {
		t.Fatalf("read the denied file through a file symlink: %q", got)
	}
}

func TestSymlinkEscapingRoot(t *testing.T) {
	fx := newFixture(t)
	if got, err := readAll(t, fx.g, filepath.Join(fx.home, "escape", "secret.txt")); err == nil {
		t.Fatalf("read a file outside the root through a symlink: %q", got)
	}
	if _, err := fx.g.List(filepath.Join(fx.home, "escape")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("listing an escaping symlink: %v", err)
	}
}

func TestListOmitsDeniedHiddenAndEscapingEntries(t *testing.T) {
	fx := newFixture(t)
	es, err := fx.g.List(fx.home)
	if err != nil {
		t.Fatal(err)
	}
	got := names(es)
	for _, hidden := range []string{".ssh", "node_modules", ".DS_Store", "escape"} {
		if contains(got, hidden) {
			t.Errorf("listing should omit %q, got %v", hidden, got)
		}
	}
	for _, want := range []string{"proj", "hello-link", "broken"} {
		if !contains(got, want) {
			t.Errorf("listing should include %q, got %v", want, got)
		}
	}
	for _, e := range es {
		switch e.Name {
		case "hello-link":
			if !e.IsSymlink || e.IsDir || e.Broken {
				t.Errorf("hello-link flags wrong: %+v", e)
			}
			if e.Size != int64(len("hello")) {
				t.Errorf("a symlink should report its target's size (5), got %d", e.Size)
			}
		case "broken":
			if !e.IsSymlink || !e.Broken {
				t.Errorf("broken flags wrong: %+v", e)
			}
		}
	}
}

func TestListOmitsSymlinkToDeniedTarget(t *testing.T) {
	fx := newFixture(t)
	es, err := fx.g.List(filepath.Join(fx.home, "proj"))
	if err != nil {
		t.Fatal(err)
	}
	if contains(names(es), "shortcut") {
		t.Errorf("a symlink whose target is denied must not be listed: %v", names(es))
	}
}

func TestHiddenDirectoryIsReachableAndListsItsContents(t *testing.T) {
	fx := newFixture(t)
	es, err := fx.g.List(filepath.Join(fx.home, "node_modules"))
	if err != nil {
		t.Fatalf("hide rules must not block direct access: %v", err)
	}
	if !contains(names(es), "pkg") {
		t.Errorf("contents of a deliberately opened hidden dir should show: %v", names(es))
	}
	if got, err := readAll(t, fx.g, filepath.Join(fx.home, "node_modules", "pkg", "index.js")); err != nil || got != "js" {
		t.Fatalf("hidden file should be readable directly: %q, %v", got, err)
	}
}

func TestDenyBeatsHide(t *testing.T) {
	fx := newFixture(t)
	// A hide-tier negation cannot expose a denied path: the guard consults
	// only the deny set for access.
	hide, err := rules.Parse(strings.NewReader("!.ssh\n"), rules.ParseOptions{Home: fx.home, AllowNegation: true})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New([]string{fx.home}, fx.g.deny, hide)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readAll(t, g, filepath.Join(fx.home, ".ssh", "id_ed25519")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestCaseVariantsOfDeniedPaths(t *testing.T) {
	fx := newFixture(t)
	for _, name := range []string{".SSH", ".Ssh", ".sSh"} {
		_, err := readAll(t, fx.g, filepath.Join(fx.home, name, "id_ed25519"))
		var de *DeniedError
		if !errors.As(err, &de) {
			t.Errorf("%s: err = %v, want DeniedError (rule match must not depend on the volume's case sensitivity)", name, err)
		}
	}
}

func TestSpecialFilesAreNeverOpened(t *testing.T) {
	fx := newFixture(t)
	fifo := filepath.Join(fx.home, "proj", "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := fx.g.Open(fifo)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("err = %v, want ErrNotRegular", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Open blocked on a FIFO")
	}
	// It is still listed.
	es, err := fx.g.List(filepath.Join(fx.home, "proj"))
	if err != nil || !contains(names(es), "pipe") {
		t.Fatalf("FIFO should be listed: %v %v", names(es), err)
	}
}

// A real bug, found from a screenshot of the running app: opening a UNIX
// domain socket's path fails at the syscall itself (EOPNOTSUPP on macOS,
// historically ENXIO on Linux), not merely at the later type check that
// catches a FIFO. An unrecognized errno fell through mapErr's default case as
// an opaque wrapped error, which the server then reported as a 500 instead of
// the same "not found" every other non-regular file gets.
//
// A real (not symlinked) socket needs a short root: its bind path has an OS
// limit of about 104 bytes on macOS, which the deep path under t.TempDir()
// (used by newFixture) can exceed, so this builds its own short-rooted guard
// rather than reusing the shared fixture.
func TestSocketFileIsRefusedLikeAnyOtherSpecialFileNotWithAnInternalError(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "fsbsock")
	if err != nil {
		t.Skipf("no writable short-path temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	write(t, filepath.Join(root, "proj", "hello.txt"), "hello")
	sockPath := filepath.Join(root, "proj", "app.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("unix sockets unsupported here: %v", err)
	}
	defer ln.Close()

	deny, err := rules.Parse(strings.NewReader(""), rules.ParseOptions{Home: root})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New([]string{root}, deny, deny)
	if err != nil {
		t.Fatal(err)
	}

	f, _, err := g.Open(sockPath)
	if err == nil {
		f.Close()
		t.Fatal("a socket file was opened")
	}
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("err = %v, want ErrNotRegular (an unrecognized errno must not surface as an opaque internal error)", err)
	}
	if _, err := g.Head(sockPath, 64); !errors.Is(err, ErrNotRegular) {
		t.Errorf("Head: err = %v, want ErrNotRegular", err)
	}
	if _, err := g.Meta(sockPath, true); !errors.Is(err, ErrNotRegular) {
		t.Errorf("Meta: err = %v, want ErrNotRegular", err)
	}
	// It is still listed, like a FIFO.
	es, err := g.List(filepath.Join(root, "proj"))
	if err != nil || !contains(names(es), "app.sock") {
		t.Fatalf("socket file should be listed: %v %v", names(es), err)
	}
}

func TestListFuncBatchesAndFilters(t *testing.T) {
	fx := newFixture(t)
	dir := filepath.Join(fx.home, "many")
	for i := 0; i < 25; i++ {
		write(t, filepath.Join(dir, "f"+string(rune('a'+i))), "x")
	}
	write(t, filepath.Join(dir, ".DS_Store"), "x") // hidden by the default ignore rules

	var chunks, total int
	err := fx.g.ListFunc(dir, 10, func(es []Entry) error {
		chunks++
		total += len(es)
		if len(es) > 10 {
			t.Errorf("chunk of %d exceeds the batch size", len(es))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 25 || chunks < 3 {
		t.Fatalf("total=%d chunks=%d, want 25 entries in at least 3 chunks", total, chunks)
	}
}

func TestListFuncStopsWhenTheCallbackFails(t *testing.T) {
	fx := newFixture(t)
	dir := filepath.Join(fx.home, "many")
	for i := 0; i < 20; i++ {
		write(t, filepath.Join(dir, "f"+string(rune('a'+i))), "x")
	}
	boom := errors.New("client went away")
	calls := 0
	err := fx.g.ListFunc(dir, 5, func([]Entry) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err=%v calls=%d, want the callback error after one call", err, calls)
	}
}

func TestListFuncErrorBeforeAnyCallback(t *testing.T) {
	fx := newFixture(t)
	called := false
	err := fx.g.ListFunc(filepath.Join(fx.home, ".ssh"), 10, func([]Entry) error { called = true; return nil })
	if !errors.Is(err, ErrNotFound) || called {
		t.Fatalf("err=%v called=%v; a denied directory must fail before streaming anything", err, called)
	}
}

func TestListOnAFileIsNotADirectory(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.g.List(filepath.Join(fx.home, "proj", "hello.txt")); !errors.Is(err, ErrNotDir) {
		t.Fatalf("err = %v", err)
	}
}

// A racing symlink swap must never yield the denied file's bytes.
func TestSymlinkSwapRaceNeverLeaksSecret(t *testing.T) {
	fx := newFixture(t)
	link := filepath.Join(fx.home, "proj", "flip")
	pub := filepath.Join(fx.home, "proj", "hello.txt")
	sec := filepath.Join(fx.home, ".ssh", "id_ed25519")
	if err := os.Symlink(pub, link); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	flipped := make(chan struct{})
	go func() {
		defer close(flipped)
		tmp := link + ".tmp"
		targets := []string{sec, pub}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			os.Remove(tmp)
			if os.Symlink(targets[i%2], tmp) == nil {
				os.Rename(tmp, link) // atomic replace
			}
		}
	}()

	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		f, _, err := fx.g.Open(link)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(f)
		f.Close()
		if string(b) == "SECRET" {
			close(stop)
			<-flipped
			t.Fatal("leaked denied file contents through a symlink swap race")
		}
	}
	close(stop)
	<-flipped
}

func TestInRoots(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	for _, d := range []string{"u/a/b", "ux", "Data/x", "DataMore/x", "etc"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g, err := New([]string{filepath.Join(base, "u"), filepath.Join(base, "Data")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{
		"u":          true,
		"u/a/b":      true,
		"ux":         false, // shares a prefix with the root
		"":           false, // the parent of the roots
		"Data/x":     true,
		"DataMore/x": false,
		"etc":        false,
	} {
		full := filepath.Join(base, p)
		if got := g.inRoots(full); got != want {
			t.Errorf("inRoots(%q) = %v, want %v", full, got, want)
		}
	}
	// On a case-insensitive volume another spelling of the same folder is the
	// same folder (identity, not spelling, decides).
	if _, err := os.Stat(filepath.Join(base, "U")); err == nil {
		if !g.inRoots(filepath.Join(base, "U", "A", "B")) {
			t.Error("a different spelling of the same folder must be inside the root")
		}
	}
	if g.inRoots("/nonexistent/place") {
		t.Error("a path that does not exist cannot be inside a root")
	}
	root := &Guard{roots: []string{"/"}}
	if !root.inRoots("/anything/at/all") {
		t.Error("root '/' should contain everything")
	}
}

func TestNewValidatesRoots(t *testing.T) {
	if _, err := New(nil, nil, nil); err == nil {
		t.Error("no roots should be an error")
	}
	if _, err := New([]string{"relative"}, nil, nil); err == nil {
		t.Error("relative root should be an error")
	}
	if _, err := New([]string{"/definitely/not/here"}, nil, nil); err == nil {
		t.Error("missing root should be an error")
	}
	f := filepath.Join(t.TempDir(), "file")
	write(t, f, "x")
	if _, err := New([]string{f}, nil, nil); err == nil {
		t.Error("a file root should be an error")
	}
}
