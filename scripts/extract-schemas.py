#!/usr/bin/env python3
"""Extract the JSON Schemas from docs/spec.md into schemas/<$id>.json.

The spec is the source; never edit schemas/ by hand. Run this after any
change to docs/spec.md, and commit both together. `--check` fails when
schemas/ is out of date, for CI.

The spec writes one convention once instead of in every schema: records,
results and inputs are closed with unevaluatedProperties: false. This
script writes that convention into the files, with the spec's exceptions:
extra and envelope.result (objects with no listed properties), the
on-disk formats (open at every level), and fragments that others build
on through allOf, which are closed only where they are used.
"""
import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
SPEC = ROOT / "docs" / "spec.md"
OUT = ROOT / "schemas"
DIALECT = "https://json-schema.org/draft/2020-12/schema"

OPEN_SCHEMAS = {"archive-file", "marker-file"}  # on-disk formats
FRAGMENTS = {("task-list", "/$defs/filter")}    # used through allOf


def blocks(text):
    """Yield every JSON value in the spec's ```json blocks."""
    dec = json.JSONDecoder()
    for body in re.findall(r"```json\n(.*?)```", text, flags=re.S):
        i = 0
        while True:
            while i < len(body) and body[i].isspace():
                i += 1
            if i >= len(body):
                break
            value, i = dec.raw_decode(body, i)
            yield value


def close(node, sid, ptr=""):
    if isinstance(node, dict):
        if (node.get("type") == "object" and "properties" in node
                and sid not in OPEN_SCHEMAS and (sid, ptr) not in FRAGMENTS):
            node.setdefault("unevaluatedProperties", False)
        for key, child in node.items():
            close(child, sid, f"{ptr}/{key}")
    elif isinstance(node, list):
        for i, child in enumerate(node):
            close(child, sid, f"{ptr}/{i}")


def render(schema):
    sid = schema["$id"]
    out = {"$schema": DIALECT}
    out.update({k: v for k, v in schema.items() if k != "$schema"})
    close(out, sid)
    return json.dumps(out, indent=2, ensure_ascii=False) + "\n"


def main():
    check = "--check" in sys.argv[1:]
    schemas = {}
    for value in blocks(SPEC.read_text(encoding="utf-8")):
        if isinstance(value, dict) and "$id" in value:
            if value["$id"] in schemas:
                sys.exit(f"duplicate $id in spec: {value['$id']}")
            schemas[value["$id"]] = render(value)

    stale = []
    if not check:
        OUT.mkdir(exist_ok=True)
    existing = {p.stem for p in OUT.glob("*.json")} if OUT.exists() else set()
    for sid, text in sorted(schemas.items()):
        path = OUT / f"{sid}.json"
        if check:
            if not path.exists() or path.read_text(encoding="utf-8") != text:
                stale.append(path.name)
        else:
            path.write_text(text, encoding="utf-8")
    for gone in sorted(existing - set(schemas)):
        if check:
            stale.append(f"{gone}.json (no longer in the spec)")
        else:
            (OUT / f"{gone}.json").unlink()

    if check and stale:
        sys.exit("schemas/ is out of date with docs/spec.md:\n  " + "\n  ".join(stale))
    print(f"{len(schemas)} schemas {'checked' if check else 'written'}")


if __name__ == "__main__":
    main()
