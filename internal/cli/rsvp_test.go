package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/KalebCole/luma-cli/internal/auth"
)

// Route the shared API client's real HTTP requests only to an in-memory
// httptest server; no TCP listener or live API access is needed.
func rsvpServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	authConfig(t)
	if err := auth.Save("usr-test.secret"); err != nil {
		t.Fatal(err)
	}
	listener := &rsvpListener{connections: make(chan net.Conn), closed: make(chan struct{})}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	t.Cleanup(server.Close)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != "api.luma.com:443" {
			t.Errorf("unexpected host: %s", address)
			return nil, net.ErrClosed
		}
		client, peer := net.Pipe()
		select {
		case listener.connections <- peer:
			return client, nil
		case <-ctx.Done():
			client.Close()
			peer.Close()
			return nil, ctx.Err()
		}
	}}
	// The URL remains the production URL for assertions; only its transport
	// scheme is changed so the pipe carries ordinary local HTTP.
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		cloned := req.Clone(req.Context())
		cloned.URL.Scheme = "http"
		cloned.URL.Host = "api.luma.com:443"
		return transport.RoundTrip(cloned)
	})
	t.Cleanup(func() { http.DefaultTransport = previous; transport.CloseIdleConnections() })
}

type rsvpListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func (l *rsvpListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *rsvpListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *rsvpListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345} }

