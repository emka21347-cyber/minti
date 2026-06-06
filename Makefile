# MINTI top-level Makefile

# ---------- Variables ----------
GO            ?= go
GOOS_LINUX    := linux
GOARCH_AMD64  := amd64

RUNTIME_PKG   := ./cmd/minti-runtime
RUNTIME_DIR   := runtime-adapter
RUNTIME_BIN   := $(RUNTIME_DIR)/minti-runtime
RUNTIME_BIN_LINUX := $(RUNTIME_DIR)/dist/minti-runtime-linux-amd64

MCP_DIR       := mcp-servers
MCP_SERVERS   := mcp-fs mcp-shell mcp-recon mcp-pkg mcp-http mcp-wiki
MCP_BINS      := $(addprefix $(MCP_DIR)/dist/minti-,$(addsuffix $(EXE),$(MCP_SERVERS)))
MCP_BINS_LINUX:= $(addprefix $(MCP_DIR)/dist/minti-,$(addsuffix -linux-amd64,$(MCP_SERVERS)))

PM_DIR        := pack-manager
PM_PKG        := ./cmd/minti-pack-fetch
PM_BIN        := $(PM_DIR)/minti-pack-fetch
PM_BIN_LINUX  := $(PM_DIR)/dist/minti-pack-fetch-linux-amd64

STATUS_DIR              := status
STATUS_PKG              := ./cmd/minti-status
STATUS_BIN              := $(STATUS_DIR)/minti-status
STATUS_BIN_LINUX        := $(STATUS_DIR)/dist/minti-status-linux-amd64
STATUS_BIN_WINDOWS      := $(STATUS_DIR)/dist/minti-status-windows-amd64.exe
STATUS_BIN_DARWIN_AMD64 := $(STATUS_DIR)/dist/minti-status-darwin-amd64
STATUS_BIN_DARWIN_ARM64 := $(STATUS_DIR)/dist/minti-status-darwin-arm64
STATUS_LDFLAGS          := -X github.com/minti/status/internal/version.Version=$(VERSION)

CLAND_DIR             := cland
CLAND_PKG             := ./cmd/minti-cland
CLAND_BIN             := $(CLAND_DIR)/minti-cland
CLAND_BIN_LINUX       := $(CLAND_DIR)/dist/minti-cland-linux-amd64
CLAND_BIN_WINDOWS     := $(CLAND_DIR)/dist/minti-cland-windows-amd64.exe
CLAND_BIN_DARWIN_AMD64 := $(CLAND_DIR)/dist/minti-cland-darwin-amd64
CLAND_BIN_DARWIN_ARM64 := $(CLAND_DIR)/dist/minti-cland-darwin-arm64

DIST          := dist
PACKS_DIR     := packs
PACK_NAMES    := recon hermes3 mistral wiki-simple
# Pinned per-milestone — bump on milestone landing.
VERSION       := 0.3.0-M7.5
LDFLAGS       := -X main.version=$(VERSION)
# Release build flags: strip debug info, omit absolute build paths. ~30%
# smaller binaries + no leaked /Users/... paths in error messages.
LDFLAGS_REL   := $(LDFLAGS) -s -w
GOFLAGS_REL   := -trimpath

# ---------- Phony targets ----------
.PHONY: help all runtime runtime-linux \
        cland cland-linux cland-windows cland-darwin-amd64 cland-darwin-arm64 cland-all-platforms cland-windows-zip cland-darwin-tarball \
        mcp mcp-linux mcptest mcptest-linux \
        pack-fetch pack-fetch-linux pack-fetch-deb \
        packs pack-recon pack-hermes3 pack-mistral pack-wiki-simple sign-recon \
        status status-linux status-windows status-darwin-amd64 status-darwin-arm64 status-all-platforms status-deb status-gif \
        install-test test fmt vet tidy clean dist-dir

