package cli

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/KalebCole/luma-cli/internal/auth"
)

type eventOutput struct {
	EventID  string  `json:"eventId"`
	Title    string  `json:"title"`
	Start    string  `json:"start"`
	Timezone string  `json:"timezone"`
	Location string  `json:"location"`
	MyRSVP   *string `json:"myRsvp"`
	UserRole string  `json:"userRole"`
	URL      string  `json:"url"`
}

type eventDetails struct {
	eventOutput
	DescriptionMirror any `json:"descriptionMirror"`
}

func invalidEventResponse() error {
	return &Error{Type: "api", Code: "invalid_response", Message: "Luma returned incomplete or invalid event data."}
}

func object(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func textField(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := value[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func (c *CLI) events(opts Options, args []string) error {
	if len(args) == 0 {
		return usageError("missing_subcommand", "Use events list or events get <event-id>.")
	}
	switch args[0] {
	case "list":
		return c.eventsList(opts, args[1:])
	case "get":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			return usageError("invalid_event_id", "Use events get <event-id>.")
		}
		response, err := c.apiClient.Get("/event/get", url.Values{"event_api_id": {args[1]}})
		if err != nil {
			return err
		}
		root := object(response)
		event, err := normalizeEvent(root)
		if err != nil {
			return err
		}
		details := eventDetails{eventOutput: event, DescriptionMirror: root["description_mirror"]}
		if opts.JSON {
			return c.writeJSON(map[string]any{"ok": true, "data": details})
		}
		if err := c.writeEventsTable([]eventOutput{event}); err != nil {
			return err
		}
		if description := strings.TrimSpace(descriptionText(details.DescriptionMirror)); description != "" {
			_, err := fmt.Fprintf(c.out, "\n%s\n", description)
			return err
		}
		return nil
	default:
		return usageError("unknown_command", "Use events list or events get <event-id>.")
	}
}

// ProseMirror text is nested under content; render readable event details on a
// terminal while retaining the original document in JSON output.
func descriptionText(value any) string {
	node := object(value)
	text := textField(node, "text")
	if textField(node, "type") == "hard_break" {
		return "\n"
	}
	children, _ := node["content"].([]any)
	for _, child := range children {
		text += descriptionText(child)
	}
	switch textField(node, "type") {
	case "paragraph", "heading", "list_item":
		text += "\n"
	}
	return text
}

func (c *CLI) eventsList(opts Options, args []string) error {
	limit := 10
	upcoming, past := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--upcoming":
			upcoming = true
		case arg == "--past":
			past = true
		case arg == "--limit" || strings.HasPrefix(arg, "--limit="):
			value := strings.TrimPrefix(arg, "--limit=")
			if arg == "--limit" {
				i++
				if i >= len(args) {
					return usageError("invalid_limit", "--limit requires a positive integer.")
				}
				value = args[i]
			}
			var err error
			limit, err = strconv.Atoi(value)
			if err != nil || limit <= 0 {
				return usageError("invalid_limit", "--limit requires a positive integer.")
			}
		default:
			return usageError("unexpected_arguments", "Use events list [--upcoming | --past] [--limit <n>].")
		}
	}
	if upcoming && past {
		return usageError("conflicting_flags", "Choose either --upcoming or --past.")
	}
	period := "future"
	if past {
		period = "past"
	}
	response, err := c.apiClient.Get("/user", nil)
	if err != nil {
		return err
	}
	user := object(response)
	calendarID := textField(user, "personal_calendar_api_id")
	if calendarID == "" {
		calendarID = textField(object(user["user"]), "personal_calendar_api_id")
	}
	if calendarID == "" {
		return invalidEventResponse()
	}
	response, err = c.apiClient.Get("/calendar/get-items", url.Values{
		"calendar_api_id": {calendarID}, "period": {period}, "pagination_limit": {strconv.Itoa(limit)},
	})
	if err != nil {
		return err
	}
	entries, ok := object(response)["entries"].([]any)
	if !ok {
		return invalidEventResponse()
	}
	events := make([]eventOutput, 0, len(entries))
	for _, entry := range entries {
		if len(events) == limit {
			break
		}
		event, err := normalizeEvent(object(entry))
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	if opts.JSON {
		return c.writeJSON(map[string]any{"ok": true, "data": events})
	}
	return c.writeEventsTable(events)
}

// Calendar items wrap the event; membership and hosts belong to the wrapper.
func normalizeEvent(entry map[string]any) (eventOutput, error) {
	event := object(entry["event"])
	result := eventOutput{
		EventID: textField(event, "api_id"), Title: textField(event, "name", "title"),
		Timezone: textField(event, "timezone"), UserRole: "attendee",
	}
	start, err := time.Parse(time.RFC3339Nano, textField(event, "start_at", "start"))
	if err != nil || result.EventID == "" || result.Title == "" || result.Timezone == "" {
		return result, invalidEventResponse()
	}
	if _, err := time.LoadLocation(result.Timezone); err != nil {
		return result, invalidEventResponse()
	}
	result.Start = start.UTC().Format(time.RFC3339Nano)
	result.Location = textField(event, "location")
	if result.Location == "" {
		result.Location = textField(object(event["geo_address_info"]), "full_address", "address", "city")
	}
	if result.Location == "" {
		result.Location = textField(object(event["geo_address_json"]), "full_address", "address", "city")
	}
	slug := textField(event, "url", "slug")
	if slug == "" {
		slug = result.EventID
	}
	// Public URLs always use the current luma.com host.
	if parsed, err := url.Parse(slug); err == nil && parsed.IsAbs() {
		slug = strings.TrimPrefix(parsed.Path, "/")
	}
	result.URL = "https://luma.com/" + strings.TrimPrefix(slug, "/")
	status := textField(entry, "my_rsvp", "rsvp_status")
	for _, field := range []string{"guest_info", "event_guest", "guest", "registration_info"} {
		if status == "" {
			status = textField(object(entry[field]), "approval_status", "status", "rsvp_status")
		}
	}
	switch status {
	case "going", "approved", "registered":
		status = "going"
	case "not-going", "declined", "not_going":
		status = "not-going"
	case "interested", "invited":
	default:
		status = ""
	}
	if status != "" {
		result.MyRSVP = &status
	}
	if entry["is_host"] == true || textField(entry, "user_role", "role") == "host" {
		result.UserRole = "host"
	}
	key, err := auth.SessionKey()
	if err != nil {
		return result, authError(err)
	}
	userID, _, _ := strings.Cut(key, ".")
	hosts, _ := entry["hosts"].([]any)
	for _, value := range hosts {
		host := object(value)
		hostID := textField(host, "api_id", "user_api_id")
		if hostID == "" {
			hostID = textField(object(host["user"]), "api_id")
		}
		if userID != "" && hostID == userID {
			result.UserRole = "host"
		}
	}
	return result, nil
}

func (c *CLI) writeEventsTable(events []eventOutput) error {
	w := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "EVENT ID\tTITLE\tSTART\tTIMEZONE\tLOCATION\tMY RSVP\tROLE\tURL"); err != nil {
		return err
	}
	for _, event := range events {
		start, _ := time.Parse(time.RFC3339Nano, event.Start)
		zone, err := time.LoadLocation(event.Timezone)
		if err != nil {
			return invalidEventResponse()
		}
		rsvp := "—"
		if event.MyRSVP != nil {
			rsvp = *event.MyRSVP
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", event.EventID, event.Title, start.In(zone).Format("2006-01-02 15:04 MST"), event.Timezone, event.Location, rsvp, event.UserRole, event.URL); err != nil {
			return err
		}
	}
	return w.Flush()
}
