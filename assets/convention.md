# Code Conventions

## Module Structure

Every module under `internal/module/<name>/` follows the same vertical-slice layout:

```text
biz/
  interface.go      # XxxBiz interface + XxxHandler struct + constructor + go:generate directive
  restate_gen.go    # Auto-generated Restate HTTP client (DO NOT EDIT)
  *.go              # Business logic methods (use restate.Context)
db/
  migrations/       # SQL schema (*.up.sql / *.down.sql)
  queries/          # SQLC query templates (pgtempl-generated + *_custom.sql for hand-written)
  sqlc/             # Generated DB code (DO NOT EDIT)
model/
  *.go              # DTOs, domain models, error sentinels
transport/echo/
  *.go              # HTTP handlers
fx.go               # Uber fx module wiring
```

## Nullable Types

- `guregu/null/v6` for DB primitive fields (string, int, float, bool, time) in SQLC-generated structs.
- `NullXxx` structs (sqlc-generated) for nullable enum fields — they have custom `MarshalJSON` added by `cmd/nullmarshal`.
- `*T` pointers for JSON serialization in models exposed to Restate — generic types like `null.Value[T]` break Restate's JSON schema generation.
- `ptrutil.PtrIf[T](val, valid)` for converting nullable DB types to pointers, `ptrutil.Ptr[T](v)` for pointer literals.

## Error Handling

- **Client-visible errors**: `sharedmodel.NewError(httpStatusCode, "message")` defined in each module's `model/error.go`. Always return with `.Terminal()` to prevent Restate retries.
- **Internal errors**: `fmt.Errorf("action: %w", err)` — no "failed to" prefix (e.g., `fmt.Errorf("get account: %w", err)`).
