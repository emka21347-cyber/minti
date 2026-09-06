#!/usr/bin/env bash
echo "--- end-to-end chat through minti-runtime → ollama → llama3.2:3b ---"
t0=$(date +%s)
curl -s http://127.0.0.1:7780/v1/chat/completions \
    -H 'Content-Type: application/json' \
    -d '{"model":"llama3.2:3b","messages":[{"role":"user","content":"Reply with only the word PONG"}],"max_tokens":20,"stream":false}'
echo
echo "elapsed: $(( $(date +%s) - t0 ))s"
