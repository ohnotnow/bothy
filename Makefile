# Build bothy from a clone and install it:
#
#   make install                       # into ~/.local/bin
#   make install PREFIX=/somewhere     # into /somewhere/bin
#
# The binary is stamped with the git version, the build date, and a mark
# that it was built from source, so `bothy version` can say when a newer
# release is out and `bothy self-update` leaves it alone.

PREFIX ?= $(HOME)/.local
BINDIR := $(PREFIX)/bin

PKG     := github.com/ohnotnow/bothy/internal/bothy
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILT   := $(shell date -u +%Y-%m-%d)
LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).Built=$(BUILT) -X $(PKG).FromSource=yes

.PHONY: build install

build:
	go build -ldflags "$(LDFLAGS)" -o bothy .

# Copy to a temporary name, then rename over the old binary: macOS kills a
# binary whose file has been overwritten in place.
install: build
	mkdir -p "$(BINDIR)"
	cp bothy "$(BINDIR)/.bothy.tmp"
	mv -f "$(BINDIR)/.bothy.tmp" "$(BINDIR)/bothy"
	@echo "Installed $(VERSION) to $(BINDIR)/bothy"
	@case ":$$PATH:" in \
		*":$(BINDIR):"*) ;; \
		*) echo; \
		   echo "$(BINDIR) isn't on your PATH. Add this line to ~/.zshrc (or your shell's startup file), then open a new terminal:"; \
		   echo; \
		   echo '  export PATH="$(BINDIR):$$PATH"' ;; \
	esac
