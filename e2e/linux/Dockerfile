# syntax=docker/dockerfile:1
#
# Runs fsb on Linux distributions. Build from the repository root (run.sh does
# this for every distribution):
#
#   docker build -f e2e/linux/Dockerfile --target smoke \
#     --build-arg BASE=debian:trixie -t fsb-smoke:debian .
#   docker run --rm fsb-smoke:debian
#
# Stages: "build" compiles fsb once per CPU architecture; "smoke" copies it into
# the distribution named by BASE and runs smoke.sh as an ordinary user; "unit"
# runs the Go unit tests on Debian (glibc) or Alpine (musl).

ARG GO_VERSION=1.27.2
ARG BASE=debian:trixie
# GO_FLAVOR picks the unit stage's image: "" for Debian or "-alpine".
ARG GO_FLAVOR=""

# The build runs natively on the Mac's architecture and cross-compiles for the
# target one, so an amd64 image does not compile under emulation.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The checkout belongs to another user inside the container; without this, git
# refuses it and go build cannot stamp the commit that --version prints.
RUN git config --global --add safe.directory /src
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/fsb ./cmd/fsb

FROM ${BASE} AS smoke
# The test user's uid. The browser suites and browse.sh give it the uid of the
# user on the Mac: Colima forwards ports as that uid inside its VM, and fsb on
# Linux accepts its launch URL only from a connection of its own user.
ARG TESTER_UID=1000
COPY e2e/linux/setup.sh e2e/linux/smoke.sh /usr/local/lib/fsb-e2e/
RUN sh /usr/local/lib/fsb-e2e/setup.sh
COPY --from=build /out/fsb /usr/local/bin/fsb
USER tester
WORKDIR /home/tester
RUN sh /usr/local/lib/fsb-e2e/setup.sh fixture
CMD ["sh", "/usr/local/lib/fsb-e2e/smoke.sh"]

FROM golang:${GO_VERSION}${GO_FLAVOR} AS unit
# Debian's image has a C compiler for -race; Alpine's has none, and the race
# detector needs cgo, so the Alpine run goes without it.
RUN if command -v adduser >/dev/null && ! command -v useradd >/dev/null; then \
      adduser -D tester; else useradd -m tester; fi
COPY --chown=tester . /src
USER tester
WORKDIR /src
ENV GOFLAGS=-buildvcs=false
CMD ["sh", "-c", "if command -v gcc >/dev/null; then go test -race ./...; else go test ./...; fi"]

# The Go tests on a distribution without an official Go image (Fedora): the
# toolchain is copied from the official image, since it does not depend on the
# distribution's libraries, and the distribution supplies the C compiler that
# the race detector needs.
FROM golang:${GO_VERSION} AS gotoolchain
FROM ${BASE} AS unit-distro
RUN if command -v dnf >/dev/null; then dnf install -qy gcc && dnf clean all; \
    elif command -v apt-get >/dev/null; then apt-get update -q && apt-get install -qy --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*; \
    elif command -v pacman >/dev/null; then pacman -Sy --noconfirm --needed gcc; \
    else echo "unit-distro: no known package manager" >&2; exit 1; fi && \
    useradd -m tester
COPY --from=gotoolchain /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=local GOFLAGS=-buildvcs=false
COPY --chown=tester . /src
USER tester
WORKDIR /src
CMD ["go", "test", "-race", "./..."]
