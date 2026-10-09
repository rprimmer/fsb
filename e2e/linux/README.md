# Linux distribution tests

Runs fsb in Docker containers for several Linux distributions. CI already tests on
Ubuntu (GitHub's runner); this covers the distributions and the minimal systems CI
doesn't.

```sh
make linux                    # or: e2e/linux/run.sh
e2e/linux/run.sh debian alpine
e2e/linux/run.sh unit         # only the Go unit tests (Debian, Alpine and Fedora)
```

Needs a running Docker engine. On a Mac, for example:

```sh
brew install colima docker docker-buildx
colima start --vm-type vz --vz-rosetta
```

## Browsing a containerized fsb

```sh
e2e/linux/browse.sh fedora        # any name from the table below; Control-C stops it
```

This opens your browser on fsb running inside the container, serving the test user's
home folder. fsb listens on 127.0.0.1 only, and that can't be configured, so publishing
a port (`-p`) can't reach it. Instead the container shares the Docker VM's network
(`--network host`), and Colima forwards the VM's 127.0.0.1 ports to the Mac's
127.0.0.1, never to the network. The port number stays the same on both sides, which
fsb's Host check requires.

## Browser tests against these distributions

`make linux-e2e` runs the Chrome browser suite on the Mac against `fsb` in each
native distribution; `FSB_E2E_LINUX=NAME` does the same for one, in Chrome or
Safari. See [`../README.md`](../README.md), "fsb on Linux".

## What runs

| Name | Image | Notes |
|---|---|---|
| debian | `debian:trixie` | |
| ubuntu | `ubuntu:24.04` | also stands in for Linux Mint 22, which is built on it |
| fedora | `fedora:latest` | |
| alpine | `alpine:latest` | musl libc and BusyBox instead of GNU tools |
| arch | `archlinux` | amd64 only, so it runs under emulation on Apple silicon |
| unit-debian, unit-alpine | `golang` images | `go test ./...` (with `-race` on Debian) as a normal user |
| unit-fedora | `fedora:latest` with the Go toolchain copied from the `golang` image | `go test -race ./...` as a normal user |

`Dockerfile` compiles fsb once per CPU architecture and copies it into each
distribution. `setup.sh` installs curl, creates the user `tester` and fills its home
directory with test files. `smoke.sh` runs inside the container as `tester`. It prints:

- **PASS / FAIL**: behavior fsb must have on every system. Any FAIL fails the run.
- **PROBE**: Linux-specific behavior that is still being decided, reported but never
  failed.

## Things to know

- Containers share the Docker VM's Linux kernel; distributions differ only in their
  userland (libc, tools, file layout). Kernels, real file systems and desktops need a VM.
- The `--version` check fails while the working tree has uncommitted changes, by
  design: the build is marked "modified".
- Under emulation (arch on Apple silicon), `/proc/PID/exe` names `qemu-x86_64` for
  every emulated process, so `--status` and `--stop` cannot recognize fsb there. This
  is an emulation artifact, not an Arch Linux problem.
- `docker run --init` is needed: without an init process to reap them, a stopped
  background fsb lingers as a zombie and `--stop` reports that it did not stop.
- Every rebuild adds to Docker's build cache, which is never trimmed on its own. After
  many runs it filled Colima's 40 GB disk, and an image build failed with "No space
  left on device". `docker system df` shows the usage; `docker builder prune -af`
  clears the cache (the next build just takes longer).