help:
	@echo "MINTI build targets:"
	@echo "  make all          — build everything available in the current milestone"
	@echo "  make runtime      — build minti-runtime (native)"
	@echo "  make runtime-linux— cross-compile minti-runtime for Linux amd64"
	@echo "  make mcp          — build the 5 MCP servers + mcptest (native)"
	@echo "  make mcp-linux    — cross-compile MCP servers for Linux amd64"
	@echo "  make pack-fetch        — build minti-pack-fetch helper (native)"
	@echo "  make pack-fetch-linux  — cross-compile minti-pack-fetch for Linux"
	@echo "  make pack-fetch-deb    — build minti-pack-fetch_*.deb (run on Debian)"
	@echo "  make packs             — build all debian tool + addon packs"
	@echo "  make pack-recon        — build minti-pack-recon.deb (lands in dist/)"
	@echo "  make pack-hermes3      — build minti-pack-hermes3.deb (addon: chat model)"
	@echo "  make pack-mistral      — build minti-pack-mistral.deb (addon: chat model)"
	@echo "  make pack-wiki-simple  — build minti-pack-wiki-simple.deb (addon: offline Wikipedia)"
	@echo "  make sign-recon        — sign the built .deb (requires MINTI_GPG_KEY env)"
	@echo "  make cland               — build minti-cland (native)"
	@echo "  make cland-linux         — cross-compile minti-cland for Linux amd64"
	@echo "  make cland-windows       — cross-compile minti-cland for Windows amd64 (.exe)"
	@echo "  make cland-darwin-amd64  — cross-compile minti-cland for macOS x86_64"
	@echo "  make cland-darwin-arm64  — cross-compile minti-cland for macOS arm64 (Apple Silicon)"
	@echo "  make cland-all-platforms — all four cland binaries"
	@echo "  make cland-windows-zip   — bundle Windows .zip distribution (NSSM service)"
	@echo "  make cland-darwin-tarball— bundle macOS .tar.gz distributions (amd64 + arm64, launchd)"
	@echo "  make status              — build minti-status TUI dashboard (native)"
	@echo "  make status-linux        — cross-compile minti-status for Linux amd64"
	@echo "  make status-windows      — cross-compile minti-status for Windows amd64"
	@echo "  make status-darwin-amd64 — cross-compile minti-status for macOS x86_64"
	@echo "  make status-darwin-arm64 — cross-compile minti-status for macOS arm64"
	@echo "  make status-all-platforms — all four minti-status binaries"
	@echo "  make status-deb          — minti-status_*.deb (binary at /usr/bin/minti-status)"
	@echo "  make install-test — run install.sh against a fresh Debian VM (TODO)"
	@echo "  make fmt vet      — gofmt + go vet on all Go modules"
	@echo "  make tidy         — go mod tidy on all Go modules"
	@echo "  make test         — go test ./... in all Go modules"
	@echo "  make clean        — remove build artifacts"

all: runtime mcp cland pack-fetch status

dist-dir:
	@mkdir -p $(DIST)

# ---------- runtime-adapter (M1) ----------
runtime: $(RUNTIME_BIN)

$(RUNTIME_BIN):
	cd $(RUNTIME_DIR) && $(GO) build -ldflags "$(LDFLAGS)" -o minti-runtime $(RUNTIME_PKG)

runtime-linux:
	mkdir -p $(RUNTIME_DIR)/dist
	cd $(RUNTIME_DIR) && GOOS=$(GOOS_LINUX) GOARCH=$(GOARCH_AMD64) $(GO) build -ldflags "$(LDFLAGS)" -o dist/minti-runtime-linux-amd64 $(RUNTIME_PKG)

# ---------- mcp-servers (M2) ----------
mcp:
	@mkdir -p $(MCP_DIR)/dist
	@for s in $(MCP_SERVERS); do \
	  echo ">> build $$s (native)"; \
	  cd $(MCP_DIR) && $(GO) build -ldflags "$(LDFLAGS)" -o dist/minti-$$s ./cmd/$$s && cd - >/dev/null; \
	done
	@echo ">> build mcptest (native)"
	@cd $(MCP_DIR) && $(GO) build -ldflags "$(LDFLAGS)" -o dist/mcptest ./cmd/mcptest

