#!/usr/bin/env python3
"""Audit (and optionally tighten) `required` on the response half of the OpenAPI contract.

Why this exists: the DTO rule is that a wire shape never omits a zero value
(`TestDTOs_NeverOmitAZeroValue`), so every response property is always present. The spec has to
say so, or a generated client types all of them as optional and every caller writes `?? []`.

Three things make this delicate, and all three are checks here rather than notes:

  1. A *request* schema must never gain `required` — the DTO rule is about marshalling and the
     server never marshals a request, so requiring a PATCH's fields would break every partial
     update. Schemas are partitioned by direction (transitive `$ref` closure from `requestBody` +
     `parameters` versus from `responses`) and only response-only ones are touched.
  2. `required` is not non-null. A Go pointer field is always *present* and may be `null`, so it
     gets `nullable: true` at the same time.
  3. A property the spec declares but no Go field backs is a phantom: it is never sent, so
     requiring it would make the spec lie. The candidate set is therefore the intersection
     `spec properties ∩ Go json names`, never the spec's own property list — and `--verify`
     *fails* on a phantom rather than skipping it, because the honest fix is to delete the
     property, not to leave it optional.

Read-only by default; `--apply` edits the fragments as text (never a yaml round-trip, which would
delete the fragments' load-bearing comments).
"""

import argparse
import glob
import json
import os
import re
import sys

import yaml

import gofields

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))

# Empty, and `--verify` fails on anything that would go in it: a property the spec declares
# and no Go field backs is never sent, so a generated client types a field that is always
# null. This started as an allowlist holding `PaymentSession.from_id`/`to_id` and
# `WalletTransaction.group_id` — all three found by this tool, silenced instead of removed,
# and still in the spec months later. An allowlist entry is a bug with a note attached.
PHANTOM: set[tuple[str, str]] = set()

# Spec schema name -> (module, Go DTO struct name), where the schema is named for the aggregate
# and the Go type for its place inside the module's package.
ALIASES = {
    "Resource": ("common", "ResourceDTO"),
    "Option": ("common", "OptionDTO"),
    "PaymentSession": ("finance", "Session"),
    "PaymentSessionPage": ("finance", "SessionPage"),
    "WalletTransaction": ("finance", "WalletMovement"),
    "WalletTransactionPage": ("finance", "WalletMovementPage"),
    "DraftOrder": ("order", "Draft"),
    "DraftOrderPage": ("order", "DraftPage"),
    "DraftVariantSnapshot": ("order", "DraftVariant"),
    "OrderItem": ("order", "Item"),
    "OrderItemPage": ("order", "ItemPage"),
    "OrderAddressSnapshot": ("order", "AddressSnapshot"),
    "ReviewVoteTally": ("trust", "VoteTally"),
    "ChatUnreadCount": ("chat", "UnreadCount"),
    "PageMeta": ("catalog", "PageInfo"),
}

# Response shapes with no DTO struct behind them: the gateway's own envelopes (`shared/httpx`)
# and the pure wrappers a handler builds around a slice. They already require every property they
# declare, and the static check leaves them alone rather than guessing at a Go type.
NO_DTO = {"CursorMeta", "Error", "ErrorField", "WebSocketTicket"}


def load_spec():
    with open(os.path.join(ROOT, "api/openapi.gen.yaml"), encoding="utf-8") as fh:
        return yaml.safe_load(fh)


def refs(node):
    """Every `#/components/...` pointer anywhere under a node."""
    found = []
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "$ref" and isinstance(value, str):
                found.append(value)
            else:
                found.extend(refs(value))
    elif isinstance(node, list):
        for item in node:
            found.extend(refs(item))
    return found


def resolve(spec, pointer):
    node = spec
    for part in pointer.lstrip("#/").split("/"):
        node = node[part]
    return node


def closure(spec, seeds):
    """Transitive closure over `$ref`, answering the set of component *schema* names reached."""
    pending, seen, schemas = list(seeds), set(), set()
    while pending:
        pointer = pending.pop()
        if pointer in seen:
            continue
        seen.add(pointer)
        if pointer.startswith("#/components/schemas/"):
            schemas.add(pointer.rsplit("/", 1)[1])
        pending.extend(refs(resolve(spec, pointer)))
    return schemas


def partition(spec):
    """Split component schemas by the direction they are reachable from."""
    methods = {"get", "put", "post", "patch", "delete", "head", "options"}
    req_seeds, resp_seeds = [], []
    for path_item in spec.get("paths", {}).values():
        shared = path_item.get("parameters", [])
        for name, op in path_item.items():
            if name not in methods:
                continue
            inbound = [op.get("requestBody"), op.get("parameters"), shared]
            req_seeds.extend(refs(inbound))
            resp_seeds.extend(refs(op.get("responses")))
    return closure(spec, req_seeds), closure(spec, resp_seeds)


