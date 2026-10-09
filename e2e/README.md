# Browser tests

End-to-end tests that drive a real browser against a real `fsb` with real
keyboard and mouse events. They cover what unit tests cannot: the interface,
the keyboard, drag-and-drop, hover, the preview pane, and, above all, that
denied and secret files stay invisible and unreachable from a browser.

No npm dependencies. Node 22+ (global `WebSocket` and `fetch`) and Go (to
build `fsb`, unless you set `FSB_BIN`).

```sh
node --test --test-reporter=spec e2e/chrome.test.mjs    # Chrome or Chromium
node --test --test-reporter=spec e2e/safari.test.mjs    # Safari (macOS)
FSB_E2E_BIG=1 node --test e2e/chrome.test.mjs           # also the 100,000-file test
```

| Variable | Meaning |
|---|---|
| `CHROME_BIN` | Chrome/Chromium executable, if it is not in a standard location |
| `FSB_BIN` | Use this `fsb` binary instead of building one |
| `FSB_E2E_BIG=1` | Also run the 100,000-file scale test (creates 100,000 files) |
| `FSB_E2E_SKIP_SAFARI=1` | Skip the Safari suite |
| `CI` | Adds `--no-sandbox` to Chrome |

Each run builds a throwaway fake home directory (`lib/fixture.mjs`), starts
`fsb` with `HOME` pointing at it (so the built-in deny and hide rules apply as
they do for a user with no config), and opens the app in a fresh browser
profile. Nothing outside a temporary directory is touched. Screenshots of
failed tests are written to `e2e/artifacts/`.

## How it is organized

- `suite.mjs` holds the tests, written once against a small driver interface.
- `lib/chrome.mjs` is a driver over the Chrome DevTools protocol.
- `lib/safari.mjs` is a driver over W3C WebDriver (`safaridriver`).
- The tests poll for state (`waitFor`) instead of sleeping, so they hold up on slow machines.
- Safari under WebDriver reports arrow keys with a modifier held as `key: "\u001c"` (a control character) while `code` stays correct. The app matches navigation keys by `code` (`web/src/lib/keys.ts`) for this reason.

## fsb on Linux

The same suites can drive an `fsb` that runs in a Linux container, with the
browser on the Mac. Set `FSB_E2E_LINUX` to a distribution of
[`linux/README.md`](linux/README.md) (`debian`, `ubuntu`, `fedora`, `alpine`,
`arch`); Docker must be running.

```sh
make linux-e2e                                                # Chrome, the four native distributions
FSB_E2E_LINUX=alpine node --test --test-reporter=spec e2e/safari.test.mjs
```

The fixture is built on the Mac and copied into the container at the same path
(on tmpfs, so extended attributes work), with one more file whose name is not
UTF-8, which macOS cannot hold; the test for it runs only here. `fsb` listens on
127.0.0.1 in the container, which Colima forwards to the Mac (`lib/container.mjs`).
Quick Look's test is skipped, since it is macOS only. `FSB_E2E_KEEP_CONTAINER=1`
leaves the container running afterwards, to inspect (`docker rm -f fsb-e2e-NAME`).

`arch` is amd64 and, on Apple silicon, runs under QEMU's emulation, where `fsb`,
like any Go program, crashes under the suite's load with garbage-collector errors;
an amd64 Debian does the same. Its results there say nothing about `fsb`.

## Safari

Safari's automation must be enabled once, by you: Safari > Settings > Advanced >
"Show features for web developers", then Developer > "Allow remote automation".
The first session may also ask you to authorize it (Touch ID or password).

**Quit Safari completely (Cmd-Q) before running the suite.** `safaridriver`
reuses a Safari that is already running, and Safari rejects it with "Safari was
not launched for automation" (visible in the system log). The only symptom is a
30-second "session timed out while connecting to a Safari instance". With Safari
quit, `safaridriver` launches its own automation instance and the suite runs in
an automation window; your normal windows and tabs are untouched afterwards
(Safari restores them next time you open it).

**Keep the Mac awake and unlocked while it runs.** Safari's mouse events (clicks,
drags, hovers) do not arrive while the display is asleep or the screen is
locked, so about a dozen tests time out waiting for a click to take effect,
while keyboard and API tests still pass. `caffeinate` does not help once the
screen has locked.

If it still times out with Safari quit, run `sudo safaridriver --enable` once
(it asks for an administrator password), look for an authorization prompt (a
system dialog, or a sheet in Safari), and to see what Safari says, run:

```sh
log show --last 5m --predicate 'process == "Safari" AND eventMessage CONTAINS[c] "automation"'
```

Known differences from Chrome, all in the driver and not the app:

- WebDriver cannot complete a native drop. Safari does start the drag (`dragstart` and `dragover` fire from the real gesture) but the drop never arrives, so the header drag-and-drop test then drives the app's drag handlers with synthetic events and says so in the output. A real mouse drag in Safari is worth trying by hand.
- WebDriver has no console access, so the "no script errors or CSP violations" test is skipped.
- The clipboard is not read back.

## Checking that the tests can fail

A passing suite proves little unless it fails on a broken app. To check, build a
copy of `fsb` with a rule removed from `CoreDeny` (in `internal/rules/defaults.go`)
and run the suite with `FSB_BIN` pointing at it: the security tests should fail
and the interface tests should still pass.
