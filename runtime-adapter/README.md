# minti-runtime

The per-node AI runtime adapter. A small Go daemon that presents a stable,
backend-agnostic HTTP API and routes requests to whichever AI engine is
configured locally.

## What it is

A localhost HTTP service exposing three API shapes simultaneously:

| Shape | Endpoints | Notes |
|---|---|---|
| OpenAI-compatible | `POST /v1/chat/completions`, `GET /v1/models` | Streaming SSE supported |
| Ollama-compatible | `POST /api/chat`, `GET /api/tags` | NDJSON streaming supported |
| MINTI native | `GET /minti/health`, `GET /minti/capabilities`, `GET /minti/version` | Introspection only |

All three shapes go through the **same internal backend interface** — see
`internal/backend/backend.go`. That interface is what `cland` will consume
when it composes the Clan capability advertisement.

## Why it exists

The rest of the system (agent clients, `cland`, MCP servers) doesn't need to
care whether the local engine is Ollama, llama.cpp-server, LocalAI, or a
remote API bridge. It just talks to `http://127.0.0.1:7780` and gets a
stable wire format.

This also means **switching backends is a config-file change**, not a code
change.

## Status

**M1 — Ollama backend functional. Stub backends declared but not implemented.**

| Backend | Status |
|---|---|
| `ollama` | ✅ Implemented (proxy + translation) |
| `llamacpp-server` | 🟡 Stub — interface present, calls return 501 |
| `localai` | 🟡 Stub |
| `remote-api` | 🟡 Stub — will land when remote-API key store does (M6) |

## Build

```bash
cd runtime-adapter
go mod tidy
go build -o minti-runtime ./cmd/minti-runtime
```

Cross-compile for Linux from anywhere:

```bash
GOOS=linux GOARCH=amd64 go build -o minti-runtime ./cmd/minti-runtime
```

## Run locally (dev)

```bash
# 1. Start Ollama somewhere (default port 11434)
ollama serve

# 2. Pull a model
ollama pull llama3.2:3b

# 3. Run the runtime adapter
./minti-runtime --port 7780

# 4. Hit it
curl -s http://127.0.0.1:7780/minti/health
curl -s http://127.0.0.1:7780/v1/models | jq

curl -sN http://127.0.0.1:7780/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{"model":"llama3.2:3b","messages":[{"role":"user","content":"hi"}]}'
```

## Run as a service (Linux)

```bash
sudo install -m 0755 minti-runtime /usr/local/bin/
sudo install -m 0644 systemd/minti-runtime.service /etc/systemd/system/
sudo install -m 0644 -D configs/runtime.yaml.example /etc/minti/runtime.yaml
sudo useradd --system --no-create-home --shell /usr/sbin/nologin minti || true
sudo install -d -o minti -g minti -m 0755 /var/lib/minti
sudo systemctl daemon-reload
sudo systemctl enable --now minti-runtime
```

The MINTI `install/install.sh` handles all of this automatically when the
binary is present in the install tree.

## Config

See [configs/runtime.yaml.example](configs/runtime.yaml.example). Defaults
are sensible — most users won't edit this file unless they're switching
backends or pointing at a non-default Ollama address.

## Endpoints in detail

### `GET /minti/health`
`200 OK` with `{"status":"ok"}` if the configured backend is reachable.
`503 Service Unavailable` with `{"error":...}` if not. Used by `cland` and
by Linux service supervisors.

### `GET /minti/capabilities`
Returns the `Capabilities` struct: backend kind, healthy bool, resident
models, streaming support, remote-API vendor (if any). `cland` queries
this every 30 seconds to keep its Clan advertisement accurate.

### `POST /v1/chat/completions`
OpenAI shape. Supports `stream: true` (SSE) and `stream: false` (single
JSON response). `model` and `messages` are required.

### `POST /api/chat`
Ollama shape. Defaults to `stream: true` like Ollama itself. NDJSON
streaming or single response per the request.

## Adding a backend

1. Add a new file under `internal/backend/` implementing the `Backend` interface.
2. Add the `Kind` constant in `backend.go`.
3. Wire it into `config.NewBackend()` in `internal/config/config.go`.
4. Document in `configs/runtime.yaml.example`.
5. Tests under `tests/runtime-adapter/<backend>/`.

## Layout

```
cmd/minti-runtime/main.go          — entry point, flag parsing, signal handling
internal/backend/backend.go        — Backend interface + shared types
internal/backend/ollama.go         — Ollama backend (M1)
internal/backend/stubs.go          — llamacpp/localai/remote-api stubs
internal/config/config.go          — YAML loader, Default(), NewBackend()
internal/server/server.go          — HTTP router + introspection endpoints
internal/server/chat.go            — chat endpoints (OpenAI + Ollama) + streaming
configs/runtime.yaml.example
systemd/minti-runtime.service
```
