# fsb

A read-only, local-only web view of your filesystem. One Go binary serves a browser UI on `127.0.0.1`; nothing is ever written, and nothing is reachable from the network.

> **Status: M2 (walk), first cut.** Browse, sort, filter, search and preview from a virtualized listing in your browser, on top of the security core (path guard, rule matcher, localhost protections). See [PRD/PRD.md](PRD/PRD.md).

### Keyboard

| Key | Action |
|---|---|
| `↑` `↓` `PgUp` `PgDn` `Home` `End` | Select |
| `Enter` or `→` | Open a folder (a search hit opens its folder with the file selected) |
| `←` or `Backspace` | Up one folder (re-selecting the one you left) |
| `Space` | Show or hide the preview pane |
| `/` | Filter this folder |
| `s` | Search subfolders by name (Enter runs it; `Esc` clears) |
| `c` | Copy the selected path (or the folder's, if nothing is selected) |

### Preview, hover peek, search, attributes

- **Preview pane:** raster images (PNG, JPEG, GIF, WebP), syntax-highlighted code, Markdown (rendered in a sandboxed frame, with a Source toggle; local images and links to other files work, remote images are never loaded), pretty-printed JSON, CSV/TSV as a table, and plain text, plus details and extended attributes. Images are served sandboxed and identified by their bytes, never their names; SVG and HTML are never rendered.
- **Hover peek:** hover a readable file for a moment to see its first lines. Turn it off with the "Hover previews" checkbox. It never reads a file that is stored only in the cloud (see the PRD, SR-9).
- **Search:** filename search under the current folder, shallowest matches first. It never enters or reports denied or hidden folders and does not follow symlinked folders.
- **Extended attributes:** shown in the preview pane, and as an optional last column (Columns menu), fetched only for the rows on screen.
- **PDF:** shown in your browser's built-in viewer, and only when the file's bytes say it is a PDF.
- **Archives:** zip, tar and tar.gz files show their table of contents (never extracted), recognized by content and bounded against zip bombs.

Columns can be resized (drag the edge; double-click to fit) and reordered (drag a header, or Alt+Left/Right); the layout is remembered in your browser, and "Reset columns" restores the defaults.

The UI lists directories with 100,000+ entries: the API streams the listing in chunks, so the first rows appear immediately, and only the visible rows are rendered. Paths live in the URL fragment (`#/Users/me/docs`), so a folder is bookmarkable while fsb is running.

## Quick start

```sh
go build -o fsb ./cmd/fsb
./fsb --init        # write ~/.config/fsb/{ignore,deny}
./fsb               # serve $HOME read-only, print a single-use URL
```

`fsb [path]` narrows the root; `--browser "Google Chrome"` opens it in that application instead of your default browser (or use `--no-open` and paste the printed URL); `--root PATH` (repeatable) adds roots such as `/Volumes/X`. Serving `/` needs `--allow-system-root`.

## Security model

- **Loopback only.** Binds `127.0.0.1` on a random port; not configurable.
- **Rebinding and cross-site defenses.** Requests must carry an allowed `Host`, must not have a foreign `Origin`/`Referer`, and must not be cross-site per `Sec-Fetch-Site`.
- **Single-use launch token.** The URL printed at startup carries a token that is exchanged once for an `HttpOnly`, `SameSite=Strict` session cookie. The token is never logged.
- **Cookie scoped to a random path.** Browsers scope cookies by host and never by port, so a plain cookie would be sent to every other web server on `127.0.0.1` that you visit. fsb therefore serves everything under a random per-launch prefix (`http://127.0.0.1:PORT/<random>/`) and sets the cookie's `Path` to it: the browser sends it only for URLs under that prefix, which no other server has. Any URL outside the prefix is refused. Open the printed URL (bookmarks do not survive a restart).
- **Read-only by construction.** Only `GET`/`HEAD` are served, and the binary contains no write code.
- **One chokepoint.** Every filesystem access goes through `internal/guard`. A test fails the build if HTTP-facing packages import `os`, `io/fs`, `syscall` or `path/filepath`.
- **Symlinks and races.** The file is opened first, then its real path is resolved and verified to be the same file before any bytes are served. Symlinks that lead outside the roots or into denied paths are refused.

### Two exclusion tiers

| | `~/.config/fsb/ignore` (hide) | `~/.config/fsb/deny` (block) |
|---|---|---|
| Listings and search | entry omitted | entry omitted |
| Direct access by path | **allowed** | **blocked (404)** |
| Syntax | gitignore, `!` allowed | gitignore, `!` not allowed |

Deny always wins. A denied path answers the same 404 as a missing one (add `--debug` to see which rule matched). Deny rules load only from the global file.

**Core deny rules are on by default**, even with no deny file: `.ssh`, `.aws`, `.gnupg`, `.config/gh`, `.netrc`, `.kube`, Keychains, and Chrome/Firefox/Safari data, all matched wherever they appear (so a backup or clone of your home folder, or another account's, is covered too), and secret files anywhere: `.env`, `.env.*`, `*.pem`, `*.key`, private keys copied out of `.ssh` (`id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519` and the `_sk` variants, but not their `.pub`), certificate and key stores (`*.p12`, `*.pfx`, `*.ppk`, `*.jks`, `*.keystore`), password databases (`*.kdbx`, `*.keychain`, `*.keychain-db`), and `.git-credentials` and `.pgpass`. Situational rules (`~/.docker/config.json`, `~/.npmrc`, Mail and Messages) are in the generated deny file, commented out. If you remove a core rule from the file, fsb warns at startup.

The secret-file patterns are deliberately broad, so they have costs: **`*.key` also matches Keynote presentations**, `.env` also matches a directory named `.env` (such as a Python virtualenv), and `.env.*` also matches templates like `.env.example`. If that gets in your way, remove the pattern from `~/.config/fsb/deny` (run `fsb --init` first to create it) and accept the startup warning.

See [SECURITY.md](SECURITY.md) for the threat model, what is and is not guaranteed, and how it has been tested.

### Known limitations

- Rules are path-based, so a hard link (another name for the same file) is judged by file identity: if any name of a file with several names is denied, every name is refused, and it disappears from listings. This covers the served roots and the credential folders of every home directory. The index of such files is built in the background at startup (about 20 seconds for a home folder of 700,000 files); until it is ready, files that have several names are refused. A hard link whose other name lies outside both the roots and the credential folders cannot be seen.
- Rule files are read at startup; restart to apply changes.
- Any process running as you can read the same files; fsb is not a sandbox against local malware.

## Algebraic specification

[`algebra/`](algebra/) holds a LaTeX specification of the security core (rule matcher, the access decision every endpoint must agree with, content-typed endpoints, frontend order/filter/navigation, and the preview state machine) as laws, each tied to an executable check. Deriving it, and auditing every name comparison, found fifteen defects, all fixed (see its Findings section). Build it with `make -C algebra` (needs a TeX installation; the PDF is written to `algebra/build/`).

## Development

The compiled frontend in `web/dist` is committed, so building the Go binary needs no Node. To change the UI:

```sh
cd web
npm ci
npm test                                 # helper tests (Node's built-in runner)
npx svelte-check --tsconfig ./tsconfig.json
npm run build                            # rewrites web/dist; commit the result
```

Browser tests (real keyboard and mouse in Chrome, and Safari on macOS; see [e2e/README.md](e2e/README.md)):

```sh
node --test --test-reporter=spec e2e/chrome.test.mjs
```

Go:

```sh
go test -race ./...
go test ./internal/rules -run xxx -fuzz FuzzParseAndMatch -fuzztime 30s
```

## License

MIT
