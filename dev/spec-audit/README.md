# spec-audit

Two checks that the published OpenAPI contract matches what the gateway actually sends. Both are
about `required` and `nullable`, because those two are what a generated client turns into a
nullable or non-nullable type — and a property the server always sends, declared optional, costs
every caller a `?? []` for a case that never happens.

## audit_required.py — static

Partitions `components.schemas` by direction (transitive `$ref` closure from `requestBody` +
`parameters` versus from `responses`) and works only on the response-only half: a **request**
schema must never gain `required`, or every PATCH breaks. For each one it intersects the spec's
properties with the Go DTO's json field names, then classifies by Go type — a pointer gets
`nullable: true` alongside `required`, a slice/map/scalar gets `required` alone, and a bare
`id.ID[K]` is left for a human because it marshals `null` at its zero.

```bash
python3 audit_required.py            # plan, changes nothing
python3 audit_required.py --apply    # edit the fragments, then: go generate ./...
python3 audit_required.py --verify   # assert required == spec props ∩ Go fields; 0 or bust
```

Edits are textual. The fragments' comments are documentation, so a yaml round-trip is not an
option; anything not matching the expected flow-list / flow-map / block form is reported and
skipped rather than guessed at. `ALIASES` maps the handful of schemas whose Go type has a
different name, `NO_DTO` names the gateway's own envelopes, and `PHANTOM` records properties the
spec declares that no DTO backs.

## live_audit.py — dynamic

Logs in, walks every `GET` the spec declares — path parameters filled from opaque ids harvested
out of earlier responses, so the deep routes need no fixture — and checks each real body against
the spec: a required property missing, or `null` under one that is not `nullable`.

```bash
python3 live_audit.py                                   # against ../../api/openapi.gen.yaml
python3 live_audit.py --spec /tmp/other.yaml --verbose
python3 live_audit.py --identifier admin@… --password …  # the admin surface needs the role
```

The static check proves the spec agrees with the Go structs; only this one proves the structs are
what goes out. Its 401/403/404s are results too: a route the credential cannot reach is a route
that was not audited.
