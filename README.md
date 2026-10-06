# luma-cli

JSON-first native Go CLI for managing your Luma event invitations from the
terminal: list what you're invited to, check details, and RSVP.

Luma's official public API is host-side only (no "events I'm invited to", no
guest RSVP), so this CLI drives Luma's internal web API (`api.luma.com`) with
your own browser session cookie, the same way the website does.

## Install

```bash
go install github.com/KalebCole/luma-cli/cmd/luma@latest
```

Verify a local install safely:

```bash
luma --version
luma schema
luma doctor
```

## Authentication

Log in to luma.com in your browser, open DevTools (Application > Cookies >
https://luma.com), and copy the `luma.auth-session-key` cookie value:

```bash
export LUMA_AUTH_SESSION_KEY="usr-..."
luma auth login
luma auth status
luma doctor
```

`auth login` also accepts `--key <key>` (the flag takes precedence over the
env var; prefer the env var to keep the key out of shell history). The key is
stored at `~/.config/luma/session-key` with file mode `0600` and never
printed. `auth logout` removes it.

## Approved command catalog

```text
luma --version
luma schema
luma doctor
luma auth login
luma auth status
luma auth logout
luma events list --upcoming
luma events list --past [--limit n]
luma events get <event-id>
luma rsvp get <event-id>
luma rsvp set <event-id> --status going
luma rsvp set <event-id> --status not-going [--message "text"]
```

Run `luma schema` for the machine-readable catalog. `--status interested` is
not a state Luma supports; the command returns an `unsupported` error for it.

## Mutation safety

`rsvp set` validates first and executes once. Add `--dry-run` to print a
redacted request preview (cookie redacted) without sending anything.

## Output

Output is JSON when piped and a human table on a terminal; `--json` forces
JSON. Errors are JSON envelopes on stderr with a nonzero exit code. The
session key is never printed.

## Development

Go-only: no Node or npm required.

```bash
go mod verify
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Release builds set the version with
`go build -ldflags '-X main.version=1.0.0' -o luma ./cmd/luma`.

## License

MIT
