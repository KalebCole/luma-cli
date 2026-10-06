// Package api provides the shared authenticated client for Luma's internal web
// API. It uses the browser session in auth.SessionKey, not a public API key.
// Get and Post return decoded JSON (objects are map[string]any, arrays []any,
// and numbers json.Number). Every failure is an *Error safe for CLI output.
// Command handlers must validate mutations and honor --dry-run before Post.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KalebCole/luma-cli/internal/auth"
)

// BaseURL is the web API host. The legacy api.lu.ma host cannot handle writes.
const BaseURL = "https://api.luma.com"

const maxResponseBytes = 8 << 20

// Client sends authenticated requests with a bounded timeout and response size.
// It does not log requests, retry mutations, or follow redirects.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// New returns a client targeting BaseURL. Credentials are read on each request,
// so a client observes subsequent auth login/logout without being recreated.
func New() *Client {
	return &Client{
		baseURL: BaseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Get fetches a root-relative endpoint, e.g. Get("/event/get", url.Values{
// "event_api_id": {"evt-123"}}). Query values are URL-encoded; nil is allowed.
// Paths must not contain a host, query string, or fragment.
func (c *Client) Get(path string, query url.Values) (any, error) {
	return c.request(http.MethodGet, path, query, nil)
}

// Post encodes body as JSON and sends it to a root-relative endpoint. It adds
// the headers required by Luma's web client. A nil body is encoded as JSON null.
// Post dispatches immediately; callers own mutation validation and dry-run.
func (c *Client) Post(path string, body any) (any, error) {
	return c.request(http.MethodPost, path, nil, body)
}

func (c *Client) request(method, path string, query url.Values, body any) (any, error) {
	u, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") ||
		u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(path, "#") {
		return nil, failure("usage", "invalid_api_path", "API paths must be root-relative and contain no query string or fragment.")
	}
	u, err = url.Parse(c.baseURL + path)
	if err != nil {
		return nil, failure("usage", "invalid_api_path", "Unable to construct the API request.")
	}
	u.RawQuery = query.Encode()
	var payload []byte
	if method == http.MethodPost {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, failure("usage", "invalid_request_body", "The request body cannot be encoded as JSON.")
		}
	}
	key, err := auth.SessionKey()
	if err != nil {
		if errors.Is(err, auth.ErrInvalidKey) {
			return nil, failure("auth", "invalid_session_key", auth.ErrInvalidKey.Error())
		}
		return nil, failure("io", "credential_store_failed", auth.ErrStore.Error())
	}
	if key == "" {
		return nil, failure("auth", "missing_session_key", "No browser session credential is stored. Use auth login.")
	}
	req, err := http.NewRequest(method, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, failure("usage", "invalid_api_path", "Unable to construct the API request.")
	}
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: "luma.auth-session-key", Value: key})
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://luma.com")
		req.Header.Set("x-luma-client-type", "luma-web")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, failure("network", "request_failed", "Unable to reach the Luma API.")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusError(resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, failure("network", "response_read_failed", "Unable to read the Luma API response.")
	}
	if len(data) > maxResponseBytes {
		return nil, failure("api", "response_too_large", "The Luma API response exceeds the supported size.")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		return nil, failure("api", "invalid_response", "Luma returned an invalid JSON response.")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, failure("api", "invalid_response", "Luma returned an invalid JSON response.")
	}
	return result, nil
}
