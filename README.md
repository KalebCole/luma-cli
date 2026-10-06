# luma-cli

Agentic CLI for Luma: list your invitations and RSVP from the terminal.

Built for the same workflow as [partiful-cli](https://github.com/KalebCole/partiful-cli) — a weekly review of event invitations with accept/decline from the command line. Luma's official public API is host-side only (no "events I'm invited to", no guest RSVP), so this CLI drives Luma's internal web API (`api.luma.com`) with your own session cookie, the same way the website does.

## Status

The Go CLI implements `--version`, `schema`, `doctor`, `auth login/status/logout`, `events list/get`, `rsvp get`, and `rsvp set --status going`. The full approved command surface is specified in `spec/commands.md`. RSVP `not-going` and `interested` return `rsvp_status_unverified` until their guest endpoints are captured.

Build with Go 1.22 or later (no Node or npm required):

```bash
go build -o luma ./cmd/luma
./luma --version
./luma schema
./luma doctor
```

`doctor` makes a read-only GET to `https://api.luma.com/user` with an eight-second timeout. It checks `LUMA_AUTH_SESSION_KEY` (falling back to the stored credential) and verifies authentication when a valid session key is present. It always exits 0 for diagnostic results; JSON includes `data.healthy` and separate authentication and connectivity checks. Missing credentials, rejected sessions, and connection failures appear in those checks. It never prints credentials or server response bodies.

Output defaults to JSON when piped and a human table on a terminal; `--json` forces JSON. `schema` always prints JSON and `--version` prints the version string (default `dev`). Global `--dry-run` validates auth mutations and prints their action with credentials redacted; RSVP dry-run fetches the event and user, then prints the normalized registration request with its Cookie redacted without sending the POST; it does not suppress read-only checks. Errors are JSON envelopes on stderr with a nonzero exit code.

Release builds can set the version with `go build -ldflags '-X main.version=1.0.0' -o luma ./cmd/luma`.

## Quick start (once built)

```bash
# 1. Log in with luma.com in your browser, open DevTools
#    Application > Cookies > https://luma.com, copy `luma.auth-session-key`
export LUMA_AUTH_SESSION_KEY="usr-..."

# 2. Store the browser session cookie and check auth
luma auth login
luma auth status
luma doctor

# 3. List upcoming invitations
luma events list --upcoming

# 4. RSVP
luma rsvp set <event-id> --status going
luma rsvp get <event-id>
luma rsvp set <event-id> --status going --dry-run
```

## Authentication

`auth login` reads `LUMA_AUTH_SESSION_KEY` or an explicit `--key <key>` / `--key=<key>` (the flag takes precedence). It validates the cookie shape locally without prompts, a browser, or an API request. Prefer the environment variable to avoid placing credentials in shell history or process arguments.

The credential is stored in `luma/session-key` under the OS user config directory, with file mode `0600` and directory mode `0700`. On Linux this is `$XDG_CONFIG_HOME/luma/session-key`, or `~/.config/luma/session-key`. Replacements are atomic, and unsafe store permissions and symlinks are rejected when reading. Keys, response bodies, and transport errors are never printed.

`auth status` reads the stored credential and sends a GET to `https://api.luma.com/user` using the `luma.auth-session-key` cookie. Authentication requires HTTP 200 and a valid JSON response with a nonempty `api_id` or `user.api_id`, using the same identity validation as `doctor`. Anonymous or malformed responses and other HTTP statuses mean unauthenticated. No stored credential reports unauthenticated without a request. Transport and storage failures produce a redacted JSON error envelope and exit 1. Expiry is omitted because it is not known. Requests time out after eight seconds and do not follow redirects.

`auth logout` removes the stored credential and succeeds if it is already absent. It does not unset environment variables. Both login and logout support `--dry-run` without changing files or sending requests.

Go packages can call `auth.SessionKey() (string, error)` to read the stored key. An empty string with a nil error means no credential is stored; the function does not read environment variables or contact the API. Treat its result as a secret.

## Build plan for Codex

1. `internal/api` — HTTP client for `https://api.luma.com` with the session cookie header. Start from the endpoint catalog in `docs/RESEARCH.md`.
2. `internal/auth` — `auth login` (paste session key into local credential store), `auth status`, `auth logout`, `doctor`.
3. `internal/events` — `events list --upcoming` via `/calendar/get-items` on the personal calendar (`personal_calendar_api_id` from `/user`), plus `--past`.
4. `internal/cli/rsvp.go` — implemented `rsvp get` and `rsvp set --status going` via `/event/register`; unverified statuses return an unsupported error. See `docs/RESEARCH.md`.
5. Mirror partiful-cli conventions: `--json` default when piped, `--dry-run` on mutations, `schema` command for the machine-readable catalog, JSON error envelopes.

## Layout

```
cmd/luma          CLI entrypoint
internal/api      api.luma.com client
internal/auth     login / status / logout / doctor
internal/events   events list
internal/cli       command registration, output, and rsvp get / set
docs/RESEARCH.md  endpoint catalog and auth notes
spec/commands.md  approved command surface
skills/           agent skill (mirrors partiful-cli layout)
```

## License

MIT
