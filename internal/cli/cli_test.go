package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	c := New("1.2.3", &out, &errOut)
	if code := c.Run([]string{"--json", "--version"}); code != 0 || out.String() != "1.2.3\n" || errOut.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestSchemaMatchesSpec(t *testing.T) {
	spec, err := os.ReadFile("../../spec/commands.md")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, match := range regexp.MustCompile(`luma (--version|schema|doctor|auth [a-z]+|events [a-z]+|rsvp [a-z]+)`).FindAllStringSubmatch(string(spec), -1) {
		want[match[1]] = true
	}
	var out, errOut bytes.Buffer
	c := New("test", &out, &errOut)
	c.isTTY = true // schema must still be JSON on a terminal.
	if code := c.Run([]string{"--dry-run", "schema", "--json"}); code != 0 {
		t.Fatalf("code=%d: %s", code, &errOut)
	}
	var result struct {
		Commands []commandSpec `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, cmd := range result.Commands {
		if !want[cmd.Name] || seen[cmd.Name] {
			t.Fatalf("unexpected or repeated command %q", cmd.Name)
		}
		seen[cmd.Name] = true
		if cmd.Mutation && !cmd.DryRun {
			t.Fatalf("mutation %q lacks dry-run", cmd.Name)
		}
		if cmd.Implemented != (cmd.Name == "--version" || cmd.Name == "schema" || cmd.Name == "doctor" || strings.HasPrefix(cmd.Name, "auth ")) {
			t.Fatalf("wrong implementation status for %q", cmd.Name)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("catalog=%v spec=%v", seen, want)
	}
}

func TestErrorsAreJSONAndRedacted(t *testing.T) {
	for _, args := range [][]string{{"schema", "--unknown=usr-test.secret"}, {"schema", "usr-test.secret"}, {"usr-test.secret"}} {
		var out, errOut bytes.Buffer
		c := New("test", &out, &errOut)
		c.isTTY = true
		if code := c.Run(args); code != 1 {
			t.Fatalf("code=%d", code)
		}
		var envelope struct {
			OK    bool  `json:"ok"`
			Error Error `json:"error"`
		}
		if err := json.Unmarshal(errOut.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.OK || envelope.Error.Type != "usage" || envelope.Error.Code == "" || envelope.Error.Message == "" {
			t.Fatalf("bad envelope: %+v", envelope)
		}
		if out.Len() != 0 || strings.Contains(errOut.String(), "usr-test.secret") {
			t.Fatal("unexpected output or leaked credential")
		}
	}
}

func TestDoctorChecks(t *testing.T) {
	const key = "usr-test.secret"
	cases := []struct {
		name, key, body, auth, connectivity string
		status                              int
		fail                                bool
	}{
		{name: "missing", status: 401, auth: "missing", connectivity: "reachable"},
		{name: "invalid", key: "api-key", status: 401, auth: "invalid", connectivity: "reachable"},
		{name: "authenticated", key: key, status: 200, body: `{"user":{"api_id":"usr-test"}}`, auth: "authenticated", connectivity: "reachable"},
		{name: "flat identity", key: key, status: 200, body: `{"api_id":"usr-test"}`, auth: "authenticated", connectivity: "reachable"},
		{name: "expired", key: key, status: 401, body: key, auth: "rejected", connectivity: "reachable"},
		{name: "forbidden", key: key, status: 403, auth: "rejected", connectivity: "reachable"},
		{name: "anonymous", key: key, status: 200, body: `{"user":null}`, auth: "unverified", connectivity: "reachable"},
		{name: "malformed response", key: key, status: 200, body: key, auth: "unverified", connectivity: "reachable"},
		{name: "server failure", key: key, status: 503, auth: "unverified", connectivity: "unhealthy"},
		{name: "redirect", key: key, status: 302, auth: "unverified", connectivity: "unhealthy"},
		{name: "offline", key: key, fail: true, auth: "unverified", connectivity: "unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newDoctorClient()
			client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.String() != doctorURL {
					t.Fatal("unexpected request")
				}
				cookie, err := req.Cookie("luma.auth-session-key")
				if sessionKeyPattern.MatchString(tc.key) {
					if err != nil || cookie.Value != tc.key {
						t.Fatal("missing session cookie")
					}
				} else if err == nil {
					t.Fatal("invalid credential was sent")
				}
				if tc.fail {
					return nil, errors.New(key)
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{"https://example.com/"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
			})
			r := checkDoctor(context.Background(), client, doctorURL, tc.key)
			if r.Authentication.Status != tc.auth || r.Connectivity.Status != tc.connectivity {
				t.Fatalf("report=%+v", r)
			}
			if r.Healthy != (tc.auth == "authenticated") {
				t.Fatalf("healthy=%v", r.Healthy)
			}
			encoded, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte(key)) {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestDoctorOutputModesAndExitCode(t *testing.T) {
	authConfig(t)
	for _, tc := range []struct {
		name string
		tty  bool
		args []string
		json bool
	}{
		{name: "pipe", args: []string{"doctor"}, json: true},
		{name: "terminal", tty: true, args: []string{"doctor"}},
		{name: "forced JSON", tty: true, args: []string{"doctor", "--json"}, json: true},
		{name: "global flags", args: []string{"--json", "--dry-run", "doctor"}, json: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			c := New("test", &out, &errOut)
			c.isTTY = tc.tty
			c.doctorClient.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) { return nil, errors.New("offline") })
			if code := c.Run(tc.args); code != 0 || errOut.Len() != 0 {
				t.Fatalf("code=%d stderr=%s", code, &errOut)
			}
			if tc.json {
				var envelope struct {
					OK   bool         `json:"ok"`
					Data doctorReport `json:"data"`
				}
				if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if !envelope.OK || envelope.Data.Healthy || envelope.Data.Authentication.Status != "missing" || envelope.Data.Connectivity.Status != "unreachable" {
					t.Fatalf("bad report: %+v", envelope)
				}
			} else if !strings.Contains(out.String(), "CHECK") || !strings.Contains(out.String(), "Authentication") {
				t.Fatalf("missing human table: %s", &out)
			}
		})
	}
}