def go_struct(by_module, name):
    """The Go DTO behind one spec schema, resolved through the module that declares the fragment."""
    if name in NO_DTO:
        return None
    if name in ALIASES:
        module, go_name = ALIASES[name]
    else:
        home = SCHEMA_HOME.get(name)
        module, go_name = (home[1] if home else None), name
    return gofields.lookup(by_module, go_name, module)


def plan(spec, by_module):
    """What to change, per schema. Also answers the drift the walk noticed."""
    request_side, response_side = partition(spec)
    schemas = spec["components"]["schemas"]

    both = sorted(request_side & response_side)
    ambiguous = [n for n in both if "properties" in (schemas.get(n) or {})]

    items, notes = [], {
        "request_reachable": len(request_side),
        "response_reachable": len(response_side),
        "both": len(both),
        "ambiguous_objects": ambiguous,
        "unmatched": [],
        "phantoms": [],
        "skipped_ids": [],
        "required_ids": [],
    }

    for name in sorted(response_side - request_side):
        schema = schemas.get(name) or {}
        props = schema.get("properties")
        if not props:
            continue
        go = go_struct(by_module, name)
        if go is None:
            missing = [p for p in props if p not in (schema.get("required") or [])]
            if missing:
                notes["unmatched"].append(f"{name} (props not required: {missing})")
            continue
        go_kinds = dict(go["fields"])
        existing = list(schema.get("required") or [])

        add_required, add_nullable = [], []
        for prop in props:
            if prop in existing:
                continue
            if prop not in go_kinds:
                notes["phantoms"].append(f"{name}.{prop}")
                continue
            if (name, prop) in PHANTOM:
                notes["phantoms"].append(f"{name}.{prop} (known)")
                continue
            k = gofields.kind(go_kinds[prop])
            if k == "id":
                notes["skipped_ids"].append(f"{name}.{prop} ({go_kinds[prop]})")
                continue
            if k == "pointer":
                add_nullable.append(prop)
            add_required.append(prop)

        # A pointer already in `required` still needs `nullable`, or the client types it
        # non-nullable and throws on the null the server does send.
        for prop in existing:
            if prop in props and prop in go_kinds and gofields.kind(go_kinds[prop]) == "pointer":
                if not (schemas[name]["properties"][prop] or {}).get("nullable"):
                    if prop not in add_nullable:
                        add_nullable.append(prop)
            # An opaque id already required is only safe if the zero id cannot reach the
            # client — it marshals `null`. Reported, never changed: whether zero is reachable
            # is a per-field fact this tool cannot read.
            if prop in props and prop in go_kinds and gofields.kind(go_kinds[prop]) == "id":
                node = props[prop] or {}
                nullable = node.get("nullable") or any(
                    isinstance(x, dict) and x.get("nullable") for x in node.get("allOf", []))
                notes["required_ids"].append(
                    f"{name}.{prop} ({go_kinds[prop]}, nullable={bool(nullable)})")

        if add_required or add_nullable:
            items.append({
                "schema": name,
                "existing_required": existing,
                "all_props": list(props),
                "add_required": add_required,
                "add_nullable": add_nullable,
            })
    return items, notes


# ---------------------------------------------------------------- text editing ---

FRAGMENTS = sorted(glob.glob(os.path.join(ROOT, "internal/module/*/api/openapi/*.yaml"))) + [
    os.path.join(ROOT, "api/openapi.base.yaml")
]

SCHEMA_KEY = re.compile(r"^    ([A-Za-z0-9_]+):\s*$")


def schema_home():
    """{schema name: (fragment path, owning module)} — the module is what disambiguates a Go type
    name that two packages both use."""
    home = {}
    for path in FRAGMENTS:
        parts = path.split(os.sep)
        module = parts[-4] if "module" in parts else None
        inside = False
        with open(path, encoding="utf-8") as fh:
            for line in fh:
                line = line.rstrip("\n")
                # base.yaml also keys `parameters` and `responses` at this indent, so the
                # section matters: only `components.schemas` names a schema.
                if re.match(r"^  [A-Za-z]", line):
                    inside = line.strip() == "schemas:"
                    continue
                m = SCHEMA_KEY.match(line)
                if inside and m:
                    home.setdefault(m.group(1), (path, module))
    return home


SCHEMA_HOME = schema_home()


def find_schema_block(lines, name):
    """Line range of one component schema, matched at the 4-space indent under components/schemas."""
    head = f"    {name}:"
    for i, line in enumerate(lines):
        if line.rstrip() != head:
            continue
        j = i + 1
        while j < len(lines):
            nxt = lines[j]
            if nxt.strip() and not nxt.startswith("      ") and not nxt.startswith("#"):
                break
            j += 1
        return i, j
    return None