mcp-linux:
	@mkdir -p $(MCP_DIR)/dist
	@for s in $(MCP_SERVERS); do \
	  echo ">> build $$s (linux/amd64)"; \
	  cd $(MCP_DIR) && GOOS=$(GOOS_LINUX) GOARCH=$(GOARCH_AMD64) $(GO) build -ldflags "$(LDFLAGS)" -o dist/minti-$$s-linux-amd64 ./cmd/$$s && cd - >/dev/null; \
	done
	@echo ">> build mcptest (linux/amd64)"
	@cd $(MCP_DIR) && GOOS=$(GOOS_LINUX) GOARCH=$(GOARCH_AMD64) $(GO) build -ldflags "$(LDFLAGS)" -o dist/mcptest-linux-amd64 ./cmd/mcptest

# ---------- pack-manager (M6) ----------
pack-fetch: $(PM_BIN)

$(PM_BIN):
	cd $(PM_DIR) && $(GO) build -ldflags "$(LDFLAGS)" -o minti-pack-fetch $(PM_PKG)

pack-fetch-linux:
	mkdir -p $(PM_DIR)/dist
	cd $(PM_DIR) && GOOS=$(GOOS_LINUX) GOARCH=$(GOARCH_AMD64) $(GO) build $(GOFLAGS_REL) -ldflags "$(LDFLAGS_REL)" -o dist/minti-pack-fetch-linux-amd64 $(PM_PKG)

# Build minti-pack-fetch as a .deb. Source-tree layout differs from the
# packs/ macro (debian/ lives inside pack-manager/, not in packs/<name>/),
# so it has its own target.
#
# Ships a PRE-BUILT binary — requires pack-fetch-linux to run first (handled
# as a make-dependency below). The pre-built strategy keeps Build-Depends to
# debhelper only (no golang-go), and matches how cland's Windows .zip and
# macOS .tar.gz are assembled.
#
# chmod knobs mirror the build-pack macro for vboxsf safety. The pre-build
# binary is staged at pack-manager/minti-pack-fetch (without the -linux-amd64
# suffix) so debian/install can refer to it by the deployed name.
pack-fetch-deb: dist-dir pack-fetch-linux
	@echo ">> build minti-pack-fetch.deb"
	@command -v dpkg-buildpackage >/dev/null 2>&1 || { \
	  echo "ERROR: dpkg-buildpackage not found — run this on a Debian/Ubuntu host."; \
	  exit 1; \
	}
	@# Stage the pre-built binary at the name debian/install expects.
	@cp $(PM_DIR)/dist/minti-pack-fetch-linux-amd64 $(PM_DIR)/minti-pack-fetch
	@chmod 0755 $(PM_DIR)/minti-pack-fetch
	@chmod +x $(PM_DIR)/debian/rules
	@chmod -x $(PM_DIR)/debian/install      2>/dev/null || true
	@chmod -x $(PM_DIR)/debian/control      2>/dev/null || true
	@chmod -x $(PM_DIR)/debian/changelog    2>/dev/null || true
	@chmod -x $(PM_DIR)/debian/copyright    2>/dev/null || true
	@chmod -x $(PM_DIR)/debian/source/format 2>/dev/null || true
	@cd $(PM_DIR) && dpkg-buildpackage -b -uc -us
	@mv minti-pack-fetch_*.deb minti-pack-fetch_*.buildinfo minti-pack-fetch_*.changes $(DIST)/ 2>/dev/null || true
	@rm -f $(PM_DIR)/minti-pack-fetch
	@ls -la $(DIST)/minti-pack-fetch_*.deb

# ---------- packs (M2 = recon; M6-content = hermes3 + mistral + wiki-simple) ----------
packs: pack-recon pack-hermes3 pack-mistral pack-wiki-simple

