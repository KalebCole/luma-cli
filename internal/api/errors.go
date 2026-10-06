package api

// Error is the CLI's public error representation. It contains only fixed,
// credential-safe messages, never upstream bodies, headers, or transport errors.
// Callers can use errors.As to inspect it and serialize it in an envelope:
// {"ok":false,"error":{"type":...,"code":...,"message":...}}.
type Error struct {
	Type       string `json:"type"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"` // Nonzero for failures mapped from HTTP status.
}

func (e *Error) Error() string { return e.Message }

func failure(kind, code, message string) *Error {
	return &Error{Type: kind, Code: code, Message: message}
}

// Luma's flat errors often have a null code and no useful detail. Do not echo
// even apparently useful messages: the server could reflect a credential.
func statusError(status int) *Error {
	var e *Error
	switch status {
	case 400, 422:
		e = failure("api", "invalid_request", "Luma rejected the request. Check the request payload and browser session.")
	case 401:
		e = failure("auth", "session_rejected", "Luma rejected the browser session; it may have expired. Log in again.")
	case 403:
		e = failure("auth", "forbidden", "The browser session does not have permission for this request.")
	case 404:
		e = failure("api", "not_found", "The requested Luma resource was not found.")
	case 409:
		e = failure("api", "conflict", "The request conflicts with the current Luma resource state.")
	case 429:
		e = failure("api", "rate_limited", "Luma's request limit was reached. Try again later.")
	default:
		switch {
		case status >= 500:
			e = failure("api", "server_error", "Luma could not complete the request. Try again later.")
		case status >= 300 && status < 400:
			e = failure("api", "unexpected_redirect", "Luma returned a redirect; the authenticated request was not forwarded.")
		default:
			e = failure("api", "http_error", "Luma returned an unexpected HTTP status.")
		}
	}
	e.HTTPStatus = status
	return e
}
