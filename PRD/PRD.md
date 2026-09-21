# PRD: Local Read-Only Filesystem Browser

Status: Draft v0.1 · Date: 2026-09-20 · Owner: Robert Primmer
Name: `fsb`

## 1. Summary

A local-only, read-only web UI for browsing the user's own Mac filesystem. It runs as a single Go binary that serves an embedded frontend on `127.0.0.1`. Two exclusion files (hide and deny) control what is visible and what is reachable. The tool is built for the author's own use first, and is designed so others can use it via a public GitHub repo.

## 2. Background and motivation

A previous tool offered browser-based FS browsing that was sometimes better than Finder or iTerm, but it was limited. The existing fork (`rprimmer/filebrowser`, from `filebrowser/filebrowser`) is a multi-user file-sharing/management app built around a jailed root, with UI-driven config and CRUD operations. It does not fit a single-user, whole-home, read-only, security-first use case.

## 3. Goals

- G1. Fast, comfortable browsing of the entire home directory in a browser, including very large directories.
- G2. **Local only**: no network exposure beyond the loopback interface, with defenses against browser-based attacks on localhost.
- G3. **Read-only by construction**: the binary contains no write code paths.
- G4. Explicit, testable exclusion semantics via a two-tier hide/deny model.
- G5. Simple distribution: single binary, `go install`, GitHub Releases; Homebrew later.
- G6. Grow in stages (crawl, walk, run) without redesigning the security core.

## 4. Non-goals

- Any remote access, sharing, multi-user accounts, or authentication beyond the per-launch token.
- Any write operation: upload, rename, move, delete, edit.
- Cross-platform support in v1 (macOS only; Linux is plausible later, Windows is out of scope).
- Replacing Finder or a terminal for file management.
- Running as a background daemon or system service (v1).

## 5. Users

- Primary: the author, on their own Mac.
- Secondary: other developers/power users who find the repo. They should be able to install, run, and trust it after reading the README and the security model.

## 6. Key decisions (settled)

| Area | Decision |
|---|---|
| Backend | Go, single static binary, stdlib `net/http` and `io/fs` where possible |
| Frontend | Svelte + TypeScript, built with Vite, embedded via `//go:embed`; virtualized lists |
| Network | Bind `127.0.0.1` only, hard-coded (no flag to change it) |
| Mode | Read-only; GET endpoints only |
| Roots | Default is `$HOME`. A path argument narrows it; `--root PATH` (repeatable) adds explicit roots such as `/Volumes/X`. `/` as a root requires `--allow-system-root`. Symlinks whose real path falls outside all configured roots are denied by default. |
| License | MIT |
| Search | Built-in filename search (walk); content search via `mdfind`, post-filtered through the chokepoint (run) |
| Local actions | "Reveal in Finder" / "Open in editor" excluded from v1 |
| Exclusions | Two files: `ignore` (hide) and `deny` (block); see section 8 |
| Deny scope | Deny rules load from the global config only, never per-directory files |
| Deny defaults | Two sets: a **core** set of known credential/secret locations that is **active by default**, and an **optional** set of potential exclusions shipped commented out for the user to enable (see 8.3 and risk R1) |
| Denied response | HTTP 404 by default; explicit reason only with `--debug` |
| Delivery | CLI first; staged rollout |

## 7. Functional requirements

### 7.1 Crawl stage (MVP)

