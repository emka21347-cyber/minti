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
MCP_SERVERS   := mcp-fs mcp-shell mcp-recon mcp-pkg mcp-http
MCP_BINS      := $(addprefix $(MCP_DIR)/dist/minti-,$(addsuffix $(EXE),$(MCP_SERVERS)))
MCP_BINS_LINUX:= $(addprefix $(MCP_DIR)/dist/minti-,$(addsuffix -linux-amd64,$(MCP_SERVERS)))

DIST          := dist
PACKS_DIR     := packs
PACK_NAMES    := recon
# Pinned to M2 — bump per-milestone.
VERSION       := 0.1.0-M2
LDFLAGS       := -X main.version=$(VERSION)

# ---------- Phony targets ----------
.PHONY: help all runtime runtime-linux cland mcp mcp-linux mcptest mcptest-linux \
        packs pack-recon sign-recon \
        install-test test fmt vet tidy clean dist-dir

help:
	@echo "MINTI build targets:"
	@echo "  make all          — build everything available in the current milestone"
	@echo "  make runtime      — build minti-runtime (native)"
	@echo "  make runtime-linux— cross-compile minti-runtime for Linux amd64"
	@echo "  make mcp          — build the 5 MCP servers + mcptest (native)"
	@echo "  make mcp-linux    — cross-compile MCP servers for Linux amd64"
	@echo "  make packs        — build all debian tool packs"
	@echo "  make pack-recon   — build minti-pack-recon.deb (lands in dist/)"
	@echo "  make sign-recon   — sign the built .deb (requires MINTI_GPG_KEY env)"
	@echo "  make cland        — (M4) build the Clan daemon"
	@echo "  make install-test — run install.sh against a fresh Debian VM (TODO)"
	@echo "  make fmt vet      — gofmt + go vet on all Go modules"
	@echo "  make tidy         — go mod tidy on all Go modules"
	@echo "  make test         — go test ./... in all Go modules"
	@echo "  make clean        — remove build artifacts"

all: runtime mcp

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

# ---------- packs (M2 — recon only; M6 adds webapp/wireless/forensics) ----------
packs: pack-recon

pack-recon: dist-dir
	@echo ">> build minti-pack-recon"
	@command -v dpkg-buildpackage >/dev/null 2>&1 || { \
	  echo "ERROR: dpkg-buildpackage not found — run this on a Debian/Ubuntu host."; \
	  exit 1; \
	}
	@chmod +x $(PACKS_DIR)/recon/debian/rules
	@cd $(PACKS_DIR)/recon && dpkg-buildpackage -b -uc -us
	@mv $(PACKS_DIR)/minti-pack-recon_*.deb $(PACKS_DIR)/minti-pack-recon_*.buildinfo $(PACKS_DIR)/minti-pack-recon_*.changes $(DIST)/ 2>/dev/null || true
	@ls -la $(DIST)/minti-pack-recon_*.deb

# Test-key signing (M2). For the production key ceremony (M6), set
# MINTI_GPG_KEY to the project key ID and run this.
sign-recon:
	@test -n "$(MINTI_GPG_KEY)" || { echo "ERROR: set MINTI_GPG_KEY=<keyid>"; exit 1; }
	@deb=$$(ls -1t $(DIST)/minti-pack-recon_*.deb | head -n1); \
	  echo ">> sign $$deb with $(MINTI_GPG_KEY)"; \
	  dpkg-sig --sign builder -k $(MINTI_GPG_KEY) "$$deb"

# ---------- Future milestones ----------
cland:
	@echo "TODO M4: build cland Go binary"

install-test:
	@echo "TODO: run install/install.sh in a fresh Debian VM"

# ---------- Go hygiene ----------
GO_MODULES := $(RUNTIME_DIR) $(MCP_DIR)

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
	rm -rf $(DIST)
	find . -type f -name 'minti-runtime' -delete
	find . -type f -name 'cland' -delete
	find . -type f -name 'minti-mcp-*' -delete
	find . -type f -name 'mcptest' -delete
	find . -type f -name '*.deb' -delete
