# Hermes 3 8B — Agent Skill

This file is read by MINTI agents to learn which chat model is available and
when to prefer it. The model itself is pulled by `minti-pack-fetch hermes3`
and served by Ollama via `minti-runtime`.

## Model

- **Tag:** `hermes3:8b`
- **Base:** Llama 3.1 8B, fine-tuned by Nous Research for agent / tool-use
  workflows. Strong on function-calling, structured output, and following
  multi-step plans.
- **Disk:** ~4.7 GB (Q4_K_M GGUF)
- **RAM at load:** ~5 GB
- **Context window:** 128k tokens (matches Llama 3.1 base)
- **Licence:** Llama 3 Community License — permissive with acceptable-use
  restrictions. Read upstream before commercial deployment.

## When to prefer Hermes 3 over alternatives

Use Hermes 3 when the task involves:
- MCP tool invocation that needs reliable structured arguments
- Multi-turn agent loops where the model must decide "call this tool then
  that one"
- Following a detailed system prompt with multiple constraints
- Drafting code or YAML with strict structure

Prefer **Mistral 7B** (`mistral:7b`, install `minti-pack-mistral`) when:
- The task is chat-flavoured / free-form
- You want a second voice for diversity (judge-panel pattern)

Prefer **DeepSeek-R1 32B** (`deepseek-r1:32b`) when:
- The task is heavy reasoning and you have GPU headroom
- You want explicit `<think>...</think>` traces

## Sample invocation

```bash
curl -sN -X POST http://127.0.0.1:7780/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "hermes3:8b",
    "messages": [{"role":"user","content":"Hello"}],
    "stream": true
  }'
```

Or, from the dashboard, toggle **agent** in chat to let the node use this model
with tools. (With the optional `opencode` client, select **MINTI Runtime
(local) → Hermes 3 8B** from the model picker.)

## Default-model behaviour

If `minti-pack-hermes3` is installed and the caller omits `model` in a request
to `/v1/chat/completions`, `minti-runtime` defaults to `hermes3:8b` (then
`mistral:7b` if Hermes isn't pulled, then the first available Ollama model).
Set the `model` field explicitly to override.
