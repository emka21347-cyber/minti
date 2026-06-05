# Mistral 7B Instruct — Agent Skill

## Model

- **Tag:** `mistral:7b`
- **Upstream:** Mistral AI, Mistral 7B Instruct v0.3 — Apache 2.0 licence.
- **Disk:** ~4.1 GB (Q4_K_M GGUF)
- **RAM at load:** ~4.5 GB
- **Context window:** 32k tokens

## When to prefer Mistral 7B

Use Mistral 7B when the task is:
- Free-form chat / explanation / summarization (its training mix is less
  tool-oriented than Hermes 3, more conversational)
- A second voice for diversity in a judge-panel or multi-attempt setup —
  combine with `hermes3:8b` for two distinct perspectives
- A storage-constrained host (slightly smaller than Hermes 3 8B)

Prefer **Hermes 3 8B** (`hermes3:8b`, install `minti-pack-hermes3`) when:
- The task involves tool invocation with structured arguments
- Multi-step agent planning
- Code generation

Prefer **DeepSeek-R1 32B** when:
- Heavy reasoning and you have GPU headroom

## Sample invocation

```bash
curl -sN -X POST http://127.0.0.1:7780/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mistral:7b",
    "messages": [{"role":"user","content":"Hello"}],
    "stream": true
  }'
```

## Larger sibling

`mistral-nemo:12b` (~7.1 GB on disk, ~7.5 GB at load) is the next step up if
you have spare RAM and want a stronger 128k-context model. Future pack:
`minti-pack-mistral-nemo`.
