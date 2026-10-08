package rules

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// CoreDeny lists locations and files that hold credentials or secrets. They are
// active by default and compiled in, so they apply even if no deny file exists.
// The list is versioned with the release; additions are called out in release
// notes.
//
// The rules from ".env" on are patterns that match at any depth. They are deliberately
// broad: ".env"/".env.*" also match a directory named .env (such as a Python
// virtualenv) and committed templates like .env.example, and "*.key" is also
// the extension of Keynote presentations. Anyone who needs those can edit their
// deny file; fsb then warns that a core rule is off (see LoadDeny).
var CoreDeny = []string{
	".ssh/",
	".aws/",
	".gnupg/",
	"**/.config/gh/",
	".netrc",
	".kube/",
	"**/Library/Keychains/",
	"**/Library/Application Support/Google/Chrome/",
	"**/Library/Application Support/Firefox/",
	"**/Library/Application Support/BraveSoftware/",
	"**/Library/Application Support/Microsoft Edge*/",
	"**/Library/Safari/",
	"**/Library/Cookies/",
	// The same on Linux: browser profiles (also inside Snap and Flatpak
	// sandboxes), the GNOME and KDE keyrings, and the pass password store.
	"**/.config/google-chrome*/",
	"**/.config/chromium/",
	"**/.config/BraveSoftware/",
	"**/.config/microsoft-edge*/",
	// Flatpak keeps each app's settings in ~/.var/app/<app ID>/config; any app
	// ID, so repackaged builds (ungoogled-chromium, say) are covered too.
	"**/.var/app/*/config/google-chrome*/",
	"**/.var/app/*/config/chromium/",
	"**/.var/app/*/config/BraveSoftware/",
	"**/.var/app/*/config/microsoft-edge*/",
	"**/.mozilla/",
	"**/.local/share/keyrings/",
	"**/.local/share/kwalletd/",
	"**/.password-store/",
	".env",
	".env.*",
	"*.pem",
	"*.key",
	// Secrets copied out of their folders keep the names that identify them.
	"id_rsa",
	"id_dsa",
	"id_ecdsa",
	"id_ed25519",
	"id_ecdsa_sk",
	"id_ed25519_sk",
	"*.p12",
	"*.pfx",
	"*.ppk",
	"*.jks",
	"*.keystore",
	"*.kdbx",
	"*.keychain",
	"*.keychain-db",
	".git-credentials",
	".pgpass",
}

// CredentialLocations returns the credential folders and files under each home
// directory that fsb knows of: the given home and every folder in /Users and
// /home. They mirror the credential entries of CoreDeny for code that must
// recognize the same files by identity (hard links).
func CredentialLocations(home string) []string {
	rel := []string{
		".ssh", ".aws", ".gnupg", ".kube", ".netrc", ".config/gh", "Library/Keychains",
		"Library/Application Support/Google/Chrome", "Library/Application Support/Firefox",
		"Library/Application Support/BraveSoftware", "Library/Application Support/Microsoft Edge",
		"Library/Safari", "Library/Cookies",
		".config/google-chrome", ".config/chromium", ".config/BraveSoftware", ".config/microsoft-edge",
		".mozilla", ".local/share/keyrings", ".local/share/kwalletd", ".password-store",
		".var/app/com.google.Chrome/config/google-chrome", ".var/app/org.chromium.Chromium/config/chromium",
		".var/app/com.brave.Browser/config/BraveSoftware", ".var/app/com.microsoft.Edge/config/microsoft-edge",
	}
	homes := map[string]bool{home: true}
	for _, parent := range homeParents {
		if es, err := os.ReadDir(parent); err == nil {
			for _, e := range es {
				if e.IsDir() {
					homes[parent+"/"+e.Name()] = true
				}
			}
		}
	}
	var out []string
	for h := range homes {
		for _, r := range rel {
			out = append(out, h+"/"+r)
		}
	}
	sort.Strings(out)
	return out
}

// homeParents are the folders that hold the home directories (a variable so
// tests can point it elsewhere).
var homeParents = []string{"/Users", "/home"}

// OptionalDeny lists situational rules shipped commented out.
var OptionalDeny = []string{
	"~/.docker/config.json",
	"~/.npmrc",
	"~/Library/Mail/",
	"~/Library/Messages/",
}

// DefaultIgnore is the built-in hide list, used when no ignore file exists.
var DefaultIgnore = []string{
	".DS_Store",
	".git/",
	"node_modules/",
}

// DenyTemplate returns the contents `fsb --init` writes to the deny file.
func DenyTemplate() string {
	var b strings.Builder
	b.WriteString("# fsb deny rules: paths matching these are never served (HTTP 404).\n")
	b.WriteString("# Global config only; gitignore syntax; negation (!) is not allowed.\n")
	b.WriteString("# Deny always wins over ignore.\n\n")
	b.WriteString("# --- Core (active by default: known credential/secret locations and files) ---\n")
	b.WriteString("# Removing a core rule makes fsb warn at startup and in the UI.\n")
	for _, r := range CoreDeny {
		b.WriteString(r + "\n")
	}
	b.WriteString("\n# --- Optional (commented out: uncomment to enable) ---\n")
	for _, r := range OptionalDeny {
		b.WriteString("# " + r + "\n")
	}
	return b.String()
}

// IgnoreTemplate returns the contents `fsb --init` writes to the ignore file.
func IgnoreTemplate() string {
	var b strings.Builder
	b.WriteString("# fsb ignore rules: matching entries are hidden from listings and search.\n")
	b.WriteString("# They stay reachable by direct path; use the deny file to block access.\n")
	b.WriteString("# gitignore syntax, including ! negation.\n\n")
	for _, r := range DefaultIgnore {
		b.WriteString(r + "\n")
	}
	return b.String()
}

// CoreMissing returns core rules that are not present in s.
func CoreMissing(s *Set) []string {
	// Compared as names are compared (Fold), so "~/.SSH" is recognized as the
	// core rule "~/.ssh". Equal text always denies the same things, so this never
	// reports a rule as present that is not ("~/.aws/" is a different rule from
	// "~/.aws": it applies to directories only).
	norm := Fold
	have := map[string]bool{}
	for _, t := range s.Texts() {
		have[norm(t)] = true
	}
	var missing []string
	for _, c := range CoreDeny {
		if !have[norm(c)] {
			missing = append(missing, c)
		}
	}
	return missing
}

// LoadDeny loads the deny file. If the file does not exist the built-in core
// rules apply. If it exists it is authoritative, and coreMissing lists any core
// rules the user removed or commented out (the caller must warn about them).
// A malformed file is an error: the caller must refuse to start.
func LoadDeny(path, home string) (set *Set, coreMissing []string, err error) {
	opts := ParseOptions{Home: home}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		s, perr := Parse(strings.NewReader(strings.Join(CoreDeny, "\n")), opts)
		return s, nil, perr
	}
	if err != nil {
		return nil, nil, err
	}
	s, err := Parse(strings.NewReader(string(data)), opts)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, CoreMissing(s), nil
}

// LoadIgnore loads the global ignore file, falling back to DefaultIgnore.
func LoadIgnore(path, home string) (*Set, error) {
	opts := ParseOptions{Home: home, AllowNegation: true}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Parse(strings.NewReader(strings.Join(DefaultIgnore, "\n")), opts)
	}
	if err != nil {
		return nil, err
	}
	s, err := Parse(strings.NewReader(string(data)), opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}
