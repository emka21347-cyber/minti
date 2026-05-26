# minti-pack-recon

Debian metapackage that installs the recon binaries `minti-mcp-recon` wraps:
nmap, masscan, whois, dig (dnsutils), theHarvester. Recommends amass; suggests
rustscan / golang-go for the optional Go-built tools.

## Build

From the repo root:

```sh
make pack-recon          # builds the .deb into dist/
```

Or natively inside Debian:

```sh
cd packs/recon
dpkg-buildpackage -b -uc -us
```

The `.deb` lands one directory above `packs/recon/` (Debian convention). The
top-level `make pack-recon` target moves it into `dist/`.

## What's in the package

- `/usr/share/minti/packs/recon/skill.md` — agent-facing skill file teaching
  the agent how to use each tool safely.
- Depends on apt-installable recon tools (see `debian/control`).

## What's NOT in the package

Tools that aren't in stock Debian 12: rustscan, dnsx, naabu, amass. The
skill.md file documents the manual install for each.
