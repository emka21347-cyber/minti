# MINTI top-level Makefile

# ---------- Variables ----------
GO            ?= go
GOOS_LINUX    := linux
GOARCH_AMD64  := amd64

RUNTIME_PKG   := ./cmd/minti-runtime
RUNTIME_DIR   := runtime-adapter
RUNTIME_BIN   := $(RUNTIME_DIR)/minti-runtime
RUNTIME_BIN_LINUX := $(RUNTIME_DIR)/dist/minti-runtime-linux-amd64

VERSION       := 0.1.0-M1
LDFLAGS       := -X main.version=$(VERSION)

# ---------- Phony targets ----------
.PHONY: help all runtime runtime-linux cland mcp packs \
        install-test test fmt vet tidy clean

help:
	@echo "MINTI build targets:"
	@echo "  make all          — build everything available in the current milestone"
	@echo "  make runtime      — build minti-runtime (native)"
	@echo "  make runtime-linux— cross-compile minti-runtime for Linux amd64"
	@echo "  make cland        — (M4) build the Clan daemon"
	@echo "  make mcp          — (M2) build MCP servers"
	@echo "  make packs        — (M2/M6) build debian tool packs"
	@echo "  make install-test — run install.sh against a fresh Debian VM (TODO)"
	@echo "  make fmt vet      — gofmt + go vet on all Go modules"
	@echo "  make tidy         — go mod tidy on all Go modules"
	@echo "  make test         — go test ./... in all Go modules"
	@echo "  make clean        — remove build artifacts"

all: runtime

# ---------- runtime-adapter ----------
runtime: $(RUNTIME_BIN)

$(RUNTIME_BIN):
	cd $(RUNTIME_DIR) && $(GO) build -ldflags "$(LDFLAGS)" -o minti-runtime $(RUNTIME_PKG)

runtime-linux:
	mkdir -p $(RUNTIME_DIR)/dist
	cd $(RUNTIME_DIR) && GOOS=$(GOOS_LINUX) GOARCH=$(GOARCH_AMD64) $(GO) build -ldflags "$(LDFLAGS)" -o dist/minti-runtime-linux-amd64 $(RUNTIME_PKG)

# ---------- Placeholders for future milestones ----------
cland:
	@echo "TODO M4: build cland Go binary"

mcp:
	@echo "TODO M2: build MCP servers"

packs:
	@echo "TODO M2/M6: build debian metapackages"

install-test:
	@echo "TODO: run install/install.sh in a fresh Debian VM"
	@# vagrant up debian12 && vagrant ssh -c "sudo bash /vagrant/install/install.sh"

# ---------- Go hygiene (runs against every Go module in the tree) ----------
GO_MODULES := $(RUNTIME_DIR)

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
	find . -type f -name 'minti-runtime' -delete
	find . -type f -name 'cland' -delete
	find . -type f -name '*.deb' -delete
