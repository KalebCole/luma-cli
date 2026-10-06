package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/KalebCole/luma-cli/internal/auth"
)

const testKey = "usr-local.secret-do-not-print"

func localAuth(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("LUMA_AUTH_SESSION_KEY", "usr-env.must-not-be-used")
	if err := auth.Save(testKey); err != nil {
		t.Fatal(err)
	}
}

func localClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	// In-memory connections exercise real HTTP framing and httptest.Server
	// even in sandboxes that prohibit opening TCP sockets.
	l := &pipeListener{connections: make(chan net.Conn), closed: make(chan struct{})}
	s := &httptest.Server{Listener: l, Config: &http.Server{Handler: handler}}
	s.Start()
	t.Cleanup(s.Close)
	c := New()
	c.baseURL = s.URL
	transport := &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != l.Addr().String() {
			t.Error("client attempted to connect outside the local server")
			return nil, net.ErrClosed
		}
		client, server := net.Pipe()
		select {
		case l.connections <- server:
			return client, nil
		case <-ctx.Done():
			client.Close()
			server.Close()
			return nil, ctx.Err()
		case <-l.closed:
			client.Close()
			server.Close()
			return nil, net.ErrClosed
		}
	}}
	c.httpClient.Transport = transport
	t.Cleanup(transport.CloseIdleConnections)
	return c
}

type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
}

func assertError(t *testing.T, err error, kind, code string, status int) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("expected typed error, got %T", err)
	}
	if e.Type != kind || e.Code != code || e.HTTPStatus != status {
		t.Fatalf("unexpected error: %+v", e)
	}
	encoded, marshalErr := json.Marshal(struct {
		OK    bool   `json:"ok"`
		Error *Error `json:"error"`
	}{Error: e})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, output := range []string{err.Error(), string(encoded), fmt.Sprintf("%+v", e)} {
		if strings.Contains(output, testKey) || strings.Contains(output, "Cookie") || strings.Contains(output, "upstream-secret") {
			t.Fatal("error exposed sensitive upstream data")
		}
	}
	var envelope map[string]any
	if err := json.Unmarshal(encoded, &envelope); err != nil || envelope["ok"] != false {
		t.Fatalf("invalid error envelope: %s", encoded)
	}
	fields := envelope["error"].(map[string]any)
	if len(fields) != 3 || fields["type"] != kind || fields["code"] != code || fields["message"] == "" {
		t.Fatalf("invalid error fields: %s", encoded)
	}
}

func TestGetAndPost(t *testing.T) {
	localAuth(t)
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		key := testKey
		if r.Method == http.MethodPost {
			key = "usr-changed.new-secret"
		}
		if r.Header.Get("Cookie") != "luma.auth-session-key="+key || r.Header.Get("Accept") != "application/json" {
			t.Error("missing expected authentication or Accept header")
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/event/get" || r.URL.Query().Get("event_api_id") != "evt-a & b" || len(r.URL.Query()["tag"]) != 2 {
				t.Errorf("incorrect query encoding: %s", r.URL)
			}
			if r.Header.Get("Origin") != "" || r.Header.Get("Content-Type") != "" || r.ContentLength != 0 {
				t.Error("GET should have no POST headers or body")
			}
		case http.MethodPost:
			if r.URL.Path != "/event/rsvp" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Origin") != "https://luma.com" || r.Header.Get("x-luma-client-type") != "luma-web" {
				t.Error("incorrect POST endpoint or web headers")
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["intent"] != "going" {
				t.Error("incorrect JSON request body")
			}
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
		_, _ = io.WriteString(w, `{"event":{"api_id":"evt-1"},"count":9007199254740993}`)
	})
	result, err := c.Get("/event/get", url.Values{"event_api_id": {"evt-a & b"}, "tag": {"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	object := result.(map[string]any)
	if object["event"].(map[string]any)["api_id"] != "evt-1" || object["count"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("unexpected decoded response: %#v", result)
	}
	// Each request must observe the current credential, not a cached session.
	if err := auth.Save("usr-changed.new-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Post("/event/rsvp", map[string]string{"intent": "going"}); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	localAuth(t)
	tests := []struct {
		status int
		kind   string
		code   string
	}{
		{400, "api", "invalid_request"}, {422, "api", "invalid_request"},
		{401, "auth", "session_rejected"}, {403, "auth", "forbidden"},
		{404, "api", "not_found"}, {409, "api", "conflict"},
		{429, "api", "rate_limited"}, {500, "api", "server_error"},
		{503, "api", "server_error"}, {418, "api", "http_error"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			for _, body := range []string{`{"message":"Invalid request.","code":null}`, `{"message":"Cookie: luma.auth-session-key=` + testKey + `","code":"upstream-secret"}`, "<html>upstream-secret</html>"} {
				c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tt.status)
					_, _ = io.WriteString(w, body)
				})
				_, err := c.Post("/event/rsvp", nil)
				assertError(t, err, tt.kind, tt.code, tt.status)
			}
		})
	}
}

