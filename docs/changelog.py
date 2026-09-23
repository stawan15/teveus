#!/usr/bin/env python3
"""Turn CHANGELOG.md into docs/changelog.json for the website.

    python3 docs/changelog.py

CHANGELOG.md is the only place to edit; run this after changing it (a test
fails if the two drift apart). The JSON looks like:

    {
      "latest": "0.3.0",
      "versions": [
        {"version": "0.3.0", "date": "2026-09-23", "url": "https://github.com/…",
         "sections": [{"title": "Added", "items": [{"text": "…", "items": [...]}]}]}
      ]
    }

"text" is Markdown (backticks, links). "Unreleased" appears first, with no
date, when there are changes not yet released; "latest" is the newest
released version.
"""
import json, os, re

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def parse(md):
    versions, links = [], {}
    version = section = None
    stack = []  # (indent, item) for nested bullets
    for line in md.splitlines():
        m = re.match(r"^\[([^\]]+)\]:\s*(\S+)", line)
        if m:
            links[m.group(1)] = m.group(2)
            continue
        m = re.match(r"^## \[([^\]]+)\](?:\s*-\s*(\d{4}-\d{2}-\d{2}))?", line)
        if m:
            version = {"version": m.group(1), "date": m.group(2), "url": None, "sections": []}
            versions.append(version)
            section, stack = None, []
            continue
        m = re.match(r"^### (.+)", line)
        if m and version is not None:
            section = {"title": m.group(1).strip(), "items": []}
            version["sections"].append(section)
            stack = []
            continue
        m = re.match(r"^(\s*)- (.+)", line)
        if m and section is not None:
            indent, item = len(m.group(1)), {"text": m.group(2).strip(), "items": []}
            while stack and stack[-1][0] >= indent:
                stack.pop()
            (stack[-1][1]["items"] if stack else section["items"]).append(item)
            stack.append((indent, item))
    for v in versions:
        v["url"] = links.get(v["version"])
    released = [v["version"] for v in versions if v["version"] != "Unreleased"]
    return {"latest": released[0] if released else None, "versions": versions}


def main():
    with open(os.path.join(ROOT, "CHANGELOG.md"), encoding="utf-8") as f:
        data = parse(f.read())
    out = os.path.join(ROOT, "docs", "changelog.json")
    with open(out, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write("\n")
    print(f"{out}: {len(data['versions'])} versions, latest {data['latest']}")


if __name__ == "__main__":
    main()
