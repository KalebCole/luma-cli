package cli

import (
	"fmt"
	"net/url"
	"strings"
)

type registration struct {
	Name        string                     `json:"name"`
	FirstName   string                     `json:"first_name"`
	LastName    string                     `json:"last_name"`
	Email       string                     `json:"email"`
	EventID     string                     `json:"event_api_id"`
	ForWaitlist bool                       `json:"for_waitlist"`
	Tickets     map[string]ticketSelection `json:"ticket_type_to_selection"`
}

type ticketSelection struct {
	Count  int `json:"count"`
	Amount int `json:"amount"`
}

func (c *CLI) rsvp(opts Options, args []string) error {
	if len(args) == 0 {
		return usageError("missing_subcommand", "Use rsvp get or rsvp set.")
	}
	action := args[0]
	if action != "get" && action != "set" {
		return usageError("unknown_command", "Use rsvp get or rsvp set.")
	}
	var eventID, status string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if action == "set" && (arg == "--status" || strings.HasPrefix(arg, "--status=")) {
			if status != "" {
				return usageError("duplicate_status", "Specify --status only once.")
			}
			if arg == "--status" {
				if i+1 >= len(args) {
					return usageError("missing_status", "--status is required.")
				}
				i++
				status = args[i]
			} else {
				status = strings.TrimPrefix(arg, "--status=")
			}
			if status != "going" && status != "not-going" && status != "interested" {
				return usageError("invalid_status", "--status must be going, not-going, or interested.")
			}
		} else {
			if eventID != "" {
				return usageError("unexpected_arguments", "Specify exactly one event ID.")
			}
			// An event ID is opaque, but must be a nonempty single token.
			if strings.TrimSpace(arg) == "" || strings.ContainsAny(arg, " \t\r\n") || strings.HasPrefix(arg, "-") {
				return usageError("invalid_event_id", "Specify a nonempty event ID without whitespace.")
			}
			eventID = arg
		}
	}
	if eventID == "" {
		return usageError("missing_event_id", "An event ID is required.")
	}
	if action == "set" {
		if status == "" {
			return usageError("missing_status", "--status is required.")
		}
		if status != "going" {
			return &Error{Type: "unsupported", Code: "rsvp_status_unverified", Message: "The guest RSVP endpoint for not-going and interested is unverified. Capture it from the browser network tab during an RSVP on luma.com."}
		}
	}
	response, err := c.apiClient.Get("/event/get", url.Values{"event_api_id": {eventID}})
	if err != nil {
		return err
	}
	event, ok := response.(map[string]any)
	if !ok {
		return invalidRSVPResponse()
	}
	if action == "get" {
		state, err := rsvpState(event)
		if err != nil {
			return err
		}
		if opts.JSON {
			return c.writeJSON(map[string]any{"ok": true, "data": map[string]any{"eventId": eventID, "myRsvp": state}})
		}
		label := "none"
		if state != nil {
			label = *state
		}
		_, err = fmt.Fprintf(c.out, "Event: %s\nRSVP: %s\n", eventID, label)
		return err
	}
	body, err := c.registration(eventID, event)
	if err != nil {
		return err
	}
	if opts.DryRun {
		// Always emit the complete normalized request, including on a terminal.
		return c.writeJSON(map[string]any{"ok": true, "data": map[string]any{
			"eventId": eventID, "intent": status, "submitted": false, "dryRun": true,
			"request": map[string]any{"method": "POST", "url": "https://api.luma.com/event/register", "headers": map[string]string{"Accept": "application/json", "Content-Type": "application/json", "Origin": "https://luma.com", "x-luma-client-type": "luma-web", "Cookie": "[REDACTED]"}, "body": body},
		}})
	}
	result, err := c.apiClient.Post("/event/register", body)
	if err != nil {
		return err
	}
	object, ok := result.(map[string]any)
	if !ok || object["status"] != "success" {
		return &Error{Type: "api", Code: "rsvp_registration_failed", Message: "Luma did not confirm registration with status:success."}
	}
	if opts.JSON {
		return c.writeJSON(map[string]any{"ok": true, "data": map[string]any{"eventId": eventID, "intent": status, "submitted": true}})
	}
	_, err = fmt.Fprintf(c.out, "Event: %s\nRSVP intent: going\nSubmitted: yes\n", eventID)
	return err
}

func invalidRSVPResponse() error {
	return &Error{Type: "api", Code: "invalid_response", Message: "Luma returned an unexpected RSVP response shape."}
}

// The current guest belongs to the response root, not the event object.
func rsvpState(event map[string]any) (*string, error) {
	raw := event["guest_data"]
	if raw == nil {
		return nil, nil
	}
	guest, ok := raw.(map[string]any)
	if !ok {
		return nil, invalidRSVPResponse()
	}
	for _, field := range []string{"rsvp_status", "status", "approval_status"} {
		raw := guest[field]
		if raw == nil {
			continue
		}
		state, ok := raw.(string)
		if !ok {
			return nil, invalidRSVPResponse()
		}
		switch state {
		case "going", "not-going", "interested", "invited":
		case "approved":
			state = "going"
		case "declined":
			state = "not-going"
		default:
			return nil, invalidRSVPResponse()
		}
		return &state, nil
	}
	return nil, nil
}

func (c *CLI) registration(eventID string, event map[string]any) (registration, error) {
	body := registration{EventID: eventID}
	tickets, ok := event["ticket_types"].([]any)
	if !ok || len(tickets) == 0 {
		return body, &Error{Type: "api", Code: "missing_ticket_type", Message: "Luma did not return a ticket type for registration."}
	}
	ticket, ok := tickets[0].(map[string]any)
	if !ok {
		return body, invalidRSVPResponse()
	}
	ticketID, ok := ticket["api_id"].(string)
	if !ok || strings.TrimSpace(ticketID) == "" {
		return body, invalidRSVPResponse()
	}
	body.Tickets = map[string]ticketSelection{ticketID: {Count: 1, Amount: 0}}
	response, err := c.apiClient.Get("/user", nil)
	if err != nil {
		return body, err
	}
	user, ok := response.(map[string]any)
	if !ok {
		return body, invalidRSVPResponse()
	}
	if nested, exists := user["user"]; exists {
		user, ok = nested.(map[string]any)
		if !ok {
			return body, invalidRSVPResponse()
		}
	}
	body.Name, _ = user["name"].(string)
	body.FirstName, _ = user["first_name"].(string)
	body.LastName, _ = user["last_name"].(string)
	body.Email, _ = user["email"].(string)
	if body.Name == "" {
		body.Name = strings.TrimSpace(body.FirstName + " " + body.LastName)
	}
	if body.FirstName == "" {
		parts := strings.SplitN(strings.TrimSpace(body.Name), " ", 2)
		body.FirstName = parts[0]
		if len(parts) == 2 && body.LastName == "" {
			body.LastName = parts[1]
		}
	}
	if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Email) == "" {
		return body, &Error{Type: "api", Code: "missing_user_identity", Message: "Luma did not return the name and email required for registration."}
	}
	return body, nil
}
