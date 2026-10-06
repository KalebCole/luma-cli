# AGENTS.md — Luma CLI

Use the Go CLI in `cmd/luma` and `internal/`.

## Release and validation

The native release path is Go-only.

```bash
go mod verify
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Do not add a release step that needs Node or npm.

## Safe local smoke commands

Use only these non-mutating commands in automated verification:

```text
luma --version
luma schema
luma doctor
```

`schema` is the machine-readable command catalog. `doctor` is the safe local
check for authentication state. Never run a mutating command (`rsvp set`,
`auth login`) in automated verification.

## Conventions

- JSON-first: default to JSON output when stdout is not a TTY; human tables
  only on a TTY unless `--json` is passed.
- Every mutation supports `--dry-run`: validate and print the normalized
  request without dispatching.
- Errors use JSON envelopes: `{"ok":false,"error":{"type":...,"code":...,"message":...}}`.
- Timestamps on the wire are UTC ISO-8601. Render with the event's own
  `timezone` field, never raw UTC.
- Never print the session key or any credential. Redact `Cookie` headers in
  logs and dry-run output.
- `LUMA_AUTH_SESSION_KEY` is the only credential. It is a browser session
  cookie (`luma.auth-session-key`, shaped `usr-<id>.<secret>`), not an API
  key. It expires with the browser session.
