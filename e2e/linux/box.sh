#!/bin/bash
# Starts a Linux container: a plain distribution, or one with fsb and its test
# files, or fsb served to this Mac's browser. Run it from anywhere; it finds the
# fsb repository from its own location (follows the ~/bin link).
#
#   box.sh                       ask which distribution and what to run
#   box.sh DISTRO [MODE] [-- CMD...]
#   box.sh --list                containers running now
#   box.sh --stop                stop every container this script started
#   box.sh --help
#
# DISTRO: debian ubuntu fedora alpine arch
# MODE:
#   shell   (default) a plain distribution, as root, the current folder mounted
#           at /work if it is under your home folder (Colima shares only that)
#   fsb     fsb built from the repository and installed, a test user "tester"
#           with test files in its home folder; a shell as tester
#   browse  fsb serving tester's home folder, opened in this Mac's browser
#           (e2e/linux/browse.sh; Control-C stops it); "-- PORT" picks the
#           port (default 8765)
# CMD (shell and fsb): run this instead of an interactive shell, e.g.
#   box.sh alpine -- cat /etc/os-release
#
# Containers are removed when you leave them (--rm). Needs Docker; with Colima,
# the VM is started if it is not running.
set -euo pipefail

self=$0
while [ -L "$self" ]; do
	link=$(readlink "$self")
	case $link in /*) self=$link ;; *) self=$(dirname "$self")/$link ;; esac
done
repo=$(cd "$(dirname "$self")/../.." && pwd)

distros=(debian ubuntu fedora alpine arch)
image_of() {
	case $1 in
	debian) echo debian:trixie ;;
	ubuntu) echo ubuntu:24.04 ;;
	fedora) echo fedora:latest ;;
	alpine) echo alpine:latest ;;
	arch) echo archlinux ;;
	*) return 1 ;;
	esac
}
platform_of() { [ "$1" = arch ] && echo linux/amd64 || echo linux/arm64; }
shell_of() { [ "$1" = alpine ] && echo sh || echo bash; }

usage() { sed -n '2,/^set -euo/p' "$self" | sed '$d' | sed 's/^# \{0,1\}//' | sed "s/box\.sh/$(basename "$0")/g"; }

ensure_docker() {
	if docker info >/dev/null 2>&1; then return; fi
	if command -v colima >/dev/null; then
		echo "box: starting Colima (Docker's virtual machine)..." >&2
		colima start >&2
	else
		echo "box: Docker is not running" >&2
		exit 1
	fi
}

case ${1:-} in
-h | --help)
	usage
	exit 0
	;;
--list)
	docker ps --filter label=fsb.box --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}'
	exit 0
	;;
--stop)
	ids=$(docker ps -q --filter label=fsb.box)
	[ -n "$ids" ] && docker stop $ids >/dev/null
	echo "box: stopped $(echo "$ids" | grep -c . || true) container(s)"
	exit 0
	;;
esac

distro=${1:-}
mode=${2:-}
[ "$mode" = -- ] && mode=
cmd=()
for ((i = 1; i <= $#; i++)); do
	if [ "${!i}" = -- ]; then
		cmd=("${@:i+1}")
		break
	fi
done

if [ -z "$distro" ]; then
	PS3="Distribution? "
	select distro in "${distros[@]}"; do [ -n "$distro" ] && break; done
	PS3="Run what? "
	select mode in "shell: plain $distro" "fsb: fsb and test files, as tester" "browse: fsb in this Mac's browser"; do
		[ -n "$mode" ] && break
	done
	mode=${mode%%:*}
fi
mode=${mode:-shell}
image=$(image_of "$distro") || { echo "box: unknown distribution '$distro' (one of: ${distros[*]})" >&2; exit 2; }
platform=$(platform_of "$distro")
[ "$platform" = linux/amd64 ] && [ "$(uname -m)" = arm64 ] &&
	echo "box: $distro has no arm64 image; it runs emulated (slow, and Go programs may crash under load)" >&2

ensure_docker
tty=(-i)
[ -t 0 ] && [ -t 1 ] && tty=(-it)
name=box-$distro-$$

case $mode in
shell)
	mount=()
	case $PWD/ in
	"$HOME"/*) mount=(-v "$PWD":/work -w /work) ;;
	*) echo "box: $PWD is outside your home folder, so it is not mounted" >&2 ;;
	esac
	[ ${#cmd[@]} -eq 0 ] && cmd=("$(shell_of "$distro")")
	exec docker run --rm "${tty[@]}" --init --label fsb.box --name "$name" \
		--platform "$platform" ${mount[@]+"${mount[@]}"} "$image" "${cmd[@]}"
	;;
fsb)
	echo "box: building fsb for $distro (a minute the first time)..." >&2
	docker build -q -f "$repo/e2e/linux/Dockerfile" --target smoke --platform "$platform" \
		--build-arg BASE="$image" --build-arg TESTER_UID="$(id -u)" \
		-t "fsb-smoke:$distro" "$repo" >/dev/null
	[ ${#cmd[@]} -eq 0 ] && cmd=(sh -l)
	echo "box: fsb is installed; try: fsb --no-open --port 8765  (open its URL in the Mac's browser)" >&2
	exec docker run --rm "${tty[@]}" --init --label fsb.box --name "$name" \
		--network host --tmpfs /tmp:rw,exec --platform "$platform" "fsb-smoke:$distro" "${cmd[@]}"
	;;
browse)
	exec "$repo/e2e/linux/browse.sh" "$distro" ${cmd[@]+"${cmd[@]}"}
	;;
*)
	echo "box: unknown mode '$mode' (shell, fsb or browse)" >&2
	exit 2
	;;
esac
