# gotempo — build and user-level desktop install (no sudo required).
#
#   make                  build the binary
#   make install          install binary, icon, and .desktop entry under ~/.local
#   make uninstall        remove them
#   make windows          cross-compile the Windows binaries (no Windows toolchain needed)
#   make clean            remove the local build artifacts
#   make release VERSION=v1.2.3   tag the current commit and push it (CI builds the release)
#
# Override the install root with PREFIX, e.g. `sudo make install PREFIX=/usr/local`.

PREFIX  ?= $(HOME)/.local
BINDIR  := $(PREFIX)/bin
DATADIR := $(PREFIX)/share
APPDIR  := $(DATADIR)/applications
ICONDIR := $(DATADIR)/icons/hicolor/512x512/apps

BIN := gotempo

ICON_SRC := internal/app/assets/logo.png

# Version is derived from git tags (e.g. v1.2.3, or v1.2.3-4-gabc123-dirty
# between tags) and baked into the binary. Falls back to "dev" with no tags.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X gotempo/internal/app.version=$(VERSION)

# The .desktop entry is fully static, so it is generated at install time rather
# than kept as a file in the tree. scripts/install.sh carries the same content
# for the release tarball (which ships without this Makefile).
define DESKTOP_ENTRY
[Desktop Entry]
Type=Application
Name=gotempo
Comment=Heart-rate monitor tray app
Exec=gotempo
Icon=gotempo
Terminal=false
Categories=Utility;
endef
export DESKTOP_ENTRY

.PHONY: all build install uninstall clean release windows

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

install: build
	install -Dm755 $(BIN) $(BINDIR)/$(BIN)
	install -Dm644 $(ICON_SRC) $(ICONDIR)/$(BIN).png
	@mkdir -p $(APPDIR)
	@printf '%s\n' "$$DESKTOP_ENTRY" > $(APPDIR)/$(BIN).desktop
	-update-desktop-database $(APPDIR) 2>/dev/null || true
	-gtk-update-icon-cache -f -t $(DATADIR)/icons/hicolor 2>/dev/null || true
	@echo "Installed $(BIN) to $(BINDIR) (ensure it is on your PATH)."

uninstall:
	rm -f $(BINDIR)/$(BIN)
	rm -f $(ICONDIR)/$(BIN).png
	rm -f $(APPDIR)/$(BIN).desktop
	-update-desktop-database $(APPDIR) 2>/dev/null || true
	-gtk-update-icon-cache -f -t $(DATADIR)/icons/hicolor 2>/dev/null || true

# Cross-compiled from Linux: the Windows BLE backend is pure Go (WinRT through
# syscall, no cgo), so only the Go compiler is needed. Mirrors the two binaries
# the release workflow ships. -H=windowsgui keeps the tray app from opening a
# console window, which also detaches it from the parent console, so the command
# line is a separate console binary.
windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-H=windowsgui $(LDFLAGS)" -o $(BIN).exe .
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN)-cli.exe .

clean:
	rm -f $(BIN) $(BIN).exe $(BIN)-cli.exe

# Cut a release: validate, test, create an annotated tag, and push it. The
# tag push triggers .github/workflows/release.yml, which builds the binary
# (embedding the tag as the version) and publishes a GitHub release.
release:
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=v1.2.3"; exit 1; }
	@echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$$' || \
		{ echo "VERSION must look like v1.2.3 (got '$(VERSION)')"; exit 1; }
	@git diff --quiet || { echo "working tree is dirty; commit or stash first"; exit 1; }
	go test ./...
	git tag -a "$(VERSION)" -m "gotempo $(VERSION)"
	git push origin "$(VERSION)"
	@echo "Pushed tag $(VERSION). GitHub Actions will build and publish the release."
