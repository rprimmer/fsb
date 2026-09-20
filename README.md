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

- **Preview pane:** raster images (PNG, JPEG, GIF, WebP), syntax-highlighted code and Markdown source, pretty-printed JSON, CSV/TSV as a table, and plain text, plus details and extended attributes. Images are served sandboxed and identified by their bytes, never their names; SVG and HTML are never rendered.
- **Hover peek:** hover a readable file for a moment to see its first lines. Turn it off with the "Hover previews" checkbox. It never reads a file that is stored only in the cloud (see the PRD, SR-9).
- **Search:** filename search under the current folder, shallowest matches first. It never enters or reports denied or hidden folders and does not follow symlinked folders.
- **Extended attributes:** shown in the preview pane, and as an optional last column (Columns menu), fetched only for the rows on screen.
- **Not yet:** PDF preview, rendered (as opposed to highlighted) Markdown, and archive listings.

Columns can be resized (drag the edge; double-click to fit) and reordered (drag a header, or Alt+Left/Right); the layout is remembered in your browser, and "Reset columns" restores the defaults.

The UI lists directories with 100,000+ entries: the API streams the listing in chunks, so the first rows appear immediately, and only the visible rows are rendered. Paths live in the URL fragment (`#/Users/me/docs`), so a folder is bookmarkable while fsb is running.

## Quick start

```sh
go build -o fsb ./cmd/fsb
./fsb --init        # write ~/.config/fsb/{ignore,deny}
./fsb               # serve $HOME read-only, print a single-use URL
```

`fsb [path]` narrows the root; `--root PATH` (repeatable) adds roots such as `/Volumes/X`. Serving `/` needs `--allow-system-root`.

## Security model

- **Loopback only.** Binds `127.0.0.1` on a random port; not configurable.
- **Rebinding and cross-site defenses.** Requests must carry an allowed `Host`, must not have a foreign `Origin`/`Referer`, and must not be cross-site per `Sec-Fetch-Site`.
- **Single-use launch token.** The URL printed at startup carries a token that is exchanged once for an `HttpOnly`, `SameSite=Strict` session cookie. The token is never logged.
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

**Core deny rules are on by default**, even with no deny file: `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/gh`, `~/.netrc`, `~/.kube`, Keychains, Chrome/Firefox/Safari data, and secret files anywhere: `.env`, `.env.*`, `*.pem` and `*.key`. Situational rules (`~/.docker/config.json`, `~/.npmrc`, Mail and Messages) are in the generated deny file, commented out. If you remove a core rule from the file, fsb warns at startup.

The secret-file patterns are deliberately broad, so they have costs: **`*.key` also matches Keynote presentations**, `.env` also matches a directory named `.env` (such as a Python virtualenv), and `.env.*` also matches templates like `.env.example`. If that gets in your way, remove the pattern from `~/.config/fsb/deny` (run `fsb --init` first to create it) and accept the startup warning.

### Known limitations

- Rules are path-based: a hard link to a denied file elsewhere is not detected.
- Rule files are read at startup; restart to apply changes.
- Any process running as you can read the same files; fsb is not a sandbox against local malware.

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
