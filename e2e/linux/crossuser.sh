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
got=$(code tester)
if [ "$got" = 303 ]; then echo "PASS the launch URL still works for fsb's own user (303)"; else echo "FAIL own user's launch got $got, want 303"; fail=1; fi
exit $fail
