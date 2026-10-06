# Command spec — luma-cli v1

Approved public command surface. `luma schema` must list exactly these.

## Global

- `luma --version` — print version, exit 0.
- `luma schema` — machine-readable command catalog (JSON), exit 0.
- `luma doctor` — auth + connectivity check, no mutations, exit 0.
- `--json` — force JSON output. `--dry-run` — validate and print the
  normalized request without dispatching (mutations only).

## auth

```bash
luma auth login        # paste LUMA_AUTH_SESSION_KEY into the local credential store
luma auth status       # authenticated yes/no, expiry if known
luma auth logout       # clear the stored credential
```

`auth login` must work non-interactively: read the key from
`LUMA_AUTH_SESSION_KEY` env or `--key` flag. No browser, no prompts.

## events

```bash
luma events list --upcoming            # your upcoming invitations + RSVPs
luma events list --past               # history
luma events list --upcoming --limit 20
luma events get <event-id>            # full details for one event
```

List output per event: `eventId`, `title`, `start` (UTC ISO), `timezone`,
`location`, `myRsvp` (`going` | `not-going` | `interested` | `invited` | null),
`userRole` (`host` | `attendee`), `url` (public luma.com link).

## rsvp

```bash
luma rsvp get <event-id>                                   # your current RSVP state
luma rsvp set <event-id> --status going                   # accept
luma rsvp set <event-id> --status not-going               # decline
luma rsvp set <event-id> --status interested              # maybe
```

`rsvp set` requires `--dry-run` support and must print the normalized request
before dispatching when the flag is present. On success return
`{"ok":true,"data":{"eventId":...,"intent":...,"submitted":true}}`.

## Out of scope for v1

Event creation, guest management, calendar management, discovery/search feeds.
Those live in the official host-side API; v1 is the attendee workflow only.
