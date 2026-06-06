# gotempo — build and user-level desktop install (no sudo required).
#
#   make            build the binary
#   make install    install binary, icon, and .desktop entry under ~/.local
#   make uninstall  remove them
#   make clean      remove the local build artifact
#
# Override the install root with PREFIX, e.g. `sudo make install PREFIX=/usr/local`.

PREFIX  ?= $(HOME)/.local
BINDIR  := $(PREFIX)/bin
DATADIR := $(PREFIX)/share
APPDIR  := $(DATADIR)/applications
ICONDIR := $(DATADIR)/icons/hicolor/512x512/apps

BIN := gotempo

ICON_SRC := assets/logo.png

.PHONY: all build install uninstall clean

all: build

build:
	go build -o $(BIN) .

install: build
	install -Dm755 $(BIN) $(BINDIR)/$(BIN)
	install -Dm644 $(ICON_SRC) $(ICONDIR)/$(BIN).png
	install -Dm644 $(BIN).desktop $(APPDIR)/$(BIN).desktop
	-update-desktop-database $(APPDIR) 2>/dev/null || true
	-gtk-update-icon-cache -f -t $(DATADIR)/icons/hicolor 2>/dev/null || true
	@echo "Installed $(BIN) to $(BINDIR) (ensure it is on your PATH)."

uninstall:
	rm -f $(BINDIR)/$(BIN)
	rm -f $(ICONDIR)/$(BIN).png
	rm -f $(APPDIR)/$(BIN).desktop
	-update-desktop-database $(APPDIR) 2>/dev/null || true
	-gtk-update-icon-cache -f -t $(DATADIR)/icons/hicolor 2>/dev/null || true

clean:
	rm -f $(BIN)