func TestRSVPGet(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"guest_data":{"approval_status":"approved"},"event":{"guest_data":{"status":"not-going"}}}`, `"going"`},
		{`{"guest_data":{"approval_status":"declined"}}`, `"not-going"`},
		{`{"guest_data":{"rsvp_status":"interested"}}`, `"interested"`},
		{`{"guest_data":{"status":"invited"}}`, `"invited"`},
		{`{"guest_data":null}`, `null`},
		{`{"event":{"guest_data":{"status":"going"}}}`, `null`},
	} {
		t.Run(tc.want+tc.body, func(t *testing.T) {
			calls := 0
			rsvpServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/event/get" || r.URL.Query().Get("event_api_id") != "evt-a&b" || r.Header.Get("Cookie") != "luma.auth-session-key=usr-test.secret" {
					t.Error("incorrect RSVP lookup")
				}
				io.WriteString(w, tc.body)
			})
			var out, errOut bytes.Buffer
			if New("test", &out, &errOut).Run([]string{"rsvp", "get", "evt-a&b"}) != 0 || errOut.Len() != 0 {
				t.Fatalf("lookup failed: %s", &errOut)
			}
			want := `{"data":{"eventId":"evt-a&b","myRsvp":` + tc.want + `},"ok":true}`
			var actual, expected any
			json.Unmarshal(out.Bytes(), &actual)
			json.Unmarshal([]byte(want), &expected)
			if !reflect.DeepEqual(actual, expected) || calls != 1 {
				t.Fatalf("unexpected result: %s", &out)
			}
		})
	}
}

func TestRSVPSetGoing(t *testing.T) {
	for _, dry := range []bool{false, true} {
		for _, nested := range []bool{false, true} {
			t.Run(map[bool]string{false: "submit", true: "dry-run"}[dry]+map[bool]string{false: "flat", true: "nested"}[nested], func(t *testing.T) {
				var paths []string
				wantBody := registration{Name: "Ada Lovelace", FirstName: "Ada", LastName: "Lovelace", Email: "ada@example.com", EventID: "evt-test", Tickets: map[string]ticketSelection{"ticket-free": {Count: 1, Amount: 0}}}
				rsvpServer(t, func(w http.ResponseWriter, r *http.Request) {
					paths = append(paths, r.Method+" "+r.URL.Path)
					if r.Header.Get("Cookie") != "luma.auth-session-key=usr-test.secret" {
						t.Error("missing auth")
					}
					switch r.URL.Path {
					case "/event/get":
						io.WriteString(w, `{"ticket_types":[{"api_id":"ticket-free"}]}`)
					case "/user":
						body := `{"name":"Ada Lovelace","email":"ada@example.com"}`
						if nested {
							body = `{"user":{"name":"Ada Lovelace","first_name":"Ada","last_name":"Lovelace","email":"ada@example.com"}}`
						}
						io.WriteString(w, body)
					case "/event/register":
						if dry {
							t.Error("dry-run dispatched mutation")
						}
						if r.Method != "POST" || r.Header.Get("Origin") != "https://luma.com" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("x-luma-client-type") != "luma-web" {
							t.Error("incorrect write headers")
						}
						var body registration
						if json.NewDecoder(r.Body).Decode(&body) != nil || !reflect.DeepEqual(body, wantBody) {
							t.Errorf("unexpected registration: %+v", body)
						}
						io.WriteString(w, `{"status":"success"}`)
					default:
						t.Error("unexpected endpoint")
					}
				})
				var out, errOut bytes.Buffer
				args := []string{"rsvp", "set", "evt-test", "--status=going"}
				if dry {
					args = append(args, "--dry-run")
				}
				if New("test", &out, &errOut).Run(args) != 0 {
					t.Fatalf("failed: %s", &errOut)
				}
				var result struct {
					OK   bool
					Data struct {
						EventID   string `json:"eventId"`
						Intent    string
						Submitted bool
						DryRun    bool
						Request   struct {
							Method  string
							URL     string
							Headers map[string]string
							Body    registration
						}
					}
				}
				if json.Unmarshal(out.Bytes(), &result) != nil || !result.OK || result.Data.EventID != "evt-test" || result.Data.Intent != "going" || result.Data.Submitted == dry || result.Data.DryRun != dry {
					t.Fatalf("incorrect result: %s", &out)
				}
				if dry && (!reflect.DeepEqual(result.Data.Request.Body, wantBody) || result.Data.Request.Headers["Cookie"] != "[REDACTED]" || result.Data.Request.Method != "POST" || result.Data.Request.URL != "https://api.luma.com/event/register") {
					t.Fatal("incorrect dry-run request")
				}
				expected := []string{"GET /event/get", "GET /user"}
				if !dry {
					expected = append(expected, "POST /event/register")
				}
				if !reflect.DeepEqual(paths, expected) {
					t.Fatalf("requests: %v", paths)
				}
				if strings.Contains(out.String()+errOut.String(), "usr-test.secret") {
					t.Fatal("credential leaked")
				}
			})
		}
	}
}

func TestRSVPValidation(t *testing.T) {
	rsvpServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid command made a request") })
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"rsvp"}, "missing_subcommand"}, {[]string{"rsvp", "unknown"}, "unknown_command"},
		{[]string{"rsvp", "get"}, "missing_event_id"}, {[]string{"rsvp", "get", " "}, "invalid_event_id"},
		{[]string{"rsvp", "get", "evt-1", "extra"}, "unexpected_arguments"},
		{[]string{"rsvp", "set", "evt-1"}, "missing_status"},
		{[]string{"rsvp", "set", "evt-1", "--status"}, "missing_status"},
		{[]string{"rsvp", "set", "evt-1", "--status="}, "invalid_status"},
		{[]string{"rsvp", "set", "evt-1", "--status=invalid-secret"}, "invalid_status"},
		{[]string{"rsvp", "set", "evt-1", "--status=going", "--status=going"}, "duplicate_status"},
		{[]string{"rsvp", "set", "evt-1", "--status", "not-going"}, "rsvp_status_unverified"},
		{[]string{"rsvp", "set", "evt-1", "--status=interested", "--dry-run"}, "rsvp_status_unverified"},
	} {
		var out, errOut bytes.Buffer
		if New("test", &out, &errOut).Run(tc.args) != 1 || out.Len() != 0 {
			t.Fatalf("expected error: %v", tc.args)
		}
		var result struct {
			OK    bool
			Error Error
		}
		if json.Unmarshal(errOut.Bytes(), &result) != nil || result.OK || result.Error.Code != tc.code {
			t.Fatalf("incorrect error: %s", &errOut)
		}
		if tc.code == "rsvp_status_unverified" && (result.Error.Type != "unsupported" || !strings.Contains(result.Error.Message, "browser network tab")) {
			t.Fatal("missing unsupported explanation")
		}
	}
}

func TestRSVPFailures(t *testing.T) {
	for _, tc := range []struct {
		event, user, post string
		status            int
		code              string
	}{
		{event: `[]`, code: "invalid_response"},
		{event: `{"guest_data":true}`, code: "invalid_response"},
		{event: `{}`, code: "missing_ticket_type"},
		{event: `{"ticket_types":[{}]}`, code: "invalid_response"},
		{user: `{}`, code: "missing_user_identity"},
		{post: `{"status":"failed","message":"usr-test.secret"}`, code: "rsvp_registration_failed"},
		{post: `{}`, code: "rsvp_registration_failed"},
		{post: `null`, code: "rsvp_registration_failed"},
		{status: 401, code: "session_rejected"},
		{status: 400, code: "invalid_request"},
	} {
		t.Run(tc.code+tc.post+tc.event, func(t *testing.T) {
			rsvpServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/event/get":
					if tc.event != "" {
						io.WriteString(w, tc.event)
					} else {
						io.WriteString(w, `{"ticket_types":[{"api_id":"ticket-1"}]}`)
					}
				case "/user":
					if tc.user != "" {
						io.WriteString(w, tc.user)
					} else {
						io.WriteString(w, `{"name":"Ada","email":"ada@example.com"}`)
					}
				case "/event/register":
					if tc.status != 0 {
						w.WriteHeader(tc.status)
					}
					io.WriteString(w, tc.post)
				}
			})
			var out, errOut bytes.Buffer
			args := []string{"rsvp", "set", "evt-1", "--status", "going"}
			if strings.Contains(tc.event, "guest_data") {
				args = []string{"rsvp", "get", "evt-1"}
			}
			if New("test", &out, &errOut).Run(args) != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), tc.code) || strings.Contains(errOut.String(), "usr-test.secret") {
				t.Fatalf("incorrect error: %s", &errOut)
			}
		})
	}
}

func TestRSVPOutputModes(t *testing.T) {
	rsvpServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"guest_data":{"approval_status":"approved"}}`)
	})
	for _, forceJSON := range []bool{false, true} {
		var out, errOut bytes.Buffer
		c := New("test", &out, &errOut)
		c.isTTY = true
		args := []string{"rsvp", "get", "evt-test"}
		if forceJSON {
			args = append(args, "--json")
		}
		if c.Run(args) != 0 || errOut.Len() != 0 {
			t.Fatalf("failed: %s", &errOut)
		}
		if forceJSON {
			if !json.Valid(out.Bytes()) {
				t.Fatal("--json did not force JSON")
			}
		} else if out.String() != "Event: evt-test\nRSVP: going\n" {
			t.Fatalf("unexpected human output: %s", &out)
		}
	}
}

func TestRSVPMissingCredential(t *testing.T) {
	rsvpServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unauthenticated request dispatched") })
	if err := auth.Logout(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if New("test", &out, &errOut).Run([]string{"rsvp", "get", "evt-test"}) != 1 || !strings.Contains(errOut.String(), "missing_session_key") || out.Len() != 0 {
		t.Fatalf("incorrect missing credential error: %s", &errOut)
	}
}
