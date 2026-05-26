"""Send the M4 plan + spec to each local LLM in turn; save each review.

Per memory/reference_local_llms.md: use Python (not PowerShell ConvertTo-Json)
because PS mangles JSON bodies with embedded backslashes/quotes. Models emit
<think> traces; set num_predict generously and strip those at the end.
"""
import json
import os
import re
import sys
import time
import urllib.request

OLLAMA = "http://localhost:11434/api/chat"
MODELS = [
    "qwen3.6:latest",      # strong code / technical
    "deepseek-r1:32b",     # explicit reasoning model
    "gemma4:31b",          # strong general-purpose
]

REPO = r"C:\Users\aouad\Documents\CCode\MINT\MINT_wip"
INPUT_PATH = os.path.join(REPO, "scripts", "m4-review-input.txt")
OUT_DIR = os.path.join(REPO, "scripts", "m4-reviews")
os.makedirs(OUT_DIR, exist_ok=True)


def strip_thinking(s: str) -> str:
    """Remove <think>...</think> blocks (deepseek, gemma, qwen all emit them)."""
    return re.sub(r"<think>.*?</think>\s*", "", s, flags=re.DOTALL)


def chat(model: str, prompt: str) -> dict:
    body = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "stream": False,
        "options": {
            "temperature": 0.3,
            "num_ctx": 16384,
            "num_predict": 12000,
        },
    }
    data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(
        OLLAMA, data=data, headers={"Content-Type": "application/json"}, method="POST"
    )
    with urllib.request.urlopen(req, timeout=1800) as r:
        return json.loads(r.read().decode("utf-8"))


def main() -> int:
    with open(INPUT_PATH, encoding="utf-8") as f:
        prompt = f.read()
    print(f"input chars={len(prompt)}, approx tokens={len(prompt)//4}", flush=True)

    summary = []
    for model in MODELS:
        safe = model.replace(":", "_").replace("/", "_")
        out_path = os.path.join(OUT_DIR, f"{safe}.md")
        print(f"\n--- {model} ---", flush=True)
        t0 = time.time()
        try:
            resp = chat(model, prompt)
        except Exception as exc:
            print(f"  ERROR: {exc}", flush=True)
            summary.append((model, "ERROR", 0, str(exc)))
            continue
        dur = time.time() - t0
        raw = resp.get("message", {}).get("content", "")
        clean = strip_thinking(raw).strip()
        eval_count = resp.get("eval_count", 0)
        prompt_eval = resp.get("prompt_eval_count", 0)

        with open(out_path, "w", encoding="utf-8") as o:
            o.write(f"# {model} — M4 plan review\n\n")
            o.write(f"- wall_time_s: {dur:.1f}\n- prompt_tokens: {prompt_eval}\n")
            o.write(f"- eval_tokens: {eval_count}\n- raw_chars: {len(raw)}\n")
            o.write(f"- clean_chars: {len(clean)}\n\n---\n\n")
            o.write(clean)
            o.write("\n\n---\n\n## Raw (with thinking trace)\n\n")
            o.write(raw)

        print(
            f"  done in {dur:.1f}s; eval_tokens={eval_count}; "
            f"clean_chars={len(clean)}; saved {out_path}",
            flush=True,
        )
        summary.append((model, "OK", dur, out_path))

    print("\n--- summary ---", flush=True)
    for m, status, dur, info in summary:
        print(f"  {m:<25} {status:<6} {dur:>5.0f}s  {info}", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
