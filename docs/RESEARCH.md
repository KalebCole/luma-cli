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
| POST | `/event/register` | Guest registration for going; see the supplied contract below. |
| POST | `/event/create` | Host-side event creation (single call, strict silent validator — out of scope for v1). |

## Guest RSVP contract

The supplied implementation contract pins down guest registration for `going`:

1. GET `/event/get?event_api_id=<id>`. Read the current guest's RSVP from
   `guest_data` at the response root, not from `event.guest_data`. A missing
   or null guest is returned as `myRsvp: null`. Canonical RSVP states are
   preserved; `approved` and `declined` approval states normalize to `going`
   and `not-going`.
2. For registration, select the first root `ticket_types[]` entry's `api_id`.
3. GET `/user` for `name`, `first_name`, `last_name`, and `email` (the identity
   may be at the root or under `user`). When only `name` is returned, split at
   its first space for the first and last names. A name and email are required.
4. POST `/event/register` with this body:

   ```json
   {
     "name": "Ada Lovelace",
     "first_name": "Ada",
     "last_name": "Lovelace",
     "email": "ada@example.com",
     "event_api_id": "evt-XXX",
     "for_waitlist": false,
     "ticket_type_to_selection": {
       "ticket-XXX": {"count": 1, "amount": 0}
     }
   }
   ```

   Require `{"status":"success"}` in the response; HTTP success alone does
   not confirm registration. This contract uses count 1 and amount 0 and
   does not implement paid checkout or ticket selection options.

`rsvp set --status going --dry-run` performs the two read-only lookups to
validate and normalize the request, prints the POST method, URL, headers
(with `Cookie` redacted), and body, and never dispatches the POST.

There is **no verified guest-side endpoint** for `not-going` or `interested`.
Both return `type: unsupported`, `code: rsvp_status_unverified`, including
in dry-run mode, without making API requests. Capture the endpoint and
request/response shapes from the browser network tab during an RSVP on
luma.com before adding support. Do not guess `/event/rsvp` or a host-side
endpoint. No live API calls were made during implementation validation.

## Gotchas

- All timestamps on the wire are UTC ISO-8601. Render with each event's own
  `timezone` field.
- `POST` calls need `Content-Type: application/json`, `Origin: https://luma.com`,
  and `x-luma-client-type: luma-web` headers (verified for `/event/create`).
- Error responses are flat and silent: `400 {"message":"Invalid request.","code":null}`
  for almost every failure mode. When a write fails with no detail, check the
  host first, then the payload, then the cookie.
