# fsb

A read-only, local-only web view of your filesystem. One Go binary serves a browser UI on `127.0.0.1`; nothing is ever written, and nothing is reachable from the network.

> **Status: M1 (crawl).** Browse, sort, filter and download from a virtualized listing in your browser, on top of the security core (path guard, rule matcher, localhost protections). Previews, search and keyboard navigation come next. See [PRD/PRD.md](PRD/PRD.md).

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

**Core deny rules are on by default**, even with no deny file: `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config/gh`, `~/.netrc`, `~/.kube`, Keychains, and Chrome/Firefox/Safari data. Situational rules (`.env`, `*.pem`, ...) are in the generated deny file, commented out. If you remove a core rule from the file, fsb warns at startup.

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

Go:

```sh
go test -race ./...
go test ./internal/rules -run xxx -fuzz FuzzParseAndMatch -fuzztime 30s
```

## License

MIT
