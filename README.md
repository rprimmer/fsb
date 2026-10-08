# fsb

`fsb` (filesystem browser): a read-only, local-only web view of your filesystem. One Go binary serves a browser UI on `127.0.0.1`; your files are only ever read, never changed, and nothing is reachable from the network. (fsb writes only its own files: its settings in `~/.config/fsb/`, and short-lived private copies of documents it hands to Quick Look, removed as soon as the picture is drawn.)

> **Status: 1.0, finished, and provided as-is.** Browse, sort, filter, search and preview from a virtualized listing in your browser, on top of the security core (path guard, rule matcher, localhost protections). See the [functional specification](specs/functional/) and the [design specification](specs/design/).
>
> This is a personal project, published in case it is useful to others. It is not actively maintained: bug reports may be attended to, but there is no guarantee of support, fixes or new features.

### Keyboard

| Key | Action |
|---|---|
| `↑` `↓` `PgUp` `PgDn` `Home` `End` | Select |
| `Enter` or `→` | Open a folder, selecting its first entry (a search hit opens its folder with the file selected) |
| `←` or `Backspace` | Up one folder (re-selecting the one you left) |
| `Space` | Show or hide the preview pane |
| `/` | Filter this folder |
| `Alt+S` | Search subfolders by name (Enter runs it; `Esc` clears) |
| `Alt+G` | Go to a path |
| `Alt+C` | Copy the selected path (or the folder's, if nothing is selected) |
| any letter or digit | Jump to (and, repeated, cycle through) entries whose name starts with it |

### Preview, hover peek, search, attributes

- **Preview pane:** raster images (PNG, JPEG, GIF, WebP), syntax-highlighted code, Markdown (rendered in a sandboxed frame, with a Source toggle; local images and links to other files work, remote images are never loaded), pretty-printed JSON, CSV/TSV as a table, plain text, and for Numbers, Pages and Office documents the picture macOS Quick Look draws (as Finder shows it; made by running `qlmanage` on a private copy fsb reads after the usual checks, never on your own file and never for a cloud-only one), plus details and extended attributes. Images are served sandboxed and identified by their bytes, never their names; SVG and HTML are never rendered.
- **Hover peek:** hover a readable file for a moment to see its first lines. Turn it off with the "Hover previews" checkbox. It never reads a file that is stored only in the cloud (functional specification, SEC-13).
- **Names are shown as they are:** a name containing an invisible or reordering character (a right-to-left override, a newline, a control character) is shown with a visible marker such as `‹U+202E›`, so `invoice‮txt.exe` cannot pass for a `.txt` file. Real right-to-left text is untouched, and downloads keep the real name.
- **Search:** filename search under the current folder, shallowest matches first. It never enters or reports denied or hidden folders and does not follow symlinked folders.
- **Extended attributes:** shown in the preview pane, and as an optional last column (Columns menu), fetched only for the rows on screen.
- **PDF:** shown in your browser's built-in viewer, and only when the file's bytes say it is a PDF.
- **Archives:** zip, tar and tar.gz files show their table of contents (never extracted), recognized by content and bounded against zip bombs.

Columns can be resized (drag the edge; double-click to fit) and reordered (drag a header, or Alt+Left/Right); the layout is remembered in your browser, and "Reset columns" restores the defaults.

The UI lists directories with 100,000+ entries: the API streams the listing in chunks, so the first rows appear immediately, and only the visible rows are rendered. Paths live in the URL fragment (`#/Users/me/docs`), so a folder is bookmarkable while fsb is running.

## Quick start

`fsb` runs on macOS and is installed from source; it needs [Go](https://go.dev/dl/) 1.26 or later. There are no prebuilt binaries and no Homebrew formula. A program you build yourself is not quarantined, so macOS does not ask to verify it.

```sh
go install github.com/rprimmer/fsb/cmd/fsb@latest   # installs fsb into $(go env GOPATH)/bin
fsb --version       # the version and the commit it was built from
fsb --init          # write ~/.config/fsb/{ignore,deny}
fsb --show-deny     # print the deny rules in effect (--show-ignore: the hide rules)
fsb                 # serve $HOME read-only, print a single-use URL
```

To install the program and its manual page from a clone of this repository:

```sh
make install                                   # /usr/local/bin/fsb and /usr/local/share/man/man1/fsb.1
make install PREFIX=/opt/homebrew              # a different prefix
make install MANDIR=$HOME/.local/share/man BINDIR=$HOME/bin
make install-man                               # only the manual page
make uninstall
```

`PREFIX`, `BINDIR`, `MANDIR` (the folder that holds `man1`) and `DESTDIR` (a staging root for packaging) can each be overridden; `make help` shows the values in effect.

`fsb [path]` narrows the root; `--browser "Google Chrome"` opens it in that application instead of your default browser (or use `--no-open` and paste the printed URL); `--root PATH` (repeatable) adds roots such as `/Volumes/X`. Serving `/` needs `--allow-system-root`, also when a root only resolves to `/` (a symbolic link to it).

`fsb` is a small web server: like any other program that keeps serving requests, it does not exit on its own, because exiting would stop the browser from being able to reach it. To get your shell prompt back, run `fsb --background`: it prints the URL, returns once it is serving, and keeps running after the terminal closes; `fsb --stop` stops it, and `fsb --status` says whether one is running. (`fsb &` with `fg` and Control-C also works.) If the usual port is taken, `fsb` names the program holding it; `fsb --stop` also stops an `fsb` left running in another terminal. `--no-open` and an unopenable `--browser` name do not change this: `fsb` keeps serving so the printed URL stays usable, so pair either with `--background` if you want your prompt back.

The browser keeps preferences such as column widths and the preview pane's width per address, including the port, so `fsb` remembers the port it last used successfully (in `~/.config/fsb/lastport`) and reuses it, rather than picking a new one every time; the first run, with nothing to reuse yet, picks a free port as before. If that remembered port is ever taken by something else, `fsb` says so and asks you to pick another with `--port N` for that run, rather than silently moving to a different port and losing the address your preferences are keyed to. `--port N` also works any time you want a specific port; a value chosen this way is used just for that run and is not remembered.

## Security model

- **Loopback only.** Binds `127.0.0.1`; the interface is not configurable. The port is remembered across restarts by default (see above) or fixed with `--port`, but it is always loopback-only.
- **Rebinding and cross-site defenses.** Requests must carry an allowed `Host`, must not have a foreign `Origin`/`Referer`, and must not be cross-site per `Sec-Fetch-Site`.
- **Single-use launch token.** The URL printed at startup carries a token that is exchanged once for an `HttpOnly`, `SameSite=Strict` session cookie. The token is never logged.
- **Cookie scoped to a random path.** Browsers scope cookies by host and never by port, so a plain cookie would be sent to every other web server on `127.0.0.1` that you visit. fsb therefore serves everything under a random per-launch prefix (`http://127.0.0.1:PORT/<random>/`) and sets the cookie's `Path` to it: the browser sends it only for URLs under that prefix, which no other server has. Any URL outside the prefix is refused. Open the printed URL (bookmarks do not survive a restart).
- **Read-only.** Only `GET`/`HEAD` are served, and nothing it serves can be modified through it. The only files `fsb` writes are its own, in `~/.config/fsb/`: the rule files `--init` creates (never overwriting one), the last port, and the background process's PID and log.
- **One chokepoint.** Every filesystem access goes through `internal/guard`. A test fails the build if HTTP-facing packages import `os`, `io/fs`, `syscall` or `path/filepath`.
- **Symlinks and races.** The file is opened first, then its real path is resolved and verified to be the same file before any bytes are served. Symlinks that lead outside the roots or into denied paths are refused.

### Two exclusion tiers

| | `~/.config/fsb/ignore` (hide) | `~/.config/fsb/deny` (block) |
|---|---|---|
| Listings and search | entry omitted | entry omitted |
| Direct access by path | **allowed** | **blocked (404)** |
| Syntax | gitignore, `!` allowed | gitignore, `!` not allowed |

Deny always wins. A denied path answers the same 404 as a missing one (add `--debug` to see which rule matched). Deny rules load only from the global file.

**Core deny rules are on by default**, even with no deny file: `.ssh`, `.aws`, `.gnupg`, `.config/gh`, `.netrc`, `.kube`, Keychains, Chrome/Firefox/Safari data, and on Linux the Chrome, Chromium and Firefox profiles (`~/.config/google-chrome*`, `~/.config/chromium`, `~/.mozilla`), the GNOME and KDE keyrings and the `pass` store (`~/.password-store`), all matched wherever they appear (so a backup or clone of your home folder, or another account's, is covered too), and secret files anywhere: `.env`, `.env.*`, `*.pem`, `*.key`, private keys copied out of `.ssh` (`id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519` and the `_sk` variants, but not their `.pub`), certificate and key stores (`*.p12`, `*.pfx`, `*.ppk`, `*.jks`, `*.keystore`), password databases (`*.kdbx`, `*.keychain`, `*.keychain-db`), and `.git-credentials` and `.pgpass`. Situational rules (`~/.docker/config.json`, `~/.npmrc`, Mail and Messages) are in the generated deny file, commented out. If you remove a core rule from the file, fsb warns at startup.

The secret-file patterns are deliberately broad, so they have costs: **`*.key` also matches Keynote presentations**, `.env` also matches a directory named `.env` (such as a Python virtualenv), and `.env.*` also matches templates like `.env.example`. If that gets in your way, remove the pattern from `~/.config/fsb/deny` (run `fsb --init` first to create it) and accept the startup warning.

See [specs/SECURITY.md](specs/SECURITY.md) for the threat model, what is and is not guaranteed, and how it has been tested.

### Known limitations

- Rules are path-based, so a hard link (another name for the same file) is judged by file identity: if any name of a file with several names is denied, every name is refused, and it disappears from listings. The names are found by an index of the served roots and the credential folders of every home directory, built in the background at startup (about 20 seconds for a home folder of 700,000 files). A file with several names is served only when the index has found every one of its names, none is denied, and each is still that file at a real path (no symbolic link along it) when it is used. Otherwise it is refused until the index is refreshed (at most every 15 seconds): while the index is being built, when a name lies outside the roots and credential folders (a pnpm store outside the root, say) or in a folder fsb cannot read, and after its names change (a link added or removed, or a file or folder renamed or moved). Names are counted as the file system compares them, so `a.txt` and `A.TXT` are one name.
- Rule files are read at startup; restart to apply changes.
- Any process running as you can read the same files; fsb is not a sandbox against local malware.

## Documentation

- **Manual page:** `man ./man/fsb.1` (hand-written roff; check with `mandoc -Tlint man/fsb.1`). Install it as `fsb.1` in a `man1` folder on your `MANPATH`.
- **[Functional specification](specs/functional/fsb-functional.pdf):** what `fsb` does, as an outside observer sees it. Every requirement has a stable identifier (`RUL-4`, `SEC-7`, ...) that the other documents and the tests cite.
- **[Design specification](specs/design/fsb-design.pdf):** how it is built and why, the alternatives rejected, how it is tested, and its limits. It also states, exhaustively, the exact machine and tool versions everything was tested with; traces every functional requirement to the tests that check it (and says plainly which have none); and lists, up front, what has not been tested (other platforms and browsers, opt-in and skipped tests, real cloud placeholders, CI, an independent review).
- **[Algebraic specification](specs/algebra/fsb-algebra.pdf):** a formal statement of the security core (rule matcher, the access decision every endpoint must agree with, content-typed endpoints, frontend order/filter/navigation, the preview state machine) as laws, each tied to an executable check. Deriving it, auditing every name comparison, and a second independent review found eighteen defects, all fixed (see its Findings section).
- **[Security policy](specs/SECURITY.md):** the threat model, what is and is not guaranteed, and how it was tested.
- **[Presentation](docs/talk/fsb-talk.pdf):** twelve Beamer slides on how `fsb` is built: the architecture, startup, the path of one request, the guard's checks and the login exchange, with a call graph generated from the code. `make -C docs/talk` rebuilds it (also needs Graphviz and `golang.org/x/tools/cmd/callgraph`).

The specifications are LaTeX. The built PDFs are committed next to their sources. To rebuild them (needs a TeX installation with `latexmk`):

```sh
make -C specs            # all three; or run make in one folder
make -C specs overleaf   # source-only zips in specs/*/dist/ for Overleaf
```

To edit on [Overleaf](https://www.overleaf.com) instead of installing TeX, upload `specs/dist/fsb-specs-overleaf.zip` as a new project (pdfLaTeX): it holds all three documents in one project, since they share `specs/common/`, and you pick which one compiles from Overleaf's file list. See [specs/OVERLEAF.md](specs/OVERLEAF.md).

## Development

The compiled frontend in `web/dist` is committed, so building the Go binary needs no Node. To change the UI you need [Node](https://nodejs.org/) 24 or later (CI uses 24; the tests rely on Node running TypeScript directly):

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

Linux distributions (Debian, Ubuntu, Fedora, Alpine, Arch) in Docker; see [e2e/linux/README.md](e2e/linux/README.md):

```sh
make linux
```

## License

MIT