# Common dpkg-buildpackage runner. $(1) = pack name (matches packs/<name>/).
# Uses the same chmod-rules + buildpackage pattern that pack-recon used at M2.
define build-pack
	@echo ">> build minti-pack-$(1)"
	@command -v dpkg-buildpackage >/dev/null 2>&1 || { \
	  echo "ERROR: dpkg-buildpackage not found — run this on a Debian/Ubuntu host."; \
	  exit 1; \
	}
	@chmod +x $(PACKS_DIR)/$(1)/debian/rules
	@chmod +x $(PACKS_DIR)/$(1)/debian/postinst 2>/dev/null || true
	@chmod +x $(PACKS_DIR)/$(1)/debian/postrm   2>/dev/null || true
	@chmod +x $(PACKS_DIR)/$(1)/debian/prerm    2>/dev/null || true
	@# vboxsf shared-folder mounts mark everything executable by default,
	@# which causes dh_install to treat debian/install + debian/dirs as
	@# executable config scripts (then tries to run them and chokes on the
	@# bare "skill.md" line). Force them non-executable before build.
	@chmod -x $(PACKS_DIR)/$(1)/debian/install 2>/dev/null || true
	@chmod -x $(PACKS_DIR)/$(1)/debian/dirs    2>/dev/null || true
	@chmod -x $(PACKS_DIR)/$(1)/debian/control 2>/dev/null || true
	@chmod -x $(PACKS_DIR)/$(1)/debian/changelog 2>/dev/null || true
	@chmod -x $(PACKS_DIR)/$(1)/debian/copyright 2>/dev/null || true
	@chmod -x $(PACKS_DIR)/$(1)/debian/source/format 2>/dev/null || true
	@cd $(PACKS_DIR)/$(1) && dpkg-buildpackage -b -uc -us
	@mv $(PACKS_DIR)/minti-pack-$(1)_*.deb $(PACKS_DIR)/minti-pack-$(1)_*.buildinfo $(PACKS_DIR)/minti-pack-$(1)_*.changes $(DIST)/ 2>/dev/null || true
	@ls -la $(DIST)/minti-pack-$(1)_*.deb
endef

pack-recon: dist-dir
	$(call build-pack,recon)

pack-hermes3: dist-dir
	$(call build-pack,hermes3)

pack-mistral: dist-dir
	$(call build-pack,mistral)

pack-wiki-simple: dist-dir
	$(call build-pack,wiki-simple)

# Test-key signing (M2). For the production key ceremony (M6), set
# MINTI_GPG_KEY to the project key ID and run this.
sign-recon:
	@test -n "$(MINTI_GPG_KEY)" || { echo "ERROR: set MINTI_GPG_KEY=<keyid>"; exit 1; }
	@deb=$$(ls -1t $(DIST)/minti-pack-recon_*.deb | head -n1); \
	  echo ">> sign $$deb with $(MINTI_GPG_KEY)"; \
	  dpkg-sig --sign builder -k $(MINTI_GPG_KEY) "$$deb"

# ---------- cland (M4 + M5 cross-OS) ----------
cland: $(CLAND_BIN)

$(CLAND_BIN):
	cd $(CLAND_DIR) && $(GO) build -ldflags "$(LDFLAGS)" -o minti-cland $(CLAND_PKG)

cland-linux:
	mkdir -p $(CLAND_DIR)/dist
	cd $(CLAND_DIR) && GOOS=$(GOOS_LINUX) GOARCH=$(GOARCH_AMD64) $(GO) build $(GOFLAGS_REL) -ldflags "$(LDFLAGS_REL)" -o dist/minti-cland-linux-amd64 $(CLAND_PKG)

cland-windows:
	mkdir -p $(CLAND_DIR)/dist
	cd $(CLAND_DIR) && GOOS=windows GOARCH=$(GOARCH_AMD64) $(GO) build $(GOFLAGS_REL) -ldflags "$(LDFLAGS_REL)" -o dist/minti-cland-windows-amd64.exe $(CLAND_PKG)

cland-darwin-amd64:
	mkdir -p $(CLAND_DIR)/dist
	cd $(CLAND_DIR) && GOOS=darwin GOARCH=$(GOARCH_AMD64) $(GO) build $(GOFLAGS_REL) -ldflags "$(LDFLAGS_REL)" -o dist/minti-cland-darwin-amd64 $(CLAND_PKG)

