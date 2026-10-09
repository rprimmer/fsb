#!/bin/sh
# Another user on the same machine reads fsb's launch URL (it is in the
# command line of the program that opens the browser) and uses it first. On
# Linux fsb accepts the launch URL only from a connection of its own user, so
# the other user must be refused, fsb must warn, and the URL must still work
# for its own user afterwards.
#
#   e2e/linux/crossuser.sh IMAGE PLATFORM   in a container of a smoke image
#                                           (the user "tester" runs fsb)
#   e2e/linux/crossuser.sh --host           on this Linux machine, as in CI:
#                                           the current user runs the fsb on
#                                           PATH; needs sudo for the other user
set -u
if [ "${1:-}" = --host ]; then
	me=$(id -un)
	as() {
		u=$1
		shift
		case $u in
		tester) "$@" ;;
		*) sudo -u "$u" "$@" ;;
		esac
	}
	as_root() { sudo "$@"; }
	# The other user's processes must be able to reach curl; nothing else.
	cd "$(mktemp -d)" || exit 1
	chmod 755 .
	work=$PWD
	inhome() { sh -c "cd '$work' && $1"; }
else
	image=$1
	platform=$2
	c=fsb-crossuser-$$
	docker run -d --rm --init --platform "$platform" --user root --name "$c" --entrypoint sleep "$image" infinity >/dev/null || exit 1
	trap 'docker rm -f "$c" >/dev/null 2>&1' EXIT
	as() { u=$1; shift; docker exec -u "$u" "$c" "$@"; }
	as_root() { docker exec -u root "$c" "$@"; }
	inhome() { as tester sh -c "cd && $1"; }
fi

if as_root sh -c 'command -v useradd' >/dev/null; then
	as_root useradd -m -s /bin/sh intruder
else
	as_root adduser -D -s /bin/sh intruder
fi
inhome 'fsb --no-open --port 8124 >fsb.out 2>fsb.err & echo $! >fsb.pid'
[ "${1:-}" = --host ] && trap 'kill "$(cat "$work/fsb.pid")" 2>/dev/null; sudo userdel -r intruder 2>/dev/null' EXIT
url=
for _ in $(seq 50); do
	url=$(inhome 'grep -o "http://127.0.0.1:8124/[^ ]*" fsb.out 2>/dev/null')
	[ -n "$url" ] && break
	sleep 0.2
done
fail=0
if [ -z "$url" ]; then
	echo "FAIL fsb did not print its URL"
	exit 1
fi
code() { as "$1" curl -s -o /dev/null -w '%{http_code}' "$url"; }
got=$(code intruder)
if [ "$got" = 403 ]; then echo "PASS another user's launch is refused (403)"; else echo "FAIL another user's launch got $got, want 403"; fail=1; fi
if inhome 'grep -q "refused the launch URL: it was opened by another user" fsb.err'; then
	echo "PASS fsb warned about it"
else
	echo "FAIL no warning; fsb said: $(inhome 'cat fsb.err')"
	fail=1
fi
case $url in
http://127.0.0.1:8124/\?token=*) echo "PASS the launch URL names no prefix" ;;
*) echo "FAIL the launch URL names more than the token: $url"; fail=1 ;;
esac
# fsb's own user launches; the redirect names the prefix, and the session
# cookie comes back with it.
r=$(as tester curl -s -D - -o /dev/null "$url")
got=$(printf '%s\n' "$r" | sed -n '1s/^HTTP[^ ]* \([0-9]*\).*/\1/p')
if [ "$got" = 303 ]; then echo "PASS the launch URL still works for fsb's own user (303)"; else echo "FAIL own user's launch got $got, want 303"; fail=1; fi
base=http://127.0.0.1:8124$(printf '%s\n' "$r" | tr -d '\r' | sed -n 's/^[Ll]ocation: //p')
cookie=$(printf '%s\n' "$r" | tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: \(fsb_session=[^;]*\).*/\1/p')
# The cookie, captured by a server of the other user (the browser sends it to
# any 127.0.0.1 server whose path starts with the prefix), and replayed.
status() { as "$1" curl -s -o /dev/null -w '%{http_code}' -H "Cookie: $cookie" "${base}api/status"; }
got=$(status intruder)
if [ "$got" = 403 ]; then echo "PASS another user replaying the session cookie is refused (403)"; else echo "FAIL another user's replayed cookie got $got, want 403"; fail=1; fi
got=$(status tester)
if [ "$got" = 200 ]; then echo "PASS the session works for fsb's own user (200)"; else echo "FAIL own user's session got $got, want 200"; fail=1; fi
if inhome 'grep -q "refused a request: a request with your session came from another user" fsb.err'; then
	echo "PASS fsb warned that the cookie may have been captured"
else
	echo "FAIL no warning about the cookie; fsb said: $(inhome 'cat fsb.err')"
	fail=1
fi
exit $fail
