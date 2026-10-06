# Luma API research

Compiled 2026-10-05 from [echennells/luma-skill](https://github.com/echennells/luma-skill)
and [mariovallereyes/luma-skill](https://github.com/mariovallereyes/luma-skill).
Verify each endpoint against the live API during the build — Luma's internal
API is undocumented and can drift.

## Base

- Host: `https://api.luma.com` (migrated from `api.lu.ma` in Aug 2026; the old
  host still answers reads but **all writes fail** there — never use it).
- Auth: `Cookie: luma.auth-session-key=$LUMA_AUTH_SESSION_KEY` on every request.
- The session key is copied from browser DevTools
  (Application > Cookies > `https://luma.com` > `luma.auth-session-key`).
  Shape: `usr-<id>.<secret>`; the part before the dot matches the account's
  `api_id`. Expires with the browser session.

## Why not the official API

Luma's public API (`public-api.luma.com`, ~74 endpoints) is host/calendar
scoped. There is **no endpoint for "events I'm invited to" and no guest-side
RSVP**. API keys also require a paid Luma Plus subscription. Attendee-side
tooling must use the internal web API below.

## Endpoint catalog

| Method | Path | Purpose |
| ------ | ---- | ------- |
| GET | `/user` | Who the session belongs to. Key fields: `personal_calendar_api_id`, `timezone`, `geo_city`. |
| GET | `/calendar/get-items?calendar_api_id=<cal>&period=future&pagination_limit=10` | **Your upcoming events (invitations + RSVPs).** `entries[]` wraps each event one level deep: `entry.event.api_id`, `entry.calendar`, `entry.hosts`. `period=past` for history. Supports `has_more` pagination. |
| GET | `/event/get?event_api_id=evt-XXX` | Full event details. Note: `description_mirror` (ProseMirror JSON) sits at the **root** of the response, not under `event`; `event.description` is always null. |
| GET | `/event/get-guest-list?event_api_id=evt-XXX` | Guest list. `entries[]`; each entry has a registration `api_id` at root and `user.api_id` underneath — not interchangeable. Only for events you host or with public guest lists. |
| GET | `/search/get-results?query=...` | Search events, calendars, places. |
| GET | `/discover/get-paginated-events?discover_place_api_id=...&pagination_limit=20` | City event feed. Resolve a city via `/url?url=<city-slug>` first. |
| GET | `/url?url=<slug-or-city>` | Resolve a luma.com slug to `kind` (`event`, `discover-place`, `calendar`, `user`) + object. |
| POST | `/event/create` | Host-side event creation (single call, strict silent validator — out of scope for v1). |

## Open research item: the RSVP endpoint

The exact guest RSVP/register call is **not pinned down** in the sources above.
`mariovallereyes/luma-skill` exposes `luma rsvp <slug> <yes|no|maybe|waitlist>`,
so a working call exists — port it from that repo or capture it from the
browser network tab while RSVPing on luma.com. Likely candidates to probe:
`/event/register`, `/event/rsvp`, ticket-type endpoints under `/event/`.
Whichever it is, wrap it as `rsvp set --status going|not-going|interested`
and record the request/response shapes here once verified.

## Gotchas

- All timestamps on the wire are UTC ISO-8601. Render with each event's own
  `timezone` field.
- `POST` calls need `Content-Type: application/json`, `Origin: https://luma.com`,
  and `x-luma-client-type: luma-web` headers (verified for `/event/create`).
- Error responses are flat and silent: `400 {"message":"Invalid request.","code":null}`
  for almost every failure mode. When a write fails with no detail, check the
  host first, then the payload, then the cookie.