cland-darwin-arm64:
	mkdir -p $(CLAND_DIR)/dist
	cd $(CLAND_DIR) && GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS_REL) -ldflags "$(LDFLAGS_REL)" -o dist/minti-cland-darwin-arm64 $(CLAND_PKG)

cland-all-platforms: cland-linux cland-windows cland-darwin-amd64 cland-darwin-arm64

# ---------- status (M7 — TUI dashboard) ----------
status: $(STATUS_BIN)

$(STATUS_BIN):
	cd $(STATUS_DIR) && $(GO) build -ldflags "$(STATUS_LDFLAGS)" -o minti-status $(STATUS_PKG)

status-linux:
	mkdir -p $(STATUS_DIR)/dist
	cd $(STATUS_DIR) && GOOS=$(GOOS_LINUX) GOARCH=$(GOARCH_AMD64) $(GO) build $(GOFLAGS_REL) -ldflags "$(LDFLAGS_REL) $(STATUS_LDFLAGS)" -o dist/minti-status-linux-amd64 $(STATUS_PKG)

status-windows:
	mkdir -p $(STATUS_DIR)/dist
	cd $(STATUS_DIR) && GOOS=windows GOARCH=$(GOARCH_AMD64) $(GO) build $(GOFLAGS_REL) -ldflags "$(LDFLAGS_REL) $(STATUS_LDFLAGS)" -o dist/minti-status-windows-amd64.exe $(STATUS_PKG)

status-darwin-amd64:
	mkdir -p $(STATUS_DIR)/dist
	cd $(STATUS_DIR) && GOOS=darwin GOARCH=$(GOARCH_AMD64) $(GO) build $(GOFLAGS_REL) -ldflags "$(LDFLAGS_REL) $(STATUS_LDFLAGS)" -o dist/minti-status-darwin-amd64 $(STATUS_PKG)

status-darwin-arm64:
	mkdir -p $(STATUS_DIR)/dist
	cd $(STATUS_DIR) && GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS_REL) -ldflags "$(LDFLAGS_REL) $(STATUS_LDFLAGS)" -o dist/minti-status-darwin-arm64 $(STATUS_PKG)

status-all-platforms: status-linux status-windows status-darwin-amd64 status-darwin-arm64

# Render the README .gif from status/docs/minti-status.tape.
# Requires `vhs` on PATH (https://github.com/charmbracelet/vhs). vhs in
# turn needs ttyd + ffmpeg available — install via your package manager
# or pull the GitHub release. NOT a CI dependency; this is an authoring
# tool. Output: status/docs/minti-status.gif (gitignored).
status-gif:
	@command -v vhs >/dev/null 2>&1 || { \
	  echo "ERROR: vhs not on PATH."; \
	  echo "Install from https://github.com/charmbracelet/vhs"; \
	  echo "  Linux (Debian/Ubuntu): use the GitHub release tarball — apt doesn't ship vhs yet."; \
	  echo "  macOS: brew install vhs"; \
	  exit 1; \
	}
	vhs $(STATUS_DIR)/docs/minti-status.tape

# Build minti-status as a .deb. Ships a pre-built binary at /usr/bin/minti-status.
# Mirrors pack-fetch-deb structure.
status-deb: dist-dir status-linux
	@echo ">> build minti-status.deb"
	@command -v dpkg-buildpackage >/dev/null 2>&1 || { \
	  echo "ERROR: dpkg-buildpackage not found — run this on a Debian/Ubuntu host."; \
	  exit 1; \
	}
	@cp $(STATUS_DIR)/dist/minti-status-linux-amd64 $(STATUS_DIR)/minti-status
	@chmod 0755 $(STATUS_DIR)/minti-status
	@chmod +x $(STATUS_DIR)/debian/rules
	@chmod -x $(STATUS_DIR)/debian/install      2>/dev/null || true
	@chmod -x $(STATUS_DIR)/debian/control      2>/dev/null || true
	@chmod -x $(STATUS_DIR)/debian/changelog    2>/dev/null || true
	@chmod -x $(STATUS_DIR)/debian/copyright    2>/dev/null || true
	@chmod -x $(STATUS_DIR)/debian/source/format 2>/dev/null || true
	@cd $(STATUS_DIR) && dpkg-buildpackage -b -uc -us
	@mv minti-status_*.deb minti-status_*.buildinfo minti-status_*.changes $(DIST)/ 2>/dev/null || true
	@rm -f $(STATUS_DIR)/minti-status
	@ls -la $(DIST)/minti-status_*.deb

