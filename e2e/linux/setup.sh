#!/bin/sh
# Prepares a distribution image for smoke.sh. POSIX sh: Alpine has only
# BusyBox ash.
#
#   setup.sh           as root: install curl and create the user "tester"
#   setup.sh fixture   as tester: fill the home directory with test files
set -eu

if [ "${1:-}" != fixture ]; then
	if ! command -v curl >/dev/null; then
		if command -v apt-get >/dev/null; then
			apt-get update -q && apt-get install -qy --no-install-recommends curl
			rm -rf /var/lib/apt/lists/*
		elif command -v dnf >/dev/null; then
			dnf install -qy curl && dnf clean all
		elif command -v apk >/dev/null; then
			apk add --no-cache curl
		elif command -v pacman >/dev/null; then
			pacman -Sy --noconfirm curl
		else
			echo "setup.sh: no known package manager" >&2
			exit 1
		fi
	fi
	# BusyBox has adduser but not useradd.
	if command -v useradd >/dev/null; then
		useradd -m -s /bin/sh tester
	else
		adduser -D -s /bin/sh tester
	fi
	exit 0
fi

# The fixture: ordinary files, the secrets fsb must refuse (macOS and Linux
# locations), and the Linux-only oddities listed in Phase 3 of the plan.
cd "$HOME"
mkdir -p docs .ssh .aws \
	".config/google-chrome/Default" ".config/chromium/Default" \
	".config/BraveSoftware/Brave-Browser/Default" ".config/microsoft-edge/Default" \
	".mozilla/firefox/abc.default" .local/share/keyrings .password-store
echo "hello from $(. /etc/os-release && echo "$PRETTY_NAME")" > docs/readme.txt
echo "secret" > .ssh/id_ed25519
echo "secret" > .aws/credentials
echo "secret" > .config/google-chrome/Default/Cookies
echo "secret" > ".config/chromium/Default/Login Data"
echo "secret" > ".config/BraveSoftware/Brave-Browser/Default/Login Data"
echo "secret" > .config/microsoft-edge/Default/Cookies
echo "secret" > .mozilla/firefox/abc.default/logins.json
echo "secret" > .local/share/keyrings/login.keyring
echo "secret" > .password-store/email.gpg
# A name that is not valid UTF-8 (byte 0xE9, Latin-1 "é"): legal on Linux, impossible on APFS.
printf 'latin-1 name\n' > "$(printf 'caf\351.txt')"
# Differs from Readme.TXT only in case: two files on Linux, one on macOS.
echo "upper" > docs/Readme.TXT
echo "unreadable" > docs/locked.txt && chmod 000 docs/locked.txt
ln -s /etc escape
