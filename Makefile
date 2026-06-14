# wepapered — four binaries from one module, built into ./bin.
#
#   make            build all into ./bin
#   make daemon     the background renderer/daemon (links LWE + the gtk-layer-shell
#                   loading overlay)
#   make gui        the WebKit browse window
#   make settings   the GTK settings window
#   make ctl        the wepaperedctl dispatcher
#   make vet        go vet ./...
#   make clean      remove ./bin
#
# The daemon links the prebuilt LWE shared library in lwe/build/output, which
# must be built first (see CLAUDE.md "Build & run"). gui/settings need only the
# gtk/webkit dev packages; ctl needs nothing native. Keep the four binaries
# together (they locate each other and the LWE dir relative to their own path).

GO  ?= go
BIN ?= bin

.PHONY: all daemon gui settings ctl vet clean

all: daemon gui settings ctl

daemon: | $(BIN)
	$(GO) build -o $(BIN)/wepapered-daemon ./cmd/wepapered-daemon

gui: | $(BIN)
	$(GO) build -o $(BIN)/wepapered-gui ./cmd/wepapered-gui

settings: | $(BIN)
	$(GO) build -o $(BIN)/wepapered-settings ./cmd/wepapered-settings

ctl: | $(BIN)
	$(GO) build -o $(BIN)/wepaperedctl ./cmd/wepaperedctl

$(BIN):
	mkdir -p $(BIN)

vet:
	$(GO) vet ./...

clean:
	rm -rf $(BIN)
