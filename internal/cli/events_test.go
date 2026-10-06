package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/KalebCole/luma-cli/internal/auth"
)

// Real HTTP framing over in-memory connections keeps fixtures local even when
// the sandbox does not allow listening on TCP ports.
type eventListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
}

func (l *eventListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *eventListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *eventListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345} }

func eventsServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	l := &eventListener{connections: make(chan net.Conn), closed: make(chan struct{})}
	s := &httptest.Server{Listener: l, Config: &http.Server{Handler: handler}}
	s.Start()
	t.Cleanup(s.Close)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != l.Addr().String() {
			t.Error("attempted non-local connection")
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
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.luma.com" {
			t.Error("unexpected API host")
			return nil, net.ErrClosed
		}
		local := req.Clone(req.Context())
		local.URL.Scheme = "http"
		local.URL.Host = l.Addr().String()
		return transport.RoundTrip(local)
	})
	t.Cleanup(func() { http.DefaultTransport = previous; transport.CloseIdleConnections() })
}

const eventFixture = `{"api_id":"evt-one","name":"Evening meetup","start_at":"2026-10-06T01:00:00Z","timezone":"America/Los_Angeles","geo_address_json":{"address":"123 Main St"},"url":"evening-meetup","description":null}`

func TestEventsList(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		period, limit string
		nested        bool
	}{
		{name: "default", args: []string{"events", "list"}, period: "future", limit: "10"},
		{name: "upcoming", args: []string{"events", "list", "--upcoming", "--limit", "20"}, period: "future", limit: "20", nested: true},
		{name: "past", args: []string{"--json", "events", "list", "--past", "--limit=2"}, period: "past", limit: "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authConfig(t)
			if err := auth.Save("usr-test.fake"); err != nil {
				t.Fatal(err)
			}
			calls := 0
			eventsServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				cookie, err := r.Cookie("luma.auth-session-key")
				if err != nil || cookie.Value != "usr-test.fake" || r.Method != "GET" {
					t.Error("incorrect authenticated request")
				}
				switch r.URL.Path {
				case "/user":
					if calls != 1 || r.URL.RawQuery != "" {
						t.Error("incorrect user lookup")
					}
					if tc.nested {
						io.WriteString(w, `{"user":{"api_id":"usr-test","personal_calendar_api_id":"cal-personal"}}`)
					} else {
						io.WriteString(w, `{"api_id":"usr-test","personal_calendar_api_id":"cal-personal"}`)
					}
				case "/calendar/get-items":
					q := r.URL.Query()
					if calls != 2 || len(q) != 3 || q.Get("calendar_api_id") != "cal-personal" || q.Get("period") != tc.period || q.Get("pagination_limit") != tc.limit {
						t.Errorf("incorrect calendar query: %s", r.URL)
					}
					io.WriteString(w, `{"has_more":true,"entries":[{"event":`+eventFixture+`,"calendar":{"api_id":"cal-other"},"hosts":[{"api_id":"usr-other"}],"guest_info":{"approval_status":"approved"}},{"event":`+strings.ReplaceAll(eventFixture, "evt-one", "evt-two")+`,"hosts":[{"user":{"api_id":"usr-test"}}]}]}`)
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
				}
			})
			var out, errOut bytes.Buffer
			c := New("test", &out, &errOut)
			if c.Run(tc.args) != 0 {
				t.Fatal(errOut.String())
			}
			var result struct {
				OK   bool          `json:"ok"`
				Data []eventOutput `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !result.OK || len(result.Data) != 2 || calls != 2 {
				t.Fatalf("bad list: %s", out.String())
			}
			event := result.Data[0]
			if event.EventID != "evt-one" || event.Title != "Evening meetup" || event.Start != "2026-10-06T01:00:00Z" || event.Timezone != "America/Los_Angeles" || event.Location != "123 Main St" || event.MyRSVP == nil || *event.MyRSVP != "going" || event.UserRole != "attendee" || event.URL != "https://luma.com/evening-meetup" {
				t.Fatalf("bad normalized event: %+v", event)
			}
			if result.Data[1].MyRSVP != nil || result.Data[1].UserRole != "host" || !strings.Contains(out.String(), `"myRsvp":null`) {
				t.Fatal("incorrect host or null RSVP")
			}
			if strings.Contains(out.String()+errOut.String(), "usr-test.fake") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestEventsGetOutputModes(t *testing.T) {
	for _, tc := range []struct {
		name           string
		tty, forceJSON bool
	}{
		{name: "pipe"}, {name: "terminal", tty: true}, {name: "forced JSON", tty: true, forceJSON: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authConfig(t)
			if err := auth.Save("usr-test.fake"); err != nil {
				t.Fatal(err)
			}
			eventsServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/event/get" || r.URL.Query().Get("event_api_id") != "evt-a & b" {
					t.Errorf("incorrect get query: %s", r.URL)
				}
				io.WriteString(w, `{"event":`+eventFixture+`,"guest_info":{"approval_status":"declined"},"description_mirror":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Bring a friend."}]}]}}`)
			})
			var out, errOut bytes.Buffer
			c := New("test", &out, &errOut)
			c.isTTY = tc.tty
			args := []string{"events", "get", "evt-a & b"}
			if tc.forceJSON {
				args = append(args, "--json")
			}
			if c.Run(args) != 0 {
				t.Fatal(errOut.String())
			}
			if !tc.tty || tc.forceJSON {
				var result struct {
					OK   bool         `json:"ok"`
					Data eventDetails `json:"data"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if !result.OK || result.Data.DescriptionMirror == nil || !strings.Contains(out.String(), "Bring a friend.") || result.Data.MyRSVP == nil || *result.Data.MyRSVP != "not-going" {
					t.Fatalf("incorrect details: %s", out.String())
				}
			} else if !strings.Contains(out.String(), "2026-10-05 18:00 PDT") || !strings.Contains(out.String(), "America/Los_Angeles") || !strings.Contains(out.String(), "Bring a friend.") {
				t.Fatalf("incorrect local time: %s", out.String())
			}
		})
	}
}

func TestEventsUsageDoesNotDispatch(t *testing.T) {
	authConfig(t)
	eventsServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid arguments dispatched") })
	for _, args := range [][]string{
		{"events"}, {"events", "unknown"}, {"events", "get"}, {"events", "get", ""}, {"events", "get", "evt-one", "extra"},
		{"events", "list", "--upcoming", "--past"}, {"events", "list", "--limit"}, {"events", "list", "--limit=0"},
		{"events", "list", "--limit", "-1"}, {"events", "list", "--limit=abc"}, {"events", "list", "extra"}, {"events", "get", "--past", "evt-one"},
	} {
		var out, errOut bytes.Buffer
		if New("test", &out, &errOut).Run(args) != 1 || out.Len() != 0 {
			t.Fatalf("expected usage failure: %v", args)
		}
		var result struct {
			OK    bool  `json:"ok"`
			Error Error `json:"error"`
		}
		if json.Unmarshal(errOut.Bytes(), &result) != nil || result.OK || result.Error.Type != "usage" {
			t.Fatalf("invalid envelope: %s", errOut.String())
		}
	}
}

func TestEventsResponseFailures(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, code string
		status                 int
	}{
		{name: "missing calendar", path: "/user", body: `{"api_id":"usr-test"}`, code: "invalid_response"},
		{name: "bad entries", path: "/calendar/get-items", body: `{"entries":[{"api_id":"evt-one"}]}`, code: "invalid_response"},
		{name: "missing entries", path: "/calendar/get-items", body: `{}`, code: "invalid_response"},
		{name: "missing event", path: "/event/get", body: `{}`, code: "invalid_response"},
		{name: "bad timestamp", path: "/event/get", body: `{"event":` + strings.ReplaceAll(eventFixture, "2026-10-06T01:00:00Z", "bad") + `}`, code: "invalid_response"},
		{name: "expired", path: "/user", status: 401, body: `{"message":"usr-test.fake"}`, code: "session_rejected"},
		{name: "not found", path: "/event/get", status: 404, body: `{"message":"usr-test.fake"}`, code: "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authConfig(t)
			if err := auth.Save("usr-test.fake"); err != nil {
				t.Fatal(err)
			}
			eventsServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					if tc.status != 0 {
						w.WriteHeader(tc.status)
					}
					io.WriteString(w, tc.body)
					return
				}
				if r.URL.Path == "/user" {
					io.WriteString(w, `{"personal_calendar_api_id":"cal-personal"}`)
					return
				}
				t.Error("unexpected request")
			})
			args := []string{"events", "list"}
			if tc.path == "/event/get" {
				args = []string{"events", "get", "evt-one"}
			}
			var out, errOut bytes.Buffer
			if New("test", &out, &errOut).Run(args) != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), tc.code) || strings.Contains(errOut.String(), "usr-test.fake") {
				t.Fatalf("incorrect error: %s", errOut.String())
			}
		})
	}
}

func TestEventsEmptyListAndLimit(t *testing.T) {
	for _, body := range []string{`{"entries":[]}`, `{"entries":[{"event":` + eventFixture + `},{"event":` + eventFixture + `}]}`} {
		authConfig(t)
		if err := auth.Save("usr-test.fake"); err != nil {
			t.Fatal(err)
		}
		eventsServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/user" {
				io.WriteString(w, `{"personal_calendar_api_id":"cal-personal"}`)
			} else {
				io.WriteString(w, body)
			}
		})
		var out, errOut bytes.Buffer
		if New("test", &out, &errOut).Run([]string{"events", "list", "--limit=1"}) != 0 {
			t.Fatal(errOut.String())
		}
		var result struct {
			Data []eventOutput `json:"data"`
		}
		if json.Unmarshal(out.Bytes(), &result) != nil || result.Data == nil || len(result.Data) > 1 {
			t.Fatalf("incorrect list limit/empty array: %s", out.String())
		}
	}
}

func TestEventsMissingAuth(t *testing.T) {
	authConfig(t)
	eventsServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("missing credential dispatched") })
	var out, errOut bytes.Buffer
	if New("test", &out, &errOut).Run([]string{"events", "list"}) != 1 || !strings.Contains(errOut.String(), "missing_session_key") {
		t.Fatalf("incorrect missing auth: %s", errOut.String())
	}
}

func TestEventsRSVPStates(t *testing.T) {
	for _, status := range []string{"going", "not-going", "interested", "invited", "unknown"} {
		t.Run(status, func(t *testing.T) {
			authConfig(t)
			if err := auth.Save("usr-test.fake"); err != nil {
				t.Fatal(err)
			}
			eventsServer(t, func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"event":`+strings.ReplaceAll(eventFixture, "2026-10-06T01:00:00Z", "2026-10-05T18:00:00-07:00")+`,"rsvp_status":"`+status+`"}`)
			})
			var out, errOut bytes.Buffer
			if New("test", &out, &errOut).Run([]string{"events", "get", "evt-one"}) != 0 {
				t.Fatal(errOut.String())
			}
			var result struct {
				Data eventOutput `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Data.Start != "2026-10-06T01:00:00Z" {
				t.Fatal("timestamp was not normalized to UTC")
			}
			if status == "unknown" {
				if result.Data.MyRSVP != nil {
					t.Fatal("unknown RSVP should be null")
				}
			} else if result.Data.MyRSVP == nil || *result.Data.MyRSVP != status {
				t.Fatal("incorrect RSVP state")
			}
		})
	}
}
