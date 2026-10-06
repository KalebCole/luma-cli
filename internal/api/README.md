# Shared Luma API client

Import `github.com/KalebCole/luma-cli/internal/api` and create a reusable client
with `api.New()`. It targets `https://api.luma.com` and reads the current browser
session through `auth.SessionKey()` on every request. Missing, invalid, or
unreadable credentials fail before dispatch. The client does not read the
environment or store credentials; `auth login` owns that workflow.

```go
client := api.New()
data, err := client.Get("/event/get", url.Values{
    "event_api_id": {"evt-123"},
})
```

`Get(path, query)` URL-encodes `url.Values`; pass nil for no query.
`Post(path, body)` JSON-encodes any marshalable value and sends the required
web headers. Paths start with `/` and contain no host, query, or fragment.
These helpers return decoded JSON: objects are `map[string]any`, arrays are
`[]any`, and numbers are `json.Number` to preserve precision. JSON null and
HTTP 204 return nil. Command handlers interpret endpoint-specific fields and
must validate mutations and honor `--dry-run` before calling `Post`.

All failures are `*api.Error`, with `type`, `code`, and `message` JSON fields.
`HTTPStatus` is available for programmatic inspection but excluded from JSON.
`cli.Error` aliases this type, so command handlers can return API errors directly
and the CLI emits its usual `{"ok":false,"error":...}` envelope. Use
`errors.As(err, &apiError)` to inspect an error. HTTP status determines the error
code, including `invalid_request` for Luma's silent 400 responses,
`session_rejected` for 401, `forbidden` for 403, and `rate_limited` for 429.
Upstream messages and codes are deliberately excluded because they may reflect
credentials. Transport and encoding errors also use fixed safe messages.

Requests have a 15-second timeout and successful JSON responses are limited to
8 MiB. Redirects are never followed, and mutations are never retried. The client
does not log request headers or response bodies. Unit tests use local
`httptest` servers and temporary credential stores; no live API calls are needed.
