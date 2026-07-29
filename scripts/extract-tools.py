#!/usr/bin/env python3
"""Extract an MCP tool surface as an agent would actually receive it.

Built for the #79 tool-description A/B. The first version pulled only the
`description` strings and dropped `inputSchema` — so the agents under test had
to GUESS parameter names that a real MCP client is simply handed. Both arms
burned calls probing `source`, which inflated the call-count metric and told us
nothing about the variable under test.

The rule this encodes: an evaluation harness must present the surface a real
client sees. Anything it strips becomes a fake difficulty that competes with the
independent variable.

Usage:  extract-tools.py <mcp.go> <tool,tool,...>
"""
import re, sys, json

def tools(path):
    src = open(path).read()
    out = {}
    # description
    for m in re.finditer(r'"name":\s*"(\w+)",\s*\n\s*"description":\s*"((?:[^"\\]|\\.)*)"', src):
        # Unescape ONLY the Go string escapes, never unicode_escape: the
        # descriptions contain real UTF-8 (em dashes, arrows) and
        # unicode_escape mangles them into mojibake.
        d = m.group(2).replace('\\"', '"').replace("\\n", "\n").replace("\\\\", "\\")
        out[m.group(1)] = {"description": d}
    # required parameter names, from the obj(...) schema builder
    for m in re.finditer(r'"name":\s*"(\w+)",.*?"inputSchema":\s*obj\((.*?)\),\n', src, re.S):
        name, body = m.group(1), m.group(2)
        params = re.findall(r'"(\w+)":\s*(?:str\("([^"]*)"\)|map\[string\]any)', body)
        req = re.findall(r'\},\s*((?:"\w+",?\s*)+)$', body.strip())
        if name in out:
            out[name]["params"] = [{"name": p, "description": d} for p, d in params]
            out[name]["required"] = re.findall(r'"(\w+)"', req[0]) if req else []
    return out

def main():
    path, keep = sys.argv[1], sys.argv[2].split(",")
    t = tools(path)
    blocks = []
    for k in keep:
        if k not in t:
            continue
        e = t[k]
        b = f"- {k}: {e['description']}"
        if e.get("params"):
            args = ", ".join(
                f'"{p["name"]}" ({p["description"]})'
                + ("  [REQUIRED]" if p["name"] in e.get("required", []) else "")
                for p in e["params"]
            )
            b += f"\n  arguments: {args if args else 'none'}"
        else:
            b += "\n  arguments: none"
        blocks.append(b)
    print("\n\n".join(blocks))

main()