def locate(name):
    home = SCHEMA_HOME.get(name)
    if not home:
        return None, None, None
    path = home[0]
    with open(path, encoding="utf-8") as fh:
        lines = fh.read().splitlines()
    span = find_schema_block(lines, name)
    return (path, lines, span) if span else (None, None, None)


PROP_KEY = re.compile(r"^        ([A-Za-z0-9_]+):")


def props_span(lines, start, end):
    """Line range of the schema's own `properties:` mapping."""
    for i in range(start, end):
        if lines[i].rstrip() == "      properties:":
            j = i + 1
            while j < end and (not lines[j].strip() or lines[j].startswith("        ")):
                j += 1
            return i, j
    return None


def prop_order(lines, pstart, pend):
    """The property names in the order the fragment writes them — the merged spec sorts its keys,
    and a `required` list in alphabetical order makes the fragment harder to read than it was."""
    return [m.group(1) for line in lines[pstart + 1 : pend] if (m := PROP_KEY.match(line))]


def apply_nullable(lines, pstart, pend, prop, report):
    """Add `nullable: true` to one property, keeping its comments and its written form."""
    head = re.compile(rf"^        {re.escape(prop)}:(.*)$")
    for i in range(pstart + 1, pend):
        m = head.match(lines[i])
        if not m:
            continue
        rest = m.group(1).strip()
        if rest.startswith("{") and rest.endswith("}"):
            body = rest[1:-1].strip().rstrip(",")
            if "nullable" in body:
                return True
            # Siblings of `$ref` are ignored in OpenAPI 3.0, so a nullable ref has to be
            # wrapped — otherwise the generated type stays non-nullable and throws on null.
            if body.startswith("$ref:"):
                lines[i] = f"        {prop}: {{ allOf: [{{ {body} }}], nullable: true }}"
            else:
                lines[i] = f"        {prop}: {{ {body}, nullable: true }}"
            return True
        if rest == "" or rest.startswith("#"):
            # Block form: insert as the first child key. The block ends at the next property,
            # not at the end of `properties` — scanning further would find a *sibling's*
            # `nullable` and conclude there was nothing to do.
            own_end = i + 1
            while own_end < pend and (not lines[own_end].strip()
                                      or lines[own_end].startswith("          ")):
                own_end += 1
            j = i + 1
            while j < own_end and not lines[j].strip():
                j += 1
            if j >= own_end:
                report.append(f"{prop}: empty block form")
                return False
            child = re.match(r"^(\s+)", lines[j]).group(1)
            block = lines[i + 1 : own_end]
            if any(re.match(rf"^{child}nullable:", b) for b in block):
                return True
            if any(re.match(rf"^{child}\$ref:", b) for b in block):
                report.append(f"{prop}: block-form $ref, needs allOf by hand")
                return False
            lines.insert(j, f"{child}nullable: true")
            return True
        report.append(f"{prop}: unrecognised property form: {rest[:40]!r}")
        return False
    report.append(f"{prop}: property line not found")
    return False


def apply_required(lines, start, end, wanted, report):
    """Rewrite (or add) the schema's own `required:` list to exactly `wanted`."""
    rendered = "[" + ", ".join(wanted) + "]"
    for i in range(start, end):
        line = lines[i]
        if not line.startswith("      required:"):
            continue
        rest = line[len("      required:") :].strip()
        if rest.startswith("[") and rest.endswith("]"):
            lines[i] = f"      required: {rendered}"
            return True
        report.append(f"required: not a flow list: {rest[:40]!r}")
        return False
    # No `required:` yet — put it at the end of the schema, which is where every fragment
    # that has one keeps it.
    j = end
    while j > start and not lines[j - 1].strip():
        j -= 1
    lines.insert(j, f"      required: {rendered}")
    return True


def apply_plan(items, dry_run):
    applied, skipped = [], []
    for item in items:
        name = item["schema"]
        path, lines, span = locate(name)
        if not path:
            skipped.append(f"{name}: schema not found in any fragment")
            continue
        start, end = span
        report = []
        pspan = props_span(lines, start, end)
        if not pspan and item["add_nullable"]:
            skipped.append(f"{name}: no properties block found")
            continue

        order = prop_order(lines, *pspan) if pspan else item["all_props"]
        missing_order = [p for p in item["all_props"] if p not in order]
        if missing_order:
            report.append(f"properties not found in fragment text: {missing_order}")
            order = order + missing_order

        for prop in item["add_nullable"]:
            apply_nullable(lines, pspan[0], pspan[1], prop, report)

        # Recompute the span: inserting nullable lines moved the tail.
        start, end = find_schema_block(lines, name)
        wanted = [p for p in order
                  if p in item["existing_required"] or p in item["add_required"]]
        apply_required(lines, start, end, wanted, report)

        if report:
            skipped.extend(f"{name}.{r}" for r in report)
        applied.append({"schema": name, "file": os.path.relpath(path, ROOT),
                        "required": wanted, "nullable": item["add_nullable"]})
        if not dry_run:
            with open(path, "w", encoding="utf-8") as fh:
                fh.write("\n".join(lines) + "\n")
    return applied, skipped