func TestRedirectIsNeverFollowed(t *testing.T) {
	localAuth(t)
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/event/rsvp" {
					t.Error("redirect target received authenticated request")
				}
				http.Redirect(w, r, "http://other.invalid/redirect-target", status)
			})
			_, err := c.Post("/event/rsvp", nil)
			assertError(t, err, "api", "unexpected_redirect", status)
		})
	}
}

func TestInvalidInputAndCredentialsDoNotDispatch(t *testing.T) {
	localAuth(t)
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid request was dispatched")
	})
	for _, path := range []string{"", "user", "https://example.com/user", "//example.com/user", "/user?key=secret", "/user?", "/user#fragment", "/bad%ZZ", "/bad\npath"} {
		_, err := c.Get(path, nil)
		assertError(t, err, "usage", "invalid_api_path", 0)
	}
	_, err := c.Post("/event/rsvp", make(chan int))
	assertError(t, err, "usage", "invalid_request_body", 0)
	if err := auth.Logout(); err != nil {
		t.Fatal(err)
	}
	_, err = c.Get("/user", nil)
	assertError(t, err, "auth", "missing_session_key", 0)
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "luma", "session-key")
	if err := os.WriteFile(path, []byte("invalid-upstream-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = c.Get("/user", nil)
	assertError(t, err, "auth", "invalid_session_key", 0)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	_, err = c.Get("/user", nil)
	assertError(t, err, "io", "credential_store_failed", 0)
}

func TestResponseValidation(t *testing.T) {
	localAuth(t)
	for _, tt := range []struct {
		name string
		body string
		code string
	}{
		{"empty", "", "invalid_response"},
		{"malformed", "Cookie: " + testKey, "invalid_response"},
		{"trailing JSON", `{} {}`, "invalid_response"},
		{"trailing garbage", `{} upstream-secret`, "invalid_response"},
		{"oversized", strings.Repeat(" ", maxResponseBytes+1), "response_too_large"},
		{"array", `[true,"text",null]`, ""},
		{"null", "null", ""},
		{"whitespace", "{} \n", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, tt.body)
			})
			_, err := c.Get("/user", nil)
			if tt.code != "" {
				assertError(t, err, "api", tt.code, 0)
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	if data, err := c.Post("/event/rsvp", nil); data != nil || err != nil {
		t.Fatalf("expected empty success, got %#v, %v", data, err)
	}
}

func TestTruncatedResponseIsSafe(t *testing.T) {
	localAuth(t)
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		_, _ = io.WriteString(w, `{"secret":"`+testKey+`"}`)
	})
	_, err := c.Get("/user", nil)
	assertError(t, err, "network", "response_read_failed", 0)
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("Cookie: luma.auth-session-key=" + testKey)
}

func TestTransportErrorsAreSafe(t *testing.T) {
	localAuth(t)
	c := New()
	if c.baseURL != "https://api.luma.com" || c.httpClient.Timeout <= 0 {
		t.Fatal("incorrect production host or missing timeout")
	}
	c.httpClient.Transport = failingTransport{}
	_, err := c.Get("/user", nil)
	assertError(t, err, "network", "request_failed", 0)
}
