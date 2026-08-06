#!/usr/bin/env python3
"""Fetch every reachable GET and check the real responses against the spec's `required`.

The static check proves the spec agrees with the Go structs. This one proves the Go structs are
what actually goes out — a projection that builds a DTO by hand, a handler that writes a different
shape, a middleware that rewrites one. A spec-required property missing from a live body, or a
`null` under a required-and-not-nullable property, is a contract the client would be generated
against and then break on.

Ids for the parameterised routes are harvested from the collection reads, so the deep routes are
exercised without a fixture.
"""

import argparse
import json
import re
import sys
import urllib.error
import urllib.request

BASE = "https://shopnexus.hopto.org/api/v1"

# Required query parameters the spec declares with no default. Enumerating them by hand is the
# only part of this that is not read off the document; each is a route the audit would otherwise
# get a 400 for and skip.
REQUIRED_QUERY = {
    "role": ["buyer", "seller"],
    "category": ["payment", "transport"],
    "currency": ["VND"],
}


def request(method, path, token=None, body=None):
    url = path if path.startswith("http") else BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/json")
    if data:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.status, json.loads(resp.read() or b"null")
    except urllib.error.HTTPError as exc:
        raw = exc.read()
        try:
            return exc.code, json.loads(raw or b"null")
        except json.JSONDecodeError:
            return exc.code, {"_raw": raw[:200].decode("utf-8", "replace")}
    except Exception as exc:  # noqa: BLE001 — a network failure is a result, not a crash
        return 0, {"_error": str(exc)}


def login(identifier, password):
    status, body = request("POST", "/login", body={"identifier": identifier, "password": password})
    if status != 200:
        raise SystemExit(f"login failed: {status} {body}")
    return body["data"]["access_token"]


OPAQUE = re.compile(r"^([a-z]{2,4})_[0-9a-hjkmnp-tv-z]{6,}$")


def harvest(node, into):
    """Collect every opaque id anywhere in a body, keyed by its prefix. A path parameter's schema
    declares the prefix in its `pattern`, so an id found anywhere can fill any route that wants
    that kind — no fixture and no per-route wiring."""
    if isinstance(node, dict):
        for value in node.values():
            harvest(value, into)
    elif isinstance(node, list):
        for item in node:
            harvest(item, into)
    elif isinstance(node, str):
        m = OPAQUE.match(node)
        if m:
            into.setdefault(m.group(1), [])
            if node not in into[m.group(1)]:
                into[m.group(1)].append(node)


# ------------------------------------------------------------------ spec walking ---

class Spec:
    def __init__(self, doc):
        self.doc = doc
        self.schemas = doc["components"]["schemas"]

    def deref(self, node):
        seen = 0
        while isinstance(node, dict) and "$ref" in node and seen < 20:
            node = self.resolve(node["$ref"])
            seen += 1
        return node

    def resolve(self, pointer):
        node = self.doc
        for part in pointer.lstrip("#/").split("/"):
            node = node[part]
        return node

    def merged(self, node):
        """Flatten one schema's `allOf` so `required`, `properties` and `nullable` are readable."""
        node = self.deref(node) or {}
        out = {"properties": {}, "required": [], "nullable": bool(node.get("nullable")),
               "type": node.get("type"), "items": node.get("items"),
               "additionalProperties": node.get("additionalProperties")}
        for sub in node.get("allOf", []):
            m = self.merged(sub)
            out["properties"].update(m["properties"])
            out["required"].extend(m["required"])
            out["nullable"] = out["nullable"] or m["nullable"]
            out["type"] = out["type"] or m["type"]
            out["items"] = out["items"] or m["items"]
        out["properties"].update(node.get("properties") or {})
        out["required"].extend(node.get("required") or [])
        return out

    def get_params(self, template, method):
        """Path-item and operation parameters together, each dereferenced."""
        item = self.doc["paths"][template]
        raw = list(item.get("parameters") or []) + list((item.get(method) or {}).get("parameters") or [])
        return [self.deref(p) for p in raw]

    def param_prefix(self, params, name):
        """The opaque-id prefix a path parameter accepts, read off its schema's `pattern`."""
        for p in params:
            if p.get("name") != name or p.get("in") != "path":
                continue
            schema = self.deref(p.get("schema")) or {}
            m = re.match(r"\^([a-z]{2,4})_", schema.get("pattern") or "")
            if m:
                return m.group(1)
        return None

    def response_schema(self, path_template, query):
        item = self.doc["paths"].get(path_template)
        if not item:
            return None
        op = item.get("get")
        if not op:
            return None
        ok = (op.get("responses") or {}).get("200") or {}
        content = (ok.get("content") or {}).get("application/json") or {}
        return content.get("schema")

    def check(self, schema, value, where, out):
        """Walk a live value against a schema, recording every required/nullable violation."""
        node = self.deref(schema)
        if node is None:
            return
        m = self.merged(node)
        if value is None:
            if not m["nullable"]:
                out.append(f"{where}: null but not nullable")
            return
        if m["properties"] or m["required"]:
            if not isinstance(value, dict):
                out.append(f"{where}: expected object, got {type(value).__name__}")
                return
            for prop in set(m["required"]):
                if prop not in value:
                    out.append(f"{where}.{prop}: required but missing")
            for prop, sub in m["properties"].items():
                if prop in value:
                    self.check(sub, value[prop], f"{where}.{prop}", out)
            return
        if m["type"] == "array" and m["items"] is not None:
            if not isinstance(value, list):
                out.append(f"{where}: expected array, got {type(value).__name__}")
                return
            for i, item in enumerate(value[:40]):
                self.check(m["items"], item, f"{where}[{i}]", out)