# Bundle the Windows NSSM-managed-service distribution.
# Runs the PowerShell builder; produces dist/minti-cland-windows-amd64-v$VERSION.zip.
# pwsh.exe is preferred (PS7), falls back to powershell.exe (PS5.1).
cland-windows-zip:
	@if command -v pwsh >/dev/null 2>&1; then \
	  pwsh -NoProfile -ExecutionPolicy Bypass -File $(CLAND_DIR)/windows/nssm/build-zip.ps1 -Version $(VERSION); \
	else \
	  powershell.exe -NoProfile -ExecutionPolicy Bypass -File $(CLAND_DIR)/windows/nssm/build-zip.ps1 -Version $(VERSION); \
	fi

# Bundle the macOS launchd-managed-service distribution(s).
# Produces dist/minti-cland-darwin-amd64-v$VERSION.tar.gz +
# dist/minti-cland-darwin-arm64-v$VERSION.tar.gz.
cland-darwin-tarball:
	VERSION=$(VERSION) GO=$(GO) bash $(CLAND_DIR)/darwin/build-tarball.sh

install-test:
	@echo "TODO: run install/install.sh in a fresh Debian VM"

# ---------- Go hygiene ----------
GO_MODULES := $(RUNTIME_DIR) $(MCP_DIR) $(CLAND_DIR) $(PM_DIR) $(STATUS_DIR)

fmt:
	@for m in $(GO_MODULES); do echo ">> gofmt $$m"; cd $$m && $(GO) fmt ./... && cd - >/dev/null; done

vet:
	@for m in $(GO_MODULES); do echo ">> go vet $$m"; cd $$m && $(GO) vet ./... && cd - >/dev/null; done

tidy:
	@for m in $(GO_MODULES); do echo ">> go mod tidy $$m"; cd $$m && $(GO) mod tidy && cd - >/dev/null; done

test:
	@for m in $(GO_MODULES); do echo ">> go test $$m"; cd $$m && $(GO) test ./... && cd - >/dev/null; done

clean:
	rm -rf $(RUNTIME_DIR)/minti-runtime $(RUNTIME_DIR)/dist
	rm -rf $(MCP_DIR)/dist
	rm -rf $(CLAND_DIR)/minti-cland $(CLAND_DIR)/minti-cland.exe $(CLAND_DIR)/dist
	rm -rf $(PM_DIR)/minti-pack-fetch $(PM_DIR)/minti-pack-fetch.exe $(PM_DIR)/dist
	rm -rf $(PM_DIR)/debian/.gocache $(PM_DIR)/debian/.debhelper $(PM_DIR)/debian/files $(PM_DIR)/debian/minti-pack-fetch $(PM_DIR)/debian/minti-pack-fetch.substvars $(PM_DIR)/debian/debhelper-build-stamp
	rm -rf $(STATUS_DIR)/minti-status $(STATUS_DIR)/minti-status.exe $(STATUS_DIR)/dist
	rm -rf $(STATUS_DIR)/debian/.debhelper $(STATUS_DIR)/debian/files $(STATUS_DIR)/debian/minti-status $(STATUS_DIR)/debian/minti-status.substvars $(STATUS_DIR)/debian/debhelper-build-stamp
	rm -rf $(DIST)
	find . -type f -name 'minti-runtime' -delete
	find . -type f -name 'minti-cland' -delete
	find . -type f -name 'minti-mcp-*' -delete
	find . -type f -name 'minti-pack-fetch' -delete
	find . -type f -name 'mcptest' -delete
	find . -type f -name '*.deb' -delete
