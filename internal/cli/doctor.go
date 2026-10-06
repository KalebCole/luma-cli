package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"text/tabwriter"
	"time"

	"github.com/KalebCole/luma-cli/internal/auth"
)

const doctorURL = "https://api.luma.com/user"

var sessionKeyPattern = regexp.MustCompile(`^usr-[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

type authCheck struct {
	Status        string `json:"status"`
	Authenticated bool   `json:"authenticated"`
	Message       string `json:"message"`
}

type connectivityCheck struct {
	Status     string `json:"status"`
	URL        string `json:"url"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	Message    string `json:"message"`
}

type doctorReport struct {
	Healthy        bool              `json:"healthy"`
	Authentication authCheck         `json:"authentication"`
	Connectivity   connectivityCheck `json:"connectivity"`
}

func newDoctorClient() *http.Client {
	// Never follow redirects with a session cookie, even to another Luma host.
	return &http.Client{
		Timeout:       8 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (c *CLI) doctor(opts Options, args []string) error {
	if err := noArguments(args); err != nil {
		return err
	}
	key := os.Getenv("LUMA_AUTH_SESSION_KEY")
	var storeErr error
	if key == "" {
		key, storeErr = auth.SessionKey()
	}
	report := checkDoctor(context.Background(), c.doctorClient, doctorURL, key)
	if storeErr != nil {
		report.Authentication = authCheck{Status: "invalid", Message: "Unable to read the local credential store securely."}
		report.Healthy = false
	}
	if opts.JSON {
		return c.writeJSON(struct {
			OK   bool         `json:"ok"`
			Data doctorReport `json:"data"`
		}{OK: true, Data: report})
	}
	w := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintf(w, "CHECK\tSTATUS\tDETAIL\nAuthentication\t%s\t%s\nConnectivity\t%s\t%s\n", report.Authentication.Status, report.Authentication.Message, report.Connectivity.Status, report.Connectivity.Message); err != nil {
		return err
	}
	return w.Flush()
}

// checkDoctor reports diagnostic failures as data: doctor itself exits 0.
// A successful HTTP response alone does not establish authentication; /user
// must return a user identity. No response bodies or transport errors are
// echoed, since either could contain credential information.
func checkDoctor(ctx context.Context, client *http.Client, endpoint, key string) doctorReport {
	r := doctorReport{
		Authentication: authCheck{Status: "missing", Message: "No browser session credential is available."},
		Connectivity:   connectivityCheck{Status: "unreachable", URL: endpoint, Message: "Unable to reach the Luma API."},
	}
	validKey := sessionKeyPattern.MatchString(key)
	if key != "" {
		r.Authentication = authCheck{Status: "invalid", Message: "LUMA_AUTH_SESSION_KEY must be a browser session cookie shaped usr-<id>.<secret>."}
		if validKey {
			r.Authentication = authCheck{Status: "unverified", Message: "Session key is present; authentication could not be verified."}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return r
	}
	req.Header.Set("Accept", "application/json")
	if validKey {
		req.AddCookie(&http.Cookie{Name: "luma.auth-session-key", Value: key})
	}
	resp, err := client.Do(req)
	if err != nil {
		return r
	}
	defer resp.Body.Close()
	r.Connectivity.HTTPStatus = resp.StatusCode
	r.Connectivity.Status = "reachable"
	r.Connectivity.Message = "Luma API responded."
	if resp.StatusCode >= 300 && resp.StatusCode < 400 || resp.StatusCode >= 500 {
		r.Connectivity.Status = "unhealthy"
		r.Connectivity.Message = "Luma API returned an unexpected HTTP status."
	}
	if validKey {
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			r.Authentication = authCheck{Status: "rejected", Message: "Luma rejected the session key; it may have expired."}
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if hasUserIdentity(resp.Body) {
				r.Authentication = authCheck{Status: "authenticated", Authenticated: true, Message: "Luma accepted the browser session key."}
			} else {
				r.Authentication.Message = "Luma did not return a verifiable user identity."
			}
		}
	}
	r.Healthy = r.Authentication.Authenticated && r.Connectivity.Status == "reachable"
	return r
}

// hasUserIdentity shares bounded response validation between auth status and
// doctor. Anonymous or malformed responses cannot establish authentication.
func hasUserIdentity(reader io.Reader) bool {
	var body struct {
		APIID string `json:"api_id"`
		User  *struct {
			APIID string `json:"api_id"`
		} `json:"user"`
	}
	return json.NewDecoder(io.LimitReader(reader, 1<<20)).Decode(&body) == nil &&
		(body.APIID != "" || body.User != nil && body.User.APIID != "")
}
