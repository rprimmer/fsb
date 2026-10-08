# Linux distribution tests

Runs fsb in Docker containers for several Linux distributions. CI already tests on
Ubuntu (GitHub's runner); this covers the distributions and the minimal systems CI
doesn't.

```sh
make linux                    # or: e2e/linux/run.sh
e2e/linux/run.sh debian alpine
e2e/linux/run.sh unit         # only the Go unit tests (Debian and Alpine)
```

Needs a running Docker engine. On a Mac, for example:

```sh
brew install colima docker docker-buildx
colima start --vm-type vz --vz-rosetta
```

## What runs

| Name | Image | Notes |
|---|---|---|
| debian | `debian:trixie` | |
| ubuntu | `ubuntu:24.04` | also stands in for Linux Mint 22, which is built on it |
| fedora | `fedora:latest` | |
| alpine | `alpine:latest` | musl libc and BusyBox instead of GNU tools |
| arch | `archlinux` | amd64 only, so it runs under emulation on Apple silicon |
| unit-debian, unit-alpine | `golang` images | `go test ./...` (with `-race` on Debian) as a normal user |

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
