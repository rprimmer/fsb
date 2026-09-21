package guard

import (
	"os"
	"sort"
	"strings"
	"sync"
)

// On macOS the data volume is mounted at /System/Volumes/Data, and "firmlinks"
// make parts of it appear at ordinary paths: /Users is the same directory as
// /System/Volumes/Data/Users. Firmlinks are not symlinks, so resolving
// symlinks does not collapse them, and a rule written for /Users/me/.ssh would
// not match /System/Volumes/Data/Users/me/.ssh even though it is the same file.
// Every path is therefore reduced to its ordinary form before it is compared
// with a root or a rule.

const dataVolume = "/System/Volumes/Data"

type firmlink struct {
	system string // ordinary path, e.g. /Users
	data   string // the same place under the data volume, e.g. /Users (leading slash, no volume prefix)
}

var (
	firmlinksOnce sync.Once
	firmlinks     []firmlink
)

// builtinFirmlinks is used when /usr/share/firmlinks cannot be read; it
// mirrors that file on current macOS releases.
var builtinFirmlinks = []string{
	"/AppleInternal", "/Applications", "/Library", "/Users", "/Volumes", "/cores", "/opt", "/pkg", "/private",
	"/usr/local", "/usr/libexec/cups", "/usr/share/snmp",
	"/System/Library/Caches", "/System/Library/Assets", "/System/Library/PreinstalledAssets",
	"/System/Library/AssetsV2", "/System/Library/PreinstalledAssetsV2", "/System/Library/Speech",
	"/System/Library/CoreServices/CoreTypes.bundle/Contents/Library",
}

func loadFirmlinks() {
	if data, err := os.ReadFile("/usr/share/firmlinks"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Split(strings.TrimSpace(line), "\t")
			if len(parts) == 2 && strings.HasPrefix(parts[0], "/") && parts[1] != "" {
				firmlinks = append(firmlinks, firmlink{system: parts[0], data: "/" + strings.TrimLeft(parts[1], "/")})
			}
		}
	}
	if len(firmlinks) == 0 {
		for _, p := range builtinFirmlinks {
			firmlinks = append(firmlinks, firmlink{system: p, data: p})
		}
	}
	// Longest first, so the most specific firmlink wins.
	sort.Slice(firmlinks, func(i, j int) bool { return len(firmlinks[i].data) > len(firmlinks[j].data) })
}

// stripPathPrefix reports whether p is prefix or lies beneath it and, if so,
// returns what remains of p ("" or "/x/y"). Components are compared as APFS
// compares names (Fold), one at a time, so spelling "System" with a long s or
// "Users" with a ligature still names the same place; slicing by byte length
// would cut such a spelling in the middle of a character.
func stripPathPrefix(p, prefix string) (string, bool) {
	rest := p
	for _, want := range strings.Split(strings.Trim(prefix, "/"), "/") {
		if want == "" {
			continue
		}
		rest = strings.TrimPrefix(rest, "/")
		comp, tail, _ := strings.Cut(rest, "/")
		if comp == "" || fold(comp) != fold(want) {
			return "", false
		}
		if tail == "" && !strings.Contains(rest, "/") {
			rest = ""
		} else {
			rest = "/" + tail
		}
	}
	return rest, true
}

// canonPath maps a path under the data volume to the ordinary path it is a
// firmlink for. Any other path is returned unchanged.
func canonPath(p string) string {
	rest, ok := stripPathPrefix(p, dataVolume)
	if !ok {
		return p
	}
	firmlinksOnce.Do(loadFirmlinks)
	for _, fl := range firmlinks {
		if r, ok := stripPathPrefix(rest, fl.data); ok {
			return fl.system + r
		}
	}
	return p
}