def verify(spec, by_module):
    """Static cross-check: every response-only schema's `required` == spec props ∩ Go fields."""
    request_side, response_side = partition(spec)
    schemas = spec["components"]["schemas"]
    bad = []
    for name in sorted(response_side - request_side):
        schema = schemas.get(name) or {}
        props = schema.get("properties")
        if not props:
            continue
        go = go_struct(by_module, name)
        if go is None:
            # No DTO behind it: a gateway envelope or a slice wrapper, which is always sent
            # whole, so every property it declares has to be required.
            missing = [p for p in props if p not in (schema.get("required") or [])]
            if missing:
                bad.append({"schema": name, "envelope_props_not_required": missing})
            continue
        go_kinds = dict(go["fields"])
        backed = {p for p in props if p in go_kinds and (name, p) not in PHANTOM}
        # A declared property nothing populates is a contract the server does not keep, so it
        # is a failure here rather than something `backed` quietly drops.
        phantoms = sorted(p for p in props if p not in go_kinds)
        if phantoms:
            bad.append({"schema": name, "declared_but_never_sent": phantoms})
        # A bare opaque id marshals `null` at its zero, so whether it may be required is a
        # per-field fact: allowed either way here, and listed by the plan for a human to decide.
        demand = {p for p in backed if gofields.kind(go_kinds[p]) != "id"}
        have = set(schema.get("required") or [])
        missing, extra = sorted(demand - have), sorted(have - backed)
        if missing or extra:
            bad.append({"schema": name, "missing": missing, "extra": extra})
        for p in props:
            if p not in go_kinds:
                continue
            node = props[p] or {}
            nullable = node.get("nullable") or any(
                isinstance(x, dict) and x.get("nullable") for x in node.get("allOf", []))
            k = gofields.kind(go_kinds[p])
            if k == "pointer" and p in have and not nullable:
                bad.append({"schema": name, "pointer_not_nullable": p})
            # The other direction costs the client just as much: a field that cannot marshal
            # null generates as nullable and every caller writes a coalesce for a case the
            # server never produces.
            if k == "value" and nullable:
                bad.append({"schema": name, "value_declared_nullable": p})
    return bad


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true", help="write the fragments")
    ap.add_argument("--verify", action="store_true", help="cross-check the generated spec only")
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args()

    spec = load_spec()
    by_module = gofields.index(ROOT)

    if args.verify:
        bad = verify(spec, by_module)
        print(json.dumps(bad, indent=2) if args.json else
              (f"{len(bad)} mismatches" + ("" if not bad else "\n" + json.dumps(bad, indent=2))))
        return 1 if bad else 0

    items, notes = plan(spec, by_module)
    applied, skipped = apply_plan(items, dry_run=not args.apply)

    out = {
        "counts": {
            "request_reachable": notes["request_reachable"],
            "response_reachable": notes["response_reachable"],
            "both": notes["both"],
            "ambiguous_objects": len(notes["ambiguous_objects"]),
            "schemas_changed": len(applied),
            # A pointer already in `required` needs `nullable` and no new requirement, so the
            # three counts do not add up to two — they are reported apart on purpose.
            "fields_newly_required": sum(len(i["add_required"]) for i in items),
            "fields_newly_required_value": sum(
                len(set(i["add_required"]) - set(i["add_nullable"])) for i in items),
            "fields_newly_required_pointer": sum(
                len(set(i["add_required"]) & set(i["add_nullable"])) for i in items),
            "fields_nullable_added": sum(len(i["add_nullable"]) for i in items),
            "fields_nullable_on_already_required": sum(
                len(set(i["add_nullable"]) - set(i["add_required"])) for i in items),
            "ids_skipped": len(notes["skipped_ids"]),
        },
        "ambiguous_objects": notes["ambiguous_objects"],
        "unmatched_schemas": notes["unmatched"],
        "phantoms": notes["phantoms"],
        "skipped_ids": notes["skipped_ids"],
        "required_ids": notes["required_ids"],
        "skipped_patterns": skipped,
        "applied": applied,
    }
    print(json.dumps(out, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
