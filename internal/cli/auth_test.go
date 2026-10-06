package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/KalebCole/luma-cli/internal/auth"
)

func authConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("LUMA_AUTH_SESSION_KEY", "")
}

func TestAuthMutations(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{false: "JSON", true: "terminal"}[tty], func(t *testing.T) {
			authConfig(t)
			t.Setenv("LUMA_AUTH_SESSION_KEY", "usr-env.fake")
			for _, args := range [][]string{
				{"auth", "login", "--dry-run"},
				{"auth", "login"},
				{"--json", "auth", "login", "--key", "usr-flag.fake"},
				{"auth", "login", "--key=usr-equals.fake", "--json"},
				{"auth", "logout", "--dry-run"},
				{"auth", "logout"},
				{"auth", "logout"},
			} {
				before, err := auth.SessionKey()
				if err != nil {
					t.Fatal(err)
				}
				var out, errOut bytes.Buffer
				c := New("test", &out, &errOut)
				c.isTTY = tty
				if code := c.Run(args); code != 0 || errOut.Len() != 0 {
					t.Fatalf("code=%d stderr=%s", code, &errOut)
				}
				if strings.Contains(out.String(), "usr-") {
					t.Fatal("output leaked credential")
				}
				if !tty || strings.Contains(strings.Join(args, " "), "--json") {
					if !json.Valid(out.Bytes()) {
						t.Fatal("output is not JSON")
					}
				}
				after, err := auth.SessionKey()
				if err != nil {
					t.Fatal(err)
				}
				dry := strings.Contains(strings.Join(args, " "), "--dry-run")
				if dry && before != after {
					t.Fatal("dry run changed credential")
				}
				if !dry {
					want := "usr-env.fake"
					if strings.Contains(strings.Join(args, " "), "usr-flag.fake") {
						want = "usr-flag.fake"
					}
					if strings.Contains(strings.Join(args, " "), "usr-equals.fake") {
						want = "usr-equals.fake"
					}
					if strings.Contains(strings.Join(args, " "), "logout") {
						want = ""
					}
					if after != want {
						t.Fatal("incorrect stored credential")
					}
				}
			}
		})
	}
}

