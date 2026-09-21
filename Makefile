# fsb: build, test, install.
#
# The installation locations follow the usual conventions and can each be
# changed on the command line, for example:
#
#   make install PREFIX=/opt/homebrew
#   make install MANDIR=$$HOME/.local/share/man BINDIR=$$HOME/bin
#   make install DESTDIR=/tmp/stage          # for packaging

GO      ?= go
INSTALL ?= install

PREFIX  ?= /usr/local
BINDIR  ?= $(PREFIX)/bin
# The directory that holds the man1, man5, ... sections. The manual page goes
# into $(MANDIR)/man1.
MANDIR  ?= $(PREFIX)/share/man
MAN1DIR ?= $(MANDIR)/man1

BIN     = fsb
MANPAGE = man/fsb.1

.PHONY: all build test man-lint install install-bin install-man uninstall \
        uninstall-bin uninstall-man specs clean help

all: build

build:
	$(GO) build -trimpath -o $(BIN) ./cmd/fsb

test:
	$(GO) vet ./...
	$(GO) test -race ./...

# The manual page must be free of errors and warnings.
man-lint:
	mandoc -Tlint $(MANPAGE)

# install puts the program in BINDIR and the manual page in MAN1DIR.
install: install-bin install-man

install-bin: build
	$(INSTALL) -d $(DESTDIR)$(BINDIR)
	$(INSTALL) -m 0755 $(BIN) $(DESTDIR)$(BINDIR)/$(BIN)

install-man:
	$(INSTALL) -d $(DESTDIR)$(MAN1DIR)
	$(INSTALL) -m 0644 $(MANPAGE) $(DESTDIR)$(MAN1DIR)/fsb.1

uninstall: uninstall-bin uninstall-man

uninstall-bin:
	$(RM) $(DESTDIR)$(BINDIR)/$(BIN)

uninstall-man:
	$(RM) $(DESTDIR)$(MAN1DIR)/fsb.1

# The specifications (LaTeX; needs latexmk) and their Overleaf packages.
specs:
	$(MAKE) -C specs

clean:
	$(RM) $(BIN)

help:
	@echo 'make               Build ./fsb'
	@echo 'make test          go vet and go test -race'
	@echo 'make install       Install fsb to BINDIR and fsb.1 to MAN1DIR'
	@echo 'make install-man   Install only the manual page'
	@echo 'make uninstall     Remove what install put in place'
	@echo 'make man-lint      Check the manual page with mandoc'
	@echo 'make specs         Build the specification PDFs'
	@echo ''
	@echo 'Variables (override on the command line):'
	@echo '  PREFIX  = $(PREFIX)'
	@echo '  BINDIR  = $(BINDIR)'
	@echo '  MANDIR  = $(MANDIR)'
	@echo '  MAN1DIR = $(MAN1DIR)'
	@echo '  DESTDIR = $(DESTDIR)   (staging root for packaging)'
