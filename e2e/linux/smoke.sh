#!/bin/sh
# Smoke test of fsb inside a Linux container, run as the user "tester" against
# the fixture that setup.sh made. POSIX sh (BusyBox ash on Alpine).
#
# Prints one line per check:
#   PASS / FAIL   behavior fsb must have; any FAIL makes the exit status 1
#   PROBE         Linux-specific behavior under investigation (Phase 3 of the
#                 plan); reported, never failed, until we decide what is right
set -u

PORT=8765
H=$HOME
fails=0
pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }
check() { # check NAME EXPECTED ACTUAL
	if [ "$2" = "$3" ]; then pass "$1"; else fail "$1 (expected $2, got $3)"; fi
}
probe() { echo "PROBE $1"; }

. /etc/os-release
echo "== $PRETTY_NAME, $(uname -m), $(id -un)"

v=$(fsb --version)
echo "      $v"
case $v in
*"commit unknown"* | *modified*) fail "--version names a clean commit" ;;
*commit*) pass "--version names a clean commit" ;;
*) fail "--version names a clean commit" ;;
esac

out=$(mktemp)
fsb --no-open --port $PORT >"$out" 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null' EXIT
# Emulated (amd64) runs can be slow to start.
url=
i=0
while [ $i -lt 30 ] && [ -z "$url" ]; do
	sleep 1
	url=$(grep -o "http://127.0.0.1:$PORT/[A-Za-z0-9_-]*/?token=[A-Za-z0-9_-]*" "$out" || true)
	i=$((i + 1))
done
if [ -z "$url" ]; then
	fail "fsb starts and prints its launch URL"
	cat "$out"
	exit 1
fi
pass "fsb starts and prints its launch URL"
base=${url%%\?token=*}
jar=$(mktemp)
body=$(mktemp)

# get PATH-PARAMETER ENDPOINT: prints the HTTP status; the body goes to $body.
get() {
	curl -s -b "$jar" -o "$body" -w '%{http_code}' -G --data-urlencode "path=$1" "${base}api/$2"
}

check "launch URL sets the session (303)" 303 "$(curl -s -c "$jar" -o /dev/null -w '%{http_code}' "$url")"
check "launch URL works only once (403)" 403 "$(curl -s -o /dev/null -w '%{http_code}' "$url")"
check "wrong Host header is refused (403)" 403 \
	"$(curl -s -b "$jar" -o /dev/null -w '%{http_code}' -H "Host: evil.example:$PORT" "${base}api/status")"
check "api/status" 200 "$(curl -s -b "$jar" -o /dev/null -w '%{http_code}' "${base}api/status")"

check "list home" 200 "$(get "$H" list)"
if grep -q '"docs"' "$body"; then pass "listing shows docs"; else fail "listing shows docs"; fi
if grep -q '"\.ssh"' "$body"; then fail "listing hides denied .ssh"; else pass "listing hides denied .ssh"; fi

check "read docs/readme.txt" 200 "$(get "$H/docs/readme.txt" head)"
grep -q "hello from" "$body" || fail "readme.txt content"
check "Readme.TXT is a separate file (case-sensitive)" 200 "$(get "$H/docs/Readme.TXT" head)"
if grep -q upper "$body"; then pass "Readme.TXT content is its own"; else fail "Readme.TXT content is its own"; fi

check "core deny: .ssh/id_ed25519 (404)" 404 "$(get "$H/.ssh/id_ed25519" head)"
check "core deny: .aws/credentials (404)" 404 "$(get "$H/.aws/credentials" head)"
check "core deny: download .ssh/id_ed25519 (404)" 404 "$(get "$H/.ssh/id_ed25519" file)"
check "symlink out of the root (404)" 404 "$(get "$H/escape/passwd" head)"

for p in .config/google-chrome/Default/Cookies ".config/chromium/Default/Login Data" \
	.mozilla/firefox/abc.default/logins.json .local/share/keyrings/login.keyring \
	.password-store/email.gpg; do
	probe "Linux secret ~/$p: $(get "$H/$p" head)"
done
probe "unreadable docs/locked.txt: $(get "$H/docs/locked.txt" head)"
get "$H" list >/dev/null
probe "non-UTF-8 name in listing: $(grep -o '"caf[^"]*"' "$body" | head -1)"
probe "non-UTF-8 name read by its real bytes: $(get "$H/$(printf 'caf\351.txt')" head)"

kill $pid 2>/dev/null
wait $pid 2>/dev/null

# --background, --status and --stop find fsb with ps and lsof, which minimal
# systems may lack (Phase 3).
have=
for c in ps lsof; do
	if command -v $c >/dev/null; then have="$have $c=$(basename "$(readlink -f "$(command -v $c)")")"; else have="$have $c=missing"; fi
done
probe "tools:$have"
bg=$(fsb --background --no-open --port $((PORT + 1)) 2>&1 | tail -1)
probe "--background: $bg"
probe "--status: $(fsb --status 2>&1 | head -1)"
probe "--stop: $(fsb --stop 2>&1 | head -1)"
if curl -s -o /dev/null "http://127.0.0.1:$((PORT + 1))/"; then
	probe "after --stop: still serving" # the container's exit ends it
else
	probe "after --stop: stopped"
fi
echo "== $fails failure(s)"
[ $fails -eq 0 ]