- FR-1. `fsb [path]` starts the server on a random free loopback port, prints the URL (including token), and opens it in the default browser (`--browser NAME` picks a macOS application instead; `--no-open` suppresses).
- FR-2. Directory listing with name, kind, size, modified time, and permissions; sortable columns; client-side filter-as-you-type.
- FR-2a. Resizable and reorderable columns. Drag a header's edge to resize (double-click or Enter on the edge fits the column to the visible rows; Left/Right resizes by 10 px, Shift by 50); drag a header onto another, or press Alt+Left/Right on it, to reorder. Widths are clamped to 60-900 px. Layout is a per-viewer preference stored in the browser's `localStorage` (never on the server), tolerates missing or corrupt storage, and has a "Reset columns" control. On narrow windows the table scrolls sideways rather than hiding columns.
- FR-3. Breadcrumb navigation and URL-addressable paths (bookmarkable while the server is running).
- FR-4. Virtualized listing that stays responsive on directories with 100k+ entries; the API streams or paginates listings.
- FR-5. Raw file download (always `Content-Disposition: attachment` with `application/octet-stream`, never rendered from the app's origin; range requests supported for large media). Inline viewing with MIME detection arrives with sandboxed previews in the walk stage (FR-10, SR-6).
- FR-6. Hidden-file toggle (dotfiles), independent of the `ignore` file.
- FR-7. Hide rules from the `ignore` files applied to listings and search; deny rules applied everywhere (see section 8).
- FR-8. `--debug` flag: verbose logging, including which deny/ignore rule matched a path, and, for denied paths only, a response body naming the matching rule.
- FR-9. Graceful handling of permission errors, broken symlinks, and special files (sockets, devices, FIFOs are listed but never opened).
- FR-9a. **Unprotected-state warning (R1 mitigation).** Because the core deny rules are active by default (section 8.3), the warning now covers only the case where the user has weakened them. When one or more built-in core rules are not in effect (removed or commented out of the deny file), the tool (a) prints a startup notice naming the uncovered paths, e.g. `warning: core deny rule(s) disabled: ~/.ssh/, ~/.aws/; these paths are browsable.`, and (b) shows a persistent banner in the UI with the same message and a pointer to the README section on deny rules. Both disappear once every core rule is back in effect. The state is computed server-side by comparing the loaded rules to the built-in core list, and it cannot be suppressed by a flag.

### 7.2 Walk stage

- FR-10. Previews in a side pane for the selected entry: raster images (PNG, JPEG, GIF, WebP), source code (syntax highlighted), Markdown, JSON (pretty-printed), CSV (table), and plain text and logs. Text is shown from a bounded head of the file (default 64 KB) and always inserted as text, never as HTML. Markdown is rendered by default (with a Source toggle): formatting, tables and task lists; raw HTML is shown as text; relative links open the target inside fsb, http(s) links open in a new tab without referrer, other schemes are shown as plain text; images inside the roots are shown via the guarded preview endpoint and remote images are replaced by a placeholder, so previewing never makes an outside request. It is sanitised and displayed in a sandboxed frame (SR-6). PDFs are shown in the browser's built-in viewer inside a frame on the app page, served by a separate endpoint only when the file's bytes begin with `%PDF-` (SR-10). Archives (zip, tar, tar.gz) show their table of contents only (name and size); nothing is extracted or decompressed for display beyond reading headers, the format is recognised by bytes rather than name, member names are displayed as text with control characters replaced and are never used as paths, and listing is bounded (entries shown, entries scanned, decompressed bytes streamed). SVG and HTML are never rendered inline.
- FR-10a. Hover peek. Hovering a readable file for about 400 ms shows a small bubble with the first ~20 lines, in the manner of a Wikipedia link preview. It shows plain text only; binary files, directories and empty files get no bubble. It never reads a file that is not stored locally (see SR-9), goes through the same guard and deny rules as everything else, cancels on mouse-out, scroll or key press, caches results, and limits concurrent requests. The keyboard equivalent is selecting the row, which shows the preview pane (FR-10). A "Hover previews" toggle turns it off; it is remembered per browser. Because hovering exposes contents more readily than clicking, `.env`, `.env.*`, `*.pem` and `*.key` are core deny rules (section 8.3), so those files never produce a bubble.
- FR-11. Keyboard-first navigation (arrow keys, enter, backspace, `/` to filter, `g`-style jumps).
- FR-12. Built-in filename search within the current subtree, running through the chokepoint so hide and deny rules are honored.
- FR-12a. "Copy path" button on entries and in the breadcrumb.
- FR-13. Metadata pane: xattrs, symlink target, quarantine flag, UTI/kind, checksums on demand. Extended attributes are read through the already-opened, already-verified file descriptor, are listed with their values (each capped at 4 KB; larger ones show name and size only), and are shown as text when they are valid UTF-8 and as hex otherwise. macOS and Linux; other platforms show none.
- FR-13a. Optional extended-attributes column, hidden by default and placed last. It shows attribute names only (for example `com.apple.quarantine, com.apple.provenance`), fetched lazily for the rows currently on screen and cached; it is never part of the streamed listing, so it cannot slow large folders. Full values live in the metadata pane.
- FR-2b (walk stage). Columns can be shown or hidden from a "Columns" menu; Name is always shown. Visibility is stored with the rest of the column layout (FR-2a).
- FR-12b. Filename search results stream in as they are found, breadth-first so shallow matches come first, and are capped (1,000 results, 500,000 entries visited). Search never enters denied or hidden directories and does not follow symlinked directories.

### 7.3 Run stage

- FR-14. Full-text and fuzzy search. Delegates to `mdfind` (Spotlight) rather than building an index; all results are post-filtered through the chokepoint (deny and hide rules) before display. A built-in index is reconsidered only if `mdfind` proves inadequate.
- FR-15. Live updates via filesystem events (fsnotify / FSEvents).
- FR-16. Multi-pane and tabs; saved views.
- FR-17. Git status overlays for repositories.
- FR-18. "Reveal in Finder" / "Open in editor" actions, restricted to a fixed allowlist of actions with no command built from a path (excluded from v1; see section 13). A "copy path" button ships earlier, in the walk stage.

## 8. Exclusion model

### 8.1 Two tiers

| | `ignore` (hide) | `deny` (block) |
|---|---|---|
| Purpose | Reduce clutter | Never serve |
| Syntax | gitignore | gitignore |
| Effect on listings and search | Entries omitted | Entries omitted |
| Effect on direct access (stat, read, download, preview) | Allowed | **Blocked, HTTP 404** |
| Locations | Global (`~/.config/fsb/ignore`) plus optional per-directory `.fsbignore` | **Global only** (`~/.config/fsb/deny`) |
| `!` negation | Supported (gitignore semantics) | Not supported; deny is never overridable |
| Shipped defaults | Active (`.DS_Store`, `.git/`, `node_modules/`, etc.) | **Core** known-secret rules active; **optional** rules commented out |

Precedence: deny always wins over ignore. No ignore rule (including negation) can re-expose a denied path.

### 8.2 Enforcement requirements

- ER-1. All filesystem access goes through a single wrapper (the chokepoint). Handlers cannot call `os`/`io/fs` directly; this is enforced by package boundaries and a lint/test.
- ER-2. The wrapper canonicalizes every path before matching: `..` and `.` removal, duplicate slashes, NUL rejection, and NFC normalization. URL-decoding happens exactly once, in the HTTP layer; the guard never decodes, so a literal `%2e%2e` is just a file name. Trailing dots are not stripped: on macOS they are ordinary characters.
- ER-3. Symlinks are resolved and rules are evaluated against **both** the requested path and the resolved real path; a match on either denies access. A resolved real path outside every configured root is also denied.
- ER-4. Matching is case-insensitive to match APFS defaults, with a documented note for case-sensitive volumes.
- ER-5. Check-then-open races are mitigated by opening first and verifying the opened file's real path (`fstat`/`F_GETPATH`) against the rules before serving any bytes.
- ER-6. Denied and nonexistent paths are indistinguishable to the client (identical 404 status and body) unless `--debug`. Response timing is not equalized: a lexical deny match returns before the filesystem is touched, so it is measurably faster than a miss. That is accepted for a loopback-only, token-protected server.
- ER-7. Derived data (search index, thumbnails, caches) must honor deny at build time and at query time.
- ER-8. Rule files are read at startup (live reload is deferred to the run stage), with parse errors reported clearly and the server failing closed (refusing to start) on a malformed deny or ignore file. If the deny file is missing, the built-in core rules apply. If it exists it is authoritative: a core rule the user removes or comments out is no longer enforced, and FR-9a warns about it.

- ER-9. Path aliases are reduced to one canonical form before any comparison with a root or a rule. On macOS the data volume's firmlinks make `/System/Volumes/Data/Users/me` the same directory as `/Users/me`; both forms, in any letter case, are treated as `/Users/me`. The directory that rules are built from (the home directory) is resolved the same way, so a symlinked `$HOME` cannot leave rules keyed to a path the guard never compares.
- ER-10. Children of a listed or searched directory are judged by the directory's real location, not by the symlink or alias it was reached through.
- ER-11. A permission error on a path that lies inside a denied place is reported as a plain not-found, decided from the nearest ancestor that can be resolved, so an unreadable file in a denied folder cannot be told from a missing one. Permission errors elsewhere stay visible to the user.
- ER-12. Rule matching ignores trailing whitespace in a name (macOS allows `id.pem ` and `.env<TAB>`), and `**` matches names containing newlines.

### 8.3 Default deny rules: core (active) and optional (commented out)

The **core** set covers locations that hold credentials or secrets and are known security issues if exposed. Core rules are **active by default** and are compiled into the binary as a built-in baseline, so they apply even if the deny file is missing (fail closed). `fsb --init` writes them, uncommented, into the deny file so the user can see them. The **optional** set covers potential exclusions that are more situational or prone to false positives; these ship commented out for the user to enable.

Generated by `fsb --init` and documented in the README. The credential rules are not anchored to the home directory: the same folders in a backup or clone of the home directory, in another account's home, or under a wrong `$HOME` are just as sensitive, so they match at any depth like the secret-file patterns:

```
# fsb deny rules: paths matching these are never served (HTTP 404).
# Global config only; gitignore syntax. Deny always wins over ignore.

# --- Core (active by default: known credential/secret locations and files) ---
# Removing a core rule makes fsb warn at startup and in the UI.
.ssh/
.aws/
.gnupg/
**/.config/gh/
.netrc
.kube/
**/Library/Keychains/
**/Library/Application Support/Google/Chrome/
**/Library/Application Support/Firefox/
**/Library/Safari/
**/Library/Cookies/
.env
.env.*
*.pem
*.key

# --- Optional (commented out: uncomment to enable) ---
# ~/.docker/config.json
# ~/.npmrc
# ~/Library/Mail/
# ~/Library/Messages/
```

The exact core list is versioned, so additions in later releases are called out in release notes. A user who already has a deny file keeps it as written (it is authoritative), so a newly added core rule that is missing from it triggers the FR-9a warning naming that rule.

**Secret-file patterns (added 2026-09-20).** `.env`, `.env.*`, `*.pem` and `*.key` became core rules because hover peek (FR-10a) exposes file contents more readily than a click. They match at any depth and are deliberately broad, which has known costs: `*.key` also matches Keynote presentations; `.env` also matches a directory named `.env` (for example a Python virtualenv) and everything in it; and `.env.*` also matches committed templates such as `.env.example`. A user who needs any of these edits their deny file and accepts the startup warning.

## 9. Security requirements

- SR-1. Bind to `127.0.0.1` only. No configuration option exposes another interface.
- SR-2. Validate the `Host` header against `127.0.0.1:PORT` / `localhost:PORT`; reject anything else (DNS rebinding defense).
- SR-3. Reject requests with an `Origin` or `Referer` from a different origin; no permissive CORS.
- SR-4. Random per-launch token (at least 128 bits), delivered in the launch URL and then stored in an `HttpOnly`, `SameSite=Strict` cookie; API requests without it are rejected. Because browsers scope cookies by host and never by port, everything is served under a random per-launch path prefix (at least 128 bits) and the cookie's `Path` is that prefix, so the cookie is never sent to other web servers on the same address; URLs outside the prefix are refused like unauthenticated requests.
- SR-5. Read-only by construction: only `GET`/`HEAD`; no write code in the binary.
- SR-6. Serve user files with `X-Content-Type-Options: nosniff`, a restrictive CSP on the app shell, and rendered/untrusted content (HTML, SVG, Markdown) sandboxed so it cannot reach the API with the user's token.
- SR-7. No telemetry, no outbound network calls, no auto-update.
- SR-8. Logs must not contain the token.
- SR-9. fsb never triggers a download of a cloud-only ("dataless") file. Hover peek, head, and preview endpoints check the file's dataless flag first and report the file as not downloaded instead of reading it. Explicit downloads through `/api/file` are unaffected.
- SR-10. Inline (non-attachment) responses are limited to raster images whose type is confirmed by their leading bytes (PNG, JPEG, GIF, WebP), and carry `Content-Security-Policy: sandbox; default-src 'none'` and `nosniff`. File extensions are never trusted. SVG, HTML and every other type are served only as downloads. The one further exception is PDF: `/api/pdf` serves a file inline as `application/pdf` only if it begins with `%PDF-`, with `nosniff`, `X-Frame-Options: SAMEORIGIN` and `Content-Security-Policy: frame-ancestors 'self'; script-src 'none'`. It cannot carry the `sandbox` CSP that images get, because browsers then refuse to display PDFs; the viewer is the browser's own isolated component, and the frame is never given access to the app.

## 10. Non-functional requirements

- NFR-1. Listing a 100k-entry directory renders the first screen in under 500 ms on a recent Mac.
- NFR-2. Idle memory under 50 MB; no persistent index in the crawl stage.
- NFR-3. Cold start to browser open under 1 second.
- NFR-4. Single binary under 20 MB.
- NFR-5. Dependencies kept minimal and reviewed; `go.sum` pinned, `govulncheck` in CI.
- NFR-6. Accessibility: keyboard operable, semantic markup, respects `prefers-color-scheme`.

## 11. Distribution

1. Public GitHub repo, MIT license.
2. `go install` and GitHub Releases built with GoReleaser (darwin arm64 + amd64), with checksums, and signed/notarized if practical.
3. Homebrew tap after the walk stage.
4. Optional later: menu-bar or webview wrapper hosting the same UI.

## 12. Testing strategy

- The matcher and chokepoint get the heaviest testing: table-driven tests plus fuzzing for path canonicalization.
- Explicit test cases: symlink escapes, `..` and encoded traversal, case variants, negation attempts against deny, TOCTOU swaps, hard-link caveats (documented, not defended), Unicode normalization (NFC vs NFD).
- Integration tests verify that every endpoint returns 404 for denied paths, including search and preview endpoints.
- HTTP-level tests for Host/Origin/token enforcement, including simulated DNS rebinding.
- A CI check that no handler imports `os` or `io/fs` directly.

## 13. Risks and open questions

**Risks**

- **R1. Sensitive paths exposed by default.** The root is the whole home directory, so without protection `~/.ssh` and similar would be reachable. Mitigations (accepted): core known-secret deny rules are active by default and built in (8.3); a startup notice and persistent UI banner appear if the user weakens them (FR-9a). Residual risk: secrets outside the core list (for example `~/.docker/config.json` or `~/.npmrc`, which are optional rules, or any other secret with an unremarkable name) remain browsable, and a user who deliberately disables core rules and ignores the warning stays exposed.
- R2. Hard links and bind-style mounts can bypass path-based rules (accepted and documented).
- R3. Rendering untrusted content (HTML/SVG/Markdown) in previews is an XSS surface against a token-holding page (mitigated by SR-6).
- R4. macOS permission prompts (TCC) for Documents, Desktop, Downloads, and external volumes may surprise users; the docs need to explain this.

**Open questions**

None currently.

**Resolved (2026-09-20)**

- Name: `fsb`.
- "Reveal in Finder" / "Open in editor": excluded from v1 because they make the server launch local processes. If added in the run stage, use a fixed allowlist of actions and never build a command from a path. A "copy path" button covers the interim need.
- Search: filename search is built in (walk stage) on top of the chokepoint. Content search, if added, shells out to `mdfind`, and any output from an external search tool is post-filtered through the chokepoint before display, since those tools do not know about deny rules.
- License: MIT.
- Multiple roots: see section 6 (`--root`, repeatable).
- Frontend: Svelte with Vite.

## 14. Milestones

| Milestone | Scope | Exit criteria |
|---|---|---|
| M0: Skeleton | Repo, CI, chokepoint wrapper, matcher, token/Host/Origin middleware | Security test suite green. **Done 2026-09-20** (tests pass under `-race`; CI workflow written but not yet run on GitHub) |
| M1: Crawl | FR-1 to FR-9a | Usable daily by the author on `$HOME`. **Built 2026-09-20**; measured on a 100,000-file directory: first rows painted in about 450 ms, full listing in 1.5 s, 31 DOM rows rendered. "Usable daily" is still to be confirmed by real use. |
| M2: Walk | FR-10 to FR-13a, FR-2b, FR-12b. **First cut built 2026-09-20**: preview pane (images, highlighted code and Markdown source, JSON, CSV, text), hover peek, keyboard navigation, streaming filename search, copy path, extended attributes (pane and optional lazy column), show/hide columns. Rendered Markdown, PDF preview and archive listings added afterwards. Verified in the browser pane and by unit and integration tests; not yet run against real iCloud placeholder files or in Safari/Firefox. | Previews and search working, no deny bypasses in tests |
| M3: Public release | Docs, releases, Homebrew tap, security-model write-up | Tagged v0.1.0 |
| M4: Run | FR-14 to FR-18 as prioritized | Reassess after real use |
