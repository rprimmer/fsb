#!/bin/sh
# Starts Docker for the Linux tests when it is not running (Colima, on a Mac),
# and stops it again once it has been idle for a while, but only if this script
# started it: a Colima you started yourself is left alone.
#
#   e2e/linux/docker.sh up        before a run: start Colima if Docker is not
#                                 reachable, and cancel a pending stop
#   e2e/linux/docker.sh release   after a run: stop Colima FSB_COLIMA_IDLE seconds
#                                 later (default 1200); while a test run or a
#                                 container is still going, it waits again
#
# The wait starts when a run ends, not when Colima starts, so a long run is
# never cut short.
set -u
state=${TMPDIR:-/tmp}/fsb-colima
idle=${FSB_COLIMA_IDLE:-1200}

cancel_timer() {
	if [ -f "$state/timer" ]; then
		kill "$(cat "$state/timer")" 2>/dev/null
		rm -f "$state/timer"
	fi
}

case ${1:-} in
up)
	cancel_timer
	docker info >/dev/null 2>&1 && exit 0
	# Without Colima, run.sh says that Docker is not running.
	command -v colima >/dev/null 2>&1 || exit 0
	echo "docker.sh: starting Colima; it stops again after $(((idle + 59) / 60)) idle minutes"
	colima start || exit 1
	mkdir -p "$state"
	: >"$state/started"
	;;
release)
	[ -f "$state/started" ] || exit 0
	cancel_timer
	# shellcheck disable=SC2016 # expanded by the inner shell, from its arguments
	nohup sh -c '
		sleep "$1"
		# Still in use (a test run, or any container): wait another idle period.
		# Only a shell running a script counts, not any command line naming one.
		while pgrep -f "^(/bin/)?sh [^ ]*e2e/linux/(run|browse)\.sh" >/dev/null ||
			[ -n "$(docker ps -q 2>/dev/null)" ]; do
			sleep "$1"
		done
		rm -f "$2/started" "$2/timer"
		colima stop
	' sh "$idle" "$state" >"$state/stop.log" 2>&1 &
	echo $! >"$state/timer"
	echo "docker.sh: Colima stops in $(((idle + 59) / 60)) minutes if nothing else uses it"
	;;
*)
	echo 'usage: e2e/linux/docker.sh up|release' >&2
	exit 2
	;;
esac
