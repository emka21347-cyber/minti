"""Tier-3 code generation driver: one prompt file -> one local-LLM completion.

The MINTI delegation workflow (memory/feedback_delegation_workflow.md) sends
tightly-specced single-file boilerplate to a local model and has Tier 2
review the result before it is integrated. This driver is the transport:

  python t3-codegen.py <prompt.txt> <output-file> [model] [num_predict]

Defaults: qwen3.6:latest with think:false (fastest + strongest on code per
memory/reference_local_llms.md), num_predict 16000. Strips <think> traces and
``` fences so the output file is the raw artifact.
"""
import json
import re
import sys
import time
import urllib.request

OLLAMA = "http://localhost:11434/api/chat"


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    prompt_path, out_path = sys.argv[1], sys.argv[2]
    model = sys.argv[3] if len(sys.argv) > 3 else "qwen3.6:latest"
    num_predict = int(sys.argv[4]) if len(sys.argv) > 4 else 16000

    with open(prompt_path, encoding="utf-8") as f:
        prompt = f.read()
    print(f"model={model} prompt_chars={len(prompt)}", flush=True)

    body = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "stream": False,
        "options": {"temperature": 0.2, "num_ctx": 24576, "num_predict": num_predict},
    }
    if model.startswith("qwen3"):
        body["think"] = False

    t0 = time.time()
    req = urllib.request.Request(
        OLLAMA, data=json.dumps(body).encode("utf-8"),
        headers={"Content-Type": "application/json"}, method="POST",
    )
    with urllib.request.urlopen(req, timeout=2400) as r:
        resp = json.loads(r.read().decode("utf-8"))
    dur = time.time() - t0

    raw = resp.get("message", {}).get("content", "")
    clean = re.sub(r"<think>.*?</think>\s*", "", raw, flags=re.DOTALL).strip()
    # Unwrap a single leading/trailing code fence if the model added one.
    m = re.match(r"^```[a-zA-Z]*\n(.*)\n```\s*$", clean, flags=re.DOTALL)
    if m:
        clean = m.group(1)

    with open(out_path, "w", encoding="utf-8", newline="\n") as o:
        o.write(clean)
        if not clean.endswith("\n"):
            o.write("\n")
    print(f"done in {dur:.1f}s; eval_tokens={resp.get('eval_count', 0)}; "
          f"wrote {len(clean)} chars to {out_path}", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
