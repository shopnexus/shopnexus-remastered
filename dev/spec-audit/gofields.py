"""Parse the published `api` packages for their DTO wire shapes.

A tiny Go struct reader rather than `go/ast`: what is needed here is only the json tag and the
literal type expression of every field, and keeping the tool in one language keeps the audit
runnable without a build. Anything it cannot parse is reported rather than guessed at.
"""

import glob
import os
import re

# `Name Type `tag`` — the type is everything between the name and the tag, trimmed.
FIELD_RE = re.compile(r"^\s*([A-Z][A-Za-z0-9_]*)\s+(.+?)\s+`([^`]*)`\s*(?://.*)?$")
EMBED_RE = re.compile(r"^\s*([A-Za-z][A-Za-z0-9_.\[\]]*)\s*(?://.*)?$")
STRUCT_RE = re.compile(r"^type\s+([A-Za-z0-9_]+)\s+struct\s*\{\s*$")
JSON_RE = re.compile(r'json:"([^"]*)"')


def dto_files(root):
    """The same sources `TestDTOs_NeverOmitAZeroValue` walks, each with its module and whether
    only `*DTO` types count (a file that holds domain types beside its wire ones)."""
    out = []
    for path in sorted(glob.glob(os.path.join(root, "internal/module/*/api/*.go"))):
        if path.endswith("_test.go"):
            continue
        module = path.split(os.sep)[-3]
        out.append((path, module, False))
    out.append((os.path.join(root, "internal/module/common/optionapi.go"), "common", False))
    out.append((os.path.join(root, "internal/module/common/resource.go"), "common", True))
    return out


def parse_structs(path):
    """Answer {StructName: {"fields": [(json_name, go_type)], "embeds": [TypeName]}}."""
    out = {}
    with open(path, encoding="utf-8") as fh:
        lines = fh.read().splitlines()

    i = 0
    while i < len(lines):
        m = STRUCT_RE.match(lines[i])
        if not m:
            i += 1
            continue
        name = m.group(1)
        fields, embeds = [], []
        i += 1
        depth = 0
        while i < len(lines):
            line = lines[i]
            if line == "}" and depth == 0:
                break
            stripped = line.strip()
            # An inline struct/interface body would break the flat read; count braces so the
            # walker at least stays in sync instead of silently mis-attributing fields.
            depth += line.count("{") - line.count("}")
            fm = FIELD_RE.match(line)
            if fm:
                jt = JSON_RE.search(fm.group(3))
                if jt:
                    jname = jt.group(1).split(",")[0]
                    if jname and jname != "-":
                        fields.append((jname, fm.group(2).strip()))
                i += 1
                continue
            if stripped and not stripped.startswith("//"):
                em = EMBED_RE.match(line)
                if em and "(" not in stripped and "{" not in stripped:
                    embeds.append(em.group(1))
            i += 1
        out[name] = {"fields": fields, "embeds": embeds}
        i += 1
    return out


def index(root):
    """All DTO structs, keyed by module then name — the same name means different fields in two
    modules (`PageInfo`, `UnreadCount`), so a flat index would silently answer the wrong shape."""
    by_module = {}
    for path, module, only_dto in dto_files(root):
        for name, body in parse_structs(path).items():
            if only_dto and not name.endswith("DTO"):
                continue
            body["file"] = path
            body["module"] = module
            by_module.setdefault(module, {})[name] = body
    # Resolve embedded DTOs, preferring the same module and falling back to common.
    for module, structs in by_module.items():
        for body in structs.values():
            for emb in body["embeds"]:
                short = emb.split(".")[-1]
                src = structs.get(short) or by_module.get("common", {}).get(short)
                if src:
                    body["fields"] = src["fields"] + body["fields"]
    return by_module


def lookup(by_module, name, module=None):
    """Find one struct: the named module first, then common, then a unique match anywhere."""
    if module and name in by_module.get(module, {}):
        return by_module[module][name]
    if name in by_module.get("common", {}):
        return by_module["common"][name]
    hits = [s[name] for s in by_module.values() if name in s]
    return hits[0] if len(hits) == 1 else None


def kind(go_type):
    """How the field marshals: 'pointer' (can be null), 'id' (null at zero), or 'value'."""
    t = go_type.strip()
    if t.startswith("*"):
        return "pointer"
    # A bare opaque id, and only a bare one: `[]id.ID[…]` is a slice, so it marshals `[]` and
    # never null. json/v2 sends a nil slice as `[]` and a nil map as `{}` (probed, not assumed).
    if re.fullmatch(r"(?:[A-Za-z_][\w]*\.)?ID\[[^\]]*\]", t):
        return "id"
    return "value"
