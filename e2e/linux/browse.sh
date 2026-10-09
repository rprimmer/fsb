#!/bin/sh
# Runs fsb in a Linux container, serving the test user's home folder, and opens
# it in the Mac's browser. Stop it with Control-C.
#
#   e2e/linux/browse.sh [debian|ubuntu|fedora|alpine|arch] [port]
#
# fsb listens on 127.0.0.1 only, and its interface cannot be changed. With
# --network host the container shares the Docker VM's network, so fsb's
# 127.0.0.1 is the VM's, and Colima forwards the VM's 127.0.0.1 ports to the
# Mac's 127.0.0.1 (not to the network). The port is the same on both sides,
# which fsb's Host check requires.
set -eu
cd "$(dirname "$0")/../.."

name=${1:-debian}
port=${2:-8765}
case $name in
debian) image=debian:trixie platform=linux/arm64 ;;
ubuntu) image=ubuntu:24.04 platform=linux/arm64 ;;
fedora) image=fedora:latest platform=linux/arm64 ;;
alpine) image=alpine:latest platform=linux/arm64 ;;
arch) image=archlinux platform=linux/amd64 ;;
*)
	echo "browse.sh: unknown distribution $name" >&2
	exit 2
	;;
esac
if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
	echo "browse.sh: port $port is in use on this Mac; pass another" >&2
	exit 1
fi

# The test user gets this Mac user's uid: Colima's port forward connects as
# that uid, and fsb accepts its launch URL only from its own user.
docker build -q -f e2e/linux/Dockerfile --target smoke --platform "$platform" \
	--build-arg BASE="$image" --build-arg TESTER_UID="$(id -u)" -t "fsb-smoke:$name" . >/dev/null
container=fsb-browse-$name
docker rm -f "$container" >/dev/null 2>&1 || true
docker run -d --rm --init --network host --platform "$platform" --name "$container" \
	"fsb-smoke:$name" fsb --no-open --port "$port" >/dev/null
trap 'docker stop "$container" >/dev/null 2>&1' EXIT INT TERM

url=
i=0
while [ -z "$url" ] && [ $i -lt 30 ]; do
	sleep 1
	url=$(docker logs "$container" 2>&1 | grep -o "http://127.0.0.1:$port/[^ ]*" || true)
	i=$((i + 1))
done
if [ -z "$url" ]; then
	echo "browse.sh: fsb did not start:" >&2
	docker logs "$container" >&2
	exit 1
fi
# Colima notices the new port within a second or two.
i=0
until curl -s -o /dev/null "http://127.0.0.1:$port/" || [ $i -ge 10 ]; do
	sleep 1
	i=$((i + 1))
done
docker logs "$container" 2>&1 | head -1
echo "Opening $url"
open "$url"
echo "Serving from $name; Control-C stops it."
docker logs -f "$container" >/dev/null 2>&1 || true
