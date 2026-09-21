# Security

fsb serves your own files to your own browser, so its job is to make sure that
*only* your browser session can reach them, that it never writes, and that the
secrets you have excluded stay excluded.

## Threat model

**Defended against**

- **A malicious web page in your browser** trying to read your files through fsb (cross-site requests, DNS rebinding, framing, cross-origin image or script loads).
- **Another user or process on the machine** connecting to the port: it has no launch token and no session cookie.
- **A path that names a denied file in a different way**: `..` traversal, symlinks (including symlinks to directories that contain denied entries), case variants, Unicode variants, macOS firmlink aliases (`/System/Volumes/Data/...`), a symlinked home directory, awkward file names (trailing whitespace, newlines).
- **Untrusted file content**: nothing from a file is rendered as HTML; inline responses are limited to sniffed PNG/JPEG/GIF/WebP served in a sandbox; a cloud-only file is never read by hover, preview or metadata.

**Not defended against**

- **Anything already running as you.** Malware with your privileges can read the same files without fsb. fsb is not a sandbox.
- **Someone with your unlocked machine, or root.**
- **Secrets that are not covered by a deny rule.** fsb can only hide what its rules name; see `~/.config/fsb/deny`.

## What is guaranteed, and how

| Guarantee | Enforced by |
|---|---|
| Reachable only on `127.0.0.1` by your session | Fixed loopback bind; exact `Host` allowlist; strict `Origin`/`Referer`/`Sec-Fetch-Site` checks; a single-use launch token exchanged for an `HttpOnly`, `SameSite=Strict` cookie whose `Path` is a random per-launch prefix under which everything is served (browsers cannot scope a cookie by port, so this is what keeps it from other local servers); `Cross-Origin-Resource-Policy`, `X-Frame-Options` and CSP headers |
| Read-only | Only `GET`/`HEAD` are served; the binary contains no write code |
| Deny rules block every access | One chokepoint (`internal/guard`) used by list, download, preview, head, metadata and search; rules are matched against the *real* location, after symlinks and aliases are resolved, and again on the file descriptor that was actually opened |
| A denied path looks like a missing one | Identical 404 status and body, including for unreadable files inside denied folders |
| Rendering a Markdown file cannot run code, load anything remote or reach the app | Three layers, each assuming the one before failed: raw HTML is escaped and links carry no `href` (`web/src/lib/markdown.ts`); the result is sanitized with DOMPurify; and it is shown in an iframe with `sandbox="allow-scripts"` (an opaque origin, so no session and no API access) whose own policy is `default-src 'none'` with only its one fixed script and style admitted by hash, and images limited to `data:`. Local images are fetched through the guarded preview endpoint, and remote images are replaced by a placeholder, so opening a file makes no outside request |
| A PDF preview is served only for real PDFs and cannot be framed by other sites | `/api/pdf` requires the `%PDF-` signature (never the name), is subject to the same guard and deny rules, refuses cloud-only files, and is sent with `nosniff`, `X-Frame-Options: SAMEORIGIN` and `frame-ancestors 'self'`. A CSP `sandbox` is not used here because browsers then refuse to display PDFs; rendering is left to the browser's own viewer |
| Listing an archive cannot extract, write or exhaust memory | `/api/archive` returns names and sizes only, never member data, through the same guard and deny rules. Format is decided from bytes; a zip claiming more than 200,000 entries is refused; tar scanning stops after 200,000 entries or 512 MiB of decompressed stream; member names are shown as text with control characters replaced |
| Only the configured roots are reachable | Real-path check against the roots; symlinks that leave them are refused |

Details are in [PRD.md](PRD.md), sections 8 and 9.

## Known limitations

- **Hard links.** Rules are path-based, so a file with several names is judged by identity: if any of its names is denied (a `.env` or `.pem` anywhere in the roots, or anything in a credential folder of any home), all of them are refused and unlisted. The index of multi-name files is built in the background at startup (about 20 seconds for a 700,000-file home) and refreshed when a new multi-name file appears; until then such files are refused rather than guessed. A hard link whose denied name lies outside every root and every credential folder is not detected, and a walk stopped by its size limit (20 million entries) leaves later links unseen.
- **Broad patterns have costs.** `*.key` also matches Keynote files, `.env` also matches a folder called `.env`, and `.env.*` also matches `.env.example`. You can remove a pattern from your deny file; fsb then warns at startup and in the UI.
- **`--root /` widens what is exposed.** Deny rules still apply, but only to what they name.
- **Explicit downloads of cloud-only files.** Clicking a file name downloads it, and for an iCloud file that is not stored locally that starts the download. Hover, preview and metadata never do.
- **Rules are read at startup.** Restart to apply changes.
- **Response timing is not equalized.** A denied path can answer slightly faster than a missing one; the status and body are identical.

## How it is tested

- **Unit and integration tests** for the rule matcher, the guard, the HTTP layer and the API, including deliberately hostile paths.
- **Fuzzing** of the path guard, the rule matcher, the HTTP middleware and the whole server stack. The guard fuzzer uses an oracle that does not trust fsb's own rules: whatever it returns must not be the same inode as a known secret or contain a secret marker. A fuzzer only reaches what its test fixture contains, and an early version missed a bug for that reason; keep the fixture broad.
- **Real-browser tests** in Chrome and Safari, including attacks from a second origin and a DNS-rebinding hostname. See [e2e/README.md](../e2e/README.md).
- **Mutation checks**: removing a deny rule (or reverting a fix) must make these tests fail.
- **An independent review** by a reviewer with no access to the author's reasoning or tests. It found real problems, all fixed and covered by regression tests: listings and search through a symlinked directory leaked denied names; a newline or trailing space in a file name defeated some rules; an unreadable file inside a denied folder answered 403 instead of 404. Earlier, a macOS firmlink alias was found to bypass deny rules under `--root /`. While writing the algebraic specification (see [`algebra/`](algebra/)), a further bypass was found: APFS compares names with *full* Unicode case folding, so `~/.ßh` opens `~/.ssh` (also `ſ`, the Kelvin sign and ligatures such as `ﬁ`), which the rule matcher's simple folding did not treat as the same name. Rules are now compared after the same folding as APFS, verified against this file system. A rule file could also load without error and do nothing (a byte order mark, old-Mac line endings, an inline comment, `~user/`, quotes, invisible or slash-lookalike characters); the first two are now handled and the rest stop fsb from starting with a message that says what is wrong. A follow-up audit of every name comparison found two more: the firmlink prefix (`/System/Volumes/Data`) was matched by byte length, so an aliased spelling dodged reduction under `--root /`; and root containment compared spellings, which on a case-sensitive volume admitted a sibling folder differing only in case. Containment is now decided by file identity. A differential test (`guard/differential_test.go`) then had the operating system itself decide which of thousands of generated spellings of each secret really reach it (case, folding aliases, decomposed accents, separators, dot segments, firmlink and symlink aliases, named forks); every one is refused, and the test fails about 1,500 times if the folding fix is reverted. The same audit found that the credential folders were only protected under the current `$HOME`; they are now matched at any depth, so a Time Machine copy or a clone of the home folder, another account's home, or a wrong `$HOME` do not expose them.
- **Dependency scanning**: `govulncheck` and `npm audit` (in CI), plus Dependabot.

None of this is a guarantee. It is a record of what has been checked. Before wider distribution, a further independent review is recommended.

## Reporting a vulnerability

The project is not yet public. When it is, please use GitHub's private vulnerability reporting for this repository rather than a public issue.
