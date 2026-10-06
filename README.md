# luma-cli

Agentic CLI for Luma: list your invitations and RSVP from the terminal.

Built for the same workflow as [partiful-cli](https://github.com/KalebCole/partiful-cli) — a weekly review of event invitations with accept/decline from the command line. Luma's official public API is host-side only (no "events I'm invited to", no guest RSVP), so this CLI drives Luma's internal web API (`api.luma.com`) with your own session cookie, the same way the website does.

## Status

The Go CLI skeleton implements `--version`, `schema`, and `doctor`. The full approved command surface is specified in `spec/commands.md`; `schema` includes future commands with `implemented: false`. The `auth`, `events`, and `rsvp` command groups are not implemented yet.

Build with Go 1.22 or later (no Node or npm required):

```bash
go build -o luma ./cmd/luma
./luma --version
./luma schema
./luma doctor
```

`doctor` makes a read-only GET to `https://api.luma.com/user` with an eight-second timeout. It checks `LUMA_AUTH_SESSION_KEY` and verifies authentication when a valid session key is present. It always exits 0 for diagnostic results; JSON includes `data.healthy` and separate authentication and connectivity checks. Missing credentials, rejected sessions, and connection failures appear in those checks. It never prints credentials or server response bodies.

Output defaults to JSON when piped and a human table on a terminal; `--json` forces JSON. `schema` always prints JSON and `--version` prints the version string (default `dev`). Global `--dry-run` is accepted for future mutations; it does not suppress read-only checks. Errors are JSON envelopes on stderr with a nonzero exit code.

Release builds can set the version with `go build -ldflags '-X main.version=1.0.0' -o luma ./cmd/luma`.

## Quick start (once built)

```bash
# 1. Log in with luma.com in your browser, open DevTools
#    Application > Cookies > https://luma.com, copy `luma.auth-session-key`
export LUMA_AUTH_SESSION_KEY="usr-..."

# 2. Check auth
luma doctor

# 3. List upcoming invitations
luma events list --upcoming

# 4. RSVP
luma rsvp set <event-id> --status going
luma rsvp set <event-id> --status not-going
```

## Build plan for Codex

1. `internal/api` — HTTP client for `https://api.luma.com` with the session cookie header. Start from the endpoint catalog in `docs/RESEARCH.md`.
2. `internal/auth` — `auth login` (paste session key into local credential store), `auth status`, `auth logout`, `doctor`.
3. `internal/events` — `events list --upcoming` via `/calendar/get-items` on the personal calendar (`personal_calendar_api_id` from `/user`), plus `--past`.
4. `internal/rsvp` — `rsvp get`, `rsvp set --status going|not-going|interested`. The exact RSVP/register endpoint is the one open research item — see `docs/RESEARCH.md`.
5. Mirror partiful-cli conventions: `--json` default when piped, `--dry-run` on mutations, `schema` command for the machine-readable catalog, JSON error envelopes.

## Layout

```
cmd/luma          CLI entrypoint
internal/api      api.luma.com client
internal/auth     login / status / logout / doctor
internal/events   events list
internal/rsvp     rsvp get / set
docs/RESEARCH.md  endpoint catalog and auth notes
spec/commands.md  approved command surface
skills/           agent skill (mirrors partiful-cli layout)
```

## License

MIT
