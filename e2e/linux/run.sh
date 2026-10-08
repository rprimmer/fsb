#!/bin/sh
# Builds fsb and runs its smoke test (smoke.sh) on several Linux distributions,
# then the Go unit tests on Debian and Alpine. Needs a running Docker engine
# (on a Mac, e.g. "colima start").
#
#   e2e/linux/run.sh             everything
#   e2e/linux/run.sh debian      one or more distributions by name
#   e2e/linux/run.sh unit        only the unit tests
set -u
cd "$(dirname "$0")/../.."

# name image platform
DISTROS='
debian  debian:trixie   linux/arm64
ubuntu  ubuntu:24.04    linux/arm64
fedora  fedora:latest   linux/arm64
alpine  alpine:latest   linux/arm64
arch    archlinux       linux/amd64
'

selected() { # selected NAME ARGS...: true if ARGS is empty or names NAME
	n=$1
	shift
	[ $# -eq 0 ] && return 0
	for a in "$@"; do [ "$a" = "$n" ] && return 0; done
	return 1
}

# Each run's full output is kept here (git-ignored), for failures that are
# hard to reproduce.
logs=e2e/artifacts/linux
mkdir -p "$logs"

summary=
status=0
record() { # record NAME RESULT
	summary="$summary$(printf '%-14s %s' "$1" "$2")
"
	[ "$2" = ok ] || status=1
}

for row in $(echo "$DISTROS" | awk 'NF==3 {print $1 "|" $2 "|" $3}'); do
	name=${row%%|*}
	rest=${row#*|}
	image=${rest%%|*}
	platform=${rest#*|}
	selected "$name" "$@" || continue
	echo
	echo "#### $name ($image, $platform)"
	if ! docker build -q -f e2e/linux/Dockerfile --target smoke --platform "$platform" \
		--build-arg BASE="$image" -t "fsb-smoke:$name" . >/dev/null; then
		record "$name" "FAILED (build)"
		continue
	fi
	docker run --rm --init --platform "$platform" "fsb-smoke:$name" >"$logs/$name.log" 2>&1
	rc=$?
	cat "$logs/$name.log"
	if [ $rc -eq 0 ]; then record "$name" ok; else record "$name" FAILED; fi
done

for flavor in debian alpine; do
	selected unit "$@" || selected "unit-$flavor" "$@" || continue
	suffix=
	[ $flavor = alpine ] && suffix=-alpine
	echo
	echo "#### unit tests on $flavor"
	if docker build -q -f e2e/linux/Dockerfile --target unit \
		--build-arg GO_FLAVOR="$suffix" -t "fsb-unit:$flavor" . >/dev/null &&
		docker run --rm --init "fsb-unit:$flavor"; then
		record "unit-$flavor" ok
	else
		record "unit-$flavor" FAILED
	fi
done

echo
echo "#### summary"
printf '%s' "$summary"
exit $status