func TestAuthFailureEnvelopes(t *testing.T) {
	authConfig(t)
	for _, args := range [][]string{
		{"auth"}, {"auth", "unknown"}, {"auth", "login"},
		{"auth", "login", "--key"}, {"auth", "login", "--key="},
		{"auth", "login", "--key", "invalid-secret"},
		{"auth", "login", "--key=invalid-secret", "--dry-run"},
		{"auth", "login", "extra"}, {"auth", "status", "invalid-secret"},
		{"auth", "logout", "invalid-secret"}, {"auth", "status", "--key=invalid-secret"},
	} {
		var out, errOut bytes.Buffer
		c := New("test", &out, &errOut)
		if c.Run(args) != 1 {
			t.Fatal("expected command failure")
		}
		var result struct {
			OK    bool  `json:"ok"`
			Error Error `json:"error"`
		}
		if err := json.Unmarshal(errOut.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.OK || result.Error.Code == "" || out.Len() != 0 || strings.Contains(errOut.String(), "invalid-secret") {
			t.Fatal("invalid or unsafe error output")
		}
	}
	// A filesystem failure must use the same safe envelope.
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/luma", []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	c := New("test", &out, &errOut)
	if c.Run([]string{"auth", "login", "--key=usr-test.fake"}) != 1 || !strings.Contains(errOut.String(), "credential_store_failed") || strings.Contains(errOut.String(), "usr-test.fake") {
		t.Fatal("unsafe store failure")
	}
}

func TestAuthStatus(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body          string
		status        int
		key           bool
		offline       bool
		authenticated bool
	}{
		{name: "missing"},
		{name: "valid nested identity", key: true, status: 200, body: `{"user":{"api_id":"usr-test"}}`, authenticated: true},
		{name: "valid flat identity", key: true, status: 200, body: `{"api_id":"usr-test"}`, authenticated: true},
		{name: "anonymous", key: true, status: 200, body: `{"user":null}`},
		{name: "missing identity", key: true, status: 200, body: `{}`},
		{name: "empty identity", key: true, status: 200, body: `{"user":{"api_id":""}}`},
		{name: "malformed JSON", key: true, status: 200, body: "usr-test.fake"},
		{name: "empty body", key: true, status: 200},
		{name: "wrong identity type", key: true, status: 200, body: `{"api_id":123}`},
		{name: "expired", key: true, status: 401}, {name: "forbidden", key: true, status: 403},
		{name: "redirect", key: true, status: 302}, {name: "server failure", key: true, status: 503},
		{name: "other success", key: true, status: 204}, {name: "offline", key: true, offline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authConfig(t)
			if tc.key {
				if err := auth.Save("usr-test.fake"); err != nil {
					t.Fatal(err)
				}
			}
			var out, errOut bytes.Buffer
			c := New("test", &out, &errOut)
			calls := 0
			c.doctorClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				cookie, err := req.Cookie("luma.auth-session-key")
				if err != nil || cookie.Value != "usr-test.fake" || req.Method != "GET" || req.URL.String() != doctorURL {
					t.Fatal("incorrect authentication request")
				}
				if tc.offline {
					return nil, errors.New("usr-test.fake")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header), Request: req}, nil
			})
			code := c.Run([]string{"auth", "status"})
			if tc.offline {
				if code != 1 || !strings.Contains(errOut.String(), "auth_check_failed") {
					t.Fatal("expected safe network error")
				}
			} else {
				var result struct {
					OK   bool `json:"ok"`
					Data struct {
						Authenticated bool `json:"authenticated"`
					} `json:"data"`
				}
				if code != 0 || json.Unmarshal(out.Bytes(), &result) != nil || !result.OK || result.Data.Authenticated != tc.authenticated {
					t.Fatal("incorrect auth status")
				}
			}
			if calls != map[bool]int{false: 0, true: 1}[tc.key] {
				t.Fatal("unexpected network request")
			}
			if strings.Contains(out.String()+errOut.String(), "usr-test.fake") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestAuthClientDoesNotFollowRedirects(t *testing.T) {
	calls := 0
	client := newDoctorClient()
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://example.com/redirected"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	req, err := http.NewRequest(http.MethodGet, doctorURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "luma.auth-session-key", Value: "usr-test.fake"})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if calls != 1 || resp.StatusCode != 302 {
		t.Fatal("followed credential-bearing redirect")
	}
}

func TestAuthStatusOutputModes(t *testing.T) {
	authConfig(t)
	for _, forceJSON := range []bool{false, true} {
		var out, errOut bytes.Buffer
		c := New("test", &out, &errOut)
		c.isTTY = true
		args := []string{"auth", "status"}
		if forceJSON {
			args = append(args, "--json")
		}
		if c.Run(args) != 0 || errOut.Len() != 0 {
			t.Fatal("status failed")
		}
		if forceJSON {
			if !json.Valid(out.Bytes()) {
				t.Fatal("forced JSON missing")
			}
		} else if out.String() != "Authenticated: no\n" {
			t.Fatal("human status missing")
		}
	}
}

func TestDoctorUsesStoredCredentialAndEnvironmentOverride(t *testing.T) {
	authConfig(t)
	if err := auth.Save("usr-stored.fake"); err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"", "usr-env.fake"} {
		t.Setenv("LUMA_AUTH_SESSION_KEY", env)
		var out, errOut bytes.Buffer
		c := New("test", &out, &errOut)
		c.doctorClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			want := env
			if want == "" {
				want = "usr-stored.fake"
			}
			cookie, err := req.Cookie("luma.auth-session-key")
			if err != nil || cookie.Value != want {
				t.Fatal("doctor used incorrect credential")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"api_id":"usr-test"}`)), Header: make(http.Header), Request: req}, nil
		})
		if c.Run([]string{"doctor"}) != 0 || !strings.Contains(out.String(), `"healthy":true`) || errOut.Len() != 0 {
			t.Fatal("doctor did not recognize credential")
		}
		if strings.Contains(out.String(), ".fake") {
			t.Fatal("doctor leaked credential")
		}
	}
}
