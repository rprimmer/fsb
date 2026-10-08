//go:build linux

package server

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rprimmer/fsb/internal/guard"
	"github.com/rprimmer/fsb/internal/rules"
)

// With / as a root on Linux, /proc holds every process's environment and
// command line, where API keys and passwords often are. fsb shows none of it,
// for two reasons: the kernel reports those files as empty (size 0), and a
// preview or download never goes past a file's size; and their entries are
// separated by NUL bytes, so even read, they would be classed as binary and
// not shown. This pins that down for every endpoint, directly and through a
// symbolic link that looks like a Markdown file.
func TestProcessSecretsStayHiddenUnderSystemRoot(t *testing.T) {
	const secret = "TOPSECRET4711"
	e := newEnvWith(t, false, nil, func(c *Config) {
		deny, err := rules.Parse(strings.NewReader(strings.Join(rules.CoreDeny, "\n")), rules.ParseOptions{Home: c.Home})
		if err != nil {
			t.Fatal(err)
		}
		hide, err := rules.Parse(strings.NewReader(""), rules.ParseOptions{Home: c.Home, AllowNegation: true})
		if err != nil {
			t.Fatal(err)
		}
		g, err := guard.New([]string{"/"}, deny, hide)
		if err != nil {
			t.Fatal(err)
		}
		c.Guard = g
	})

	// The secret is in the child's environment and on its command line.
	cmd := exec.Command("sh", "-c", "sleep 60; : "+secret)
	cmd.Env = append(os.Environ(), "AWS_SECRET_ACCESS_KEY="+secret)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	proc := "/proc/" + strconv.Itoa(cmd.Process.Pid)
	// Until the child has finished starting sh, /proc still shows the
	// environment it inherited.
	for i := 0; ; i++ {
		data, err := os.ReadFile(proc + "/environ")
		if err == nil && strings.Contains(string(data), secret) {
			break
		}
		if i == 100 {
			t.Fatalf("the test's own setup is wrong: %s/environ lacks the secret (%v)", proc, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	link := filepath.Join(e.home, "notes.md")
	if err := os.Symlink(proc+"/environ", link); err != nil {
		t.Fatal(err)
	}

	paths := []string{proc + "/environ", proc + "/cmdline", proc + "/task/" + strconv.Itoa(cmd.Process.Pid) + "/environ", link}
	for _, p := range paths {
		for _, q := range []url.Values{
			{"bytes": {"65536"}}, {}, // head
		} {
			q.Set("path", p)
			if code, body := e.getQuery(t, "/api/head", q); strings.Contains(body, secret) {
				t.Errorf("head %s (%s) shows the secret: %d %s", p, q.Get("bytes"), code, body)
			}
		}
		for _, ep := range []string{"file", "meta", "preview", "pdf", "mdframe", "archive", "quicklook"} {
			if code, body := e.get(t, "/api/"+ep, p); strings.Contains(body, secret) {
				t.Errorf("%s %s shows the secret: %d %.200s", ep, p, code, body)
			}
		}
	}
}
