# MINTI top-level Makefile (M0 stub — most targets land in later milestones)

.PHONY: help all cland runtime mcp packs install-test clean

help:
	@echo "MINTI build targets:"
	@echo "  make all            — build everything (cland + runtime-adapter + mcp-servers)"
	@echo "  make cland          — build the Clan daemon"
	@echo "  make runtime        — build minti-runtime adapter"
	@echo "  make mcp            — build all MCP servers"
	@echo "  make packs          — build all debian tool packs"
	@echo "  make install-test   — run install.sh against a fresh Debian VM (TODO M0)"
	@echo "  make clean          — remove build artifacts"

all: cland runtime mcp

cland:
	@echo "TODO M4: build cland Go binary"
	@# cd cland && go build -o cland ./cmd/cland

runtime:
	@echo "TODO M1: build runtime-adapter"
	@# cd runtime-adapter && go build -o runtime-adapter ./cmd/runtime-adapter

mcp:
	@echo "TODO M2: build MCP servers"

packs:
	@echo "TODO M2/M6: build debian metapackages"

install-test:
	@echo "TODO M0: run install/install.sh in a fresh Debian VM"
	@# vagrant up debian12 && vagrant ssh -c "sudo bash /vagrant/install/install.sh"

clean:
	rm -rf build/ dist/
	find . -type f -name '*.deb' -delete
	find . -type f -name 'cland' -delete
	find . -type f -name 'runtime-adapter' -delete