PARAM = re.compile(r"\{([A-Za-z0-9_]+)\}")


def template_for(spec, path):
    """Match a concrete path back to its spec path template, literal segments winning: `/orders/
    summary` is its own operation and would otherwise be checked against `/orders/{id}`."""
    concrete = path.split("?")[0].strip("/").split("/")
    best, best_score = None, -1
    for template in spec.doc["paths"]:
        parts = template.strip("/").split("/")
        if len(parts) != len(concrete):
            continue
        if not all(PARAM.fullmatch(p) or p == c for p, c in zip(parts, concrete)):
            continue
        score = sum(1 for p in parts if not PARAM.fullmatch(p))
        if score > best_score:
            best, best_score = template, score
    return best


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--spec", default="../../api/openapi.gen.yaml")
    ap.add_argument("--identifier", default="bob@shopnexus.test")
    ap.add_argument("--password", default="Bob@12345")
    ap.add_argument("--verbose", action="store_true")
    args = ap.parse_args()

    import yaml
    with open(args.spec, encoding="utf-8") as fh:
        spec = Spec(yaml.safe_load(fh))

    token = login(args.identifier, args.password)
    print(f"logged in, token {len(token)} chars", file=sys.stderr)

    results, violations, unmatched, nonok = [], [], [], []
    ids = {}

    def fetch(path):
        status, body = request("GET", path, token)
        results.append((path, status))
        if status != 200:
            nonok.append(f"{path} -> {status} {json.dumps(body)[:110]}")
            return None
        harvest(body, ids)
        template = template_for(spec, path)
        if not template:
            unmatched.append(path)
            return body
        schema = spec.response_schema(template, path)
        if schema is None:
            unmatched.append(f"{path} (no 200 json schema on {template})")
            return body
        found = []
        spec.check(schema, body, path, found)
        violations.extend(found)
        if args.verbose:
            print(f"  {status} {path} ({len(found)} violations)", file=sys.stderr)
        return body

    def variants(template):
        """Every concrete URL for one spec path: path params filled from harvested ids, required
        query params expanded over their known values."""
        params = spec.get_params(template, "get")
        urls = [template]
        for name in PARAM.findall(template):
            prefix = spec.param_prefix(params, name)
            pool = ids.get(prefix, [])[:2] if prefix else []
            if not pool:
                return []
            urls = [u.replace("{" + name + "}", v) for u in urls for v in pool]
        query = [p for p in params
                 if p.get("in") == "query" and p.get("required")
                 and p["name"] in REQUIRED_QUERY]
        for p in query:
            urls = [f"{u}{'&' if '?' in u else '?'}{p['name']}={v}"
                    for u in urls for v in REQUIRED_QUERY[p["name"]]]
        missing = [p["name"] for p in params
                   if p.get("in") == "query" and p.get("required")
                   and p["name"] not in REQUIRED_QUERY]
        if missing:
            nonok.append(f"{template} -> skipped, required query params unknown: {missing}")
            return []
        return urls

    gets = [t for t, item in spec.doc["paths"].items() if "get" in item]
    plain = [t for t in gets if not PARAM.search(t)]
    parameterised = [t for t in gets if PARAM.search(t)]

    # Two passes over the parameterised routes: the first fills from ids the plain collections
    # gave up, the second from ids only a detail read returns (a message's, a transaction's).
    for template in sorted(plain):
        for url in variants(template):
            fetch(url)
    for _ in range(2):
        for template in sorted(parameterised):
            for url in variants(template):
                if url not in [p for p, _ in results]:
                    fetch(url)

    print(f"\nid kinds harvested: {len(ids)} ({', '.join(sorted(ids))})")
    print(f"endpoints fetched: {len(results)}  200s: {sum(1 for _, s in results if s == 200)}")
    print(f"non-200: {len(nonok)}")
    for x in nonok:
        print("  ", x)
    print(f"unmatched to a spec schema: {len(unmatched)}")
    for x in unmatched:
        print("  ", x)
    print(f"\nVIOLATIONS: {len(violations)}")
    for x in sorted(set(violations)):
        print("  ", x)
    return 1 if violations else 0


if __name__ == "__main__":
    sys.exit(main())
