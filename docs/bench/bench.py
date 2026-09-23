#!/usr/bin/env python3
"""Compare teveus with Claude Code and OpenCode on the same small tasks.

Each run gets a fresh copy of docs/demo-app, runs one tool non-interactively
with every tool call allowed, then checks the result with `go test`.

    python3 docs/bench/bench.py            # 3 repetitions
    REPS=1 python3 docs/bench/bench.py     # quicker

Writes docs/bench/results.jsonl (one line per run) and prints a summary.
Needs: claude (logged in), opencode (with a Google key), teveus built at
./teveus, and a Google key saved in teveus (/login).
"""
import json, os, shutil, subprocess, sys, tempfile, time

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
APP = os.path.join(ROOT, "docs", "demo-app")
TEVEUS = os.path.join(ROOT, "teveus")
OUT = os.path.join(ROOT, "docs", "bench", "results.jsonl")
REPS = int(os.environ.get("REPS", "3"))
GEMINI = "google/gemini-3.5-flash"
# Gemini 3.5 Flash, USD per million tokens: the rates OpenCode's own cost
# figures work out to, applied to both tools so they're priced alike.
PRICE = {"in": 0.50, "out": 3.00, "cache_read": 0.05}

TASKS = {
    "explain": "What does this project do, and is anything wrong with it? Answer briefly.",
    "fix": "The tests fail. Find the bug, fix it, and run the tests to confirm.",
    "feature": "Add a function Count(prices []float64) int that returns how many prices there are, with a test for it. Run the tests.",
}

def tools(task):
    return {
        "claude-code (haiku)": ["claude", "-p", task, "--model", "haiku", "--output-format", "json", "--permission-mode", "bypassPermissions"],
        "teveus (haiku)": [TEVEUS, "-p", task, "-engine", "claude", "-model", "haiku", "-json", "-mode", "bypassPermissions"],
        "opencode (gemini)": ["opencode", "run", "--format", "json", "-m", GEMINI, "--auto", task],
        "teveus (gemini)": [TEVEUS, "-p", task, "-engine", "api", "-model", GEMINI, "-json", "-mode", "auto"],
    }

def parse(tool, out):
    """Return input, output, cache_read, cache_write, cost, turns, answer."""
    if tool.startswith("claude-code"):
        j = json.loads(out)
        u = j.get("usage", {})
        return dict(input=u.get("input_tokens", 0), output=u.get("output_tokens", 0),
                    cache_read=u.get("cache_read_input_tokens", 0), cache_write=u.get("cache_creation_input_tokens", 0),
                    cost=j.get("total_cost_usd", 0), turns=j.get("num_turns", 0), answer=j.get("result", ""))
    if tool.startswith("opencode"):
        r = dict(input=0, output=0, cache_read=0, cache_write=0, cost=0.0, turns=0, answer="")
        for line in out.splitlines():
            try:
                ev = json.loads(line)
            except ValueError:
                continue
            p = ev.get("part", {})
            if ev.get("type") == "step_finish":
                t = p.get("tokens", {})
                r["input"] += t.get("input", 0)
                r["output"] += t.get("output", 0) + t.get("reasoning", 0)
                r["cache_read"] += t.get("cache", {}).get("read", 0)
                r["cache_write"] += t.get("cache", {}).get("write", 0)
                r["cost"] += p.get("cost", 0)
                r["turns"] += 1
            elif ev.get("type") == "text":
                r["answer"] = p.get("text", "")
        return r
    j = json.loads(out)
    r = dict(input=j["input_tokens"], output=j["output_tokens"], cache_read=j["cache_read_tokens"],
             cache_write=j["cache_write_tokens"], cost=j["cost_usd"], turns=j["num_turns"], answer=j["result"])
    if "gemini" in tool and not r["cost"]:
        r["cost"] = (r["input"] * PRICE["in"] + r["output"] * PRICE["out"] + r["cache_read"] * PRICE["cache_read"]) / 1e6
    return r

HIDDEN_COUNT_TEST = """package demo

import "testing"

func TestBenchHiddenCount(t *testing.T) {
	if got := Count([]float64{1, 2, 3}); got != 3 {
		t.Fatalf("Count = %d, want 3", got)
	}
	if got := Count(nil); got != 0 {
		t.Fatalf("Count(nil) = %d, want 0", got)
	}
}
"""

def check(task, d, answer):
    a = answer.lower()
    if task == "explain":
        # It must name the real bug: the discount is applied wrongly.
        return "discount" in a and any(w in a for w in ("bug", "wrong", "incorrect", "instead", "should", "1 -", "1-", "(1 -", "subtract"))
    if task == "fix":
        return subprocess.run(["go", "test", "./..."], cwd=d, capture_output=True).returncode == 0
    # feature: the original bug isn't part of the task, so run only a hidden
    # test of the new function (written after the tool has finished).
    with open(os.path.join(d, "bench_hidden_test.go"), "w") as f:
        f.write(HIDDEN_COUNT_TEST)
    return subprocess.run(["go", "test", "-run", "TestBenchHiddenCount", "./..."], cwd=d, capture_output=True).returncode == 0

def main():
    results = []
    with open(OUT, "w") as f:
        for rep in range(REPS):
            for task, prompt in TASKS.items():
                for tool, cmd in tools(prompt).items():
                    d = tempfile.mkdtemp(prefix="teveus-bench-")
                    shutil.copytree(APP, d, dirs_exist_ok=True)
                    start = time.time()
                    try:
                        p = subprocess.run(cmd, cwd=d, capture_output=True, text=True, timeout=600)
                        r = parse(tool, p.stdout)
                        r["ok"] = check(task, d, r["answer"])
                    except Exception as e:  # a crash or timeout counts as a failed run
                        r = dict(input=0, output=0, cache_read=0, cache_write=0, cost=0, turns=0, answer="", ok=False, error=str(e)[:300])
                    r.update(tool=tool, task=task, rep=rep, seconds=round(time.time() - start, 1))
                    answer = r.pop("answer")
                    r["answer_chars"], r["answer_start"] = len(answer), answer[:600]
                    results.append(r)
                    f.write(json.dumps(r) + "\n")
                    f.flush()
                    print(f"{rep} {task:8} {tool:22} ok={r['ok']!s:5} in={r['input']:>7} cached={r['cache_read']:>7} out={r['output']:>6} ${r['cost']:.4f} {r['seconds']}s", flush=True)
                    shutil.rmtree(d, ignore_errors=True)
    summarize(results)

def summarize(results):
    print("\n| tool | task | success | input tokens | cached | output tokens | cost | time |")
    print("|---|---|---|---|---|---|---|---|")
    keys = []
    for r in results:
        k = (r["tool"], r["task"])
        if k not in keys:
            keys.append(k)
    for tool, task in sorted(keys, key=lambda k: (k[1], k[0])):
        rs = [r for r in results if r["tool"] == tool and r["task"] == task]
        n = len(rs)
        avg = lambda k: sum(r[k] for r in rs) / n
        print(f"| {tool} | {task} | {sum(r['ok'] for r in rs)}/{n} | {avg('input'):,.0f} | {avg('cache_read'):,.0f} | {avg('output'):,.0f} | ${avg('cost'):.4f} | {avg('seconds'):.0f}s |")

if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "summary":
        summarize([json.loads(l) for l in open(OUT)])
    else:
        main()
