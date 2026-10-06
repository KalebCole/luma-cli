package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/KalebCole/luma-cli/internal/auth"
)

func authError(err error) error {
	if errors.Is(err, auth.ErrInvalidKey) {
		return &Error{Type: "auth", Code: "invalid_session_key", Message: auth.ErrInvalidKey.Error()}
	}
	return &Error{Type: "io", Code: "credential_store_failed", Message: auth.ErrStore.Error()}
}

func (c *CLI) auth(opts Options, args []string) error {
	if len(args) == 0 {
		return usageError("missing_subcommand", "Use auth login, auth status, or auth logout.")
	}
	switch args[0] {
	case "login":
		key := os.Getenv("LUMA_AUTH_SESSION_KEY")
		if len(args) > 1 {
			switch {
			case len(args) == 3 && args[1] == "--key":
				key = args[2]
			case len(args) == 2 && strings.HasPrefix(args[1], "--key="):
				key = strings.TrimPrefix(args[1], "--key=")
			default:
				return usageError("unexpected_arguments", "Use auth login [--key <key>].")
			}
		}
		if err := auth.Validate(key); err != nil {
			return authError(err)
		}
		if opts.DryRun {
			return c.authMutationOutput(opts, "login", false)
		}
		if err := auth.Save(key); err != nil {
			return authError(err)
		}
		return c.authMutationOutput(opts, "login", true)
	case "logout":
		if err := noArguments(args[1:]); err != nil {
			return err
		}
		if opts.DryRun {
			return c.authMutationOutput(opts, "logout", false)
		}
		if err := auth.Logout(); err != nil {
			return authError(err)
		}
		return c.authMutationOutput(opts, "logout", true)
	case "status":
		if err := noArguments(args[1:]); err != nil {
			return err
		}
		key, err := auth.SessionKey()
		if err != nil {
			return authError(err)
		}
		authenticated := false
		if key != "" {
			req, err := http.NewRequest(http.MethodGet, doctorURL, nil)
			if err != nil {
				return &Error{Type: "network", Code: "auth_check_failed", Message: "Unable to check authentication with Luma."}
			}
			req.Header.Set("Accept", "application/json")
			req.AddCookie(&http.Cookie{Name: "luma.auth-session-key", Value: key})
			resp, err := c.doctorClient.Do(req)
			if err != nil {
				return &Error{Type: "network", Code: "auth_check_failed", Message: "Unable to check authentication with Luma."}
			}
			authenticated = resp.StatusCode == http.StatusOK && hasUserIdentity(resp.Body)
			resp.Body.Close()
		}
		if opts.JSON {
			return c.writeJSON(map[string]any{"ok": true, "data": map[string]any{"authenticated": authenticated}})
		}
		status := "no"
		if authenticated {
			status = "yes"
		}
		_, err = fmt.Fprintf(c.out, "Authenticated: %s\n", status)
		return err
	default:
		return usageError("unknown_command", "Use auth login, auth status, or auth logout.")
	}
}

func (c *CLI) authMutationOutput(opts Options, action string, submitted bool) error {
	if opts.JSON {
		data := map[string]any{"action": action, "submitted": submitted}
		if opts.DryRun {
			data["dryRun"] = true
			if action == "login" {
				data["sessionKey"] = "[REDACTED]"
			}
		}
		return c.writeJSON(map[string]any{"ok": true, "data": data})
	}
	message := "Stored browser session credential."
	if action == "logout" {
		message = "Cleared stored browser session credential."
	}
	if opts.DryRun {
		message = "Dry run: auth " + action + " (credential redacted; no changes)."
	}
	_, err := fmt.Fprintln(c.out, message)
	return err
}
