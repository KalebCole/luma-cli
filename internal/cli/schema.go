package cli

type flagSpec struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Required    bool     `json:"required,omitempty"`
	Values      []string `json:"values,omitempty"`
}

type commandSpec struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Arguments   []string   `json:"arguments,omitempty"`
	Flags       []flagSpec `json:"flags,omitempty"`
	Mutation    bool       `json:"mutation"`
	DryRun      bool       `json:"supportsDryRun"`
	Implemented bool       `json:"implemented"`
}

// catalog mirrors spec/commands.md, including approved commands that will be
// implemented in later stages. Flag variants are not separate commands.
var catalog = []commandSpec{
	{Name: "--version", Description: "Print version and exit 0.", Implemented: true},
	{Name: "schema", Description: "Print the machine-readable command catalog as JSON and exit 0.", Implemented: true},
	{Name: "doctor", Description: "Check authentication and connectivity without mutations and exit 0.", Implemented: true},
	{Name: "auth login", Description: "Store the browser session key from LUMA_AUTH_SESSION_KEY or --key without prompts.", Implemented: true, Mutation: true, DryRun: true, Flags: []flagSpec{{Name: "--key", Type: "string", Description: "Browser session key (usr-<id>.<secret>)."}}},
	{Name: "auth status", Description: "Report authentication status and expiry if known.", Implemented: true},
	{Name: "auth logout", Description: "Clear the stored credential.", Implemented: true, Mutation: true, DryRun: true},
	{Name: "events list", Description: "List invitations and RSVPs.", Implemented: true, Flags: []flagSpec{
		{Name: "--upcoming", Type: "boolean", Description: "List upcoming invitations and RSVPs."},
		{Name: "--past", Type: "boolean", Description: "List event history."},
		{Name: "--limit", Type: "integer", Description: "Maximum number of events."},
	}},
	{Name: "events get", Description: "Get full event details.", Implemented: true, Arguments: []string{"event-id"}},
	{Name: "rsvp get", Description: "Get your current RSVP state.", Implemented: true, Arguments: []string{"event-id"}},
	{Name: "rsvp set", Description: "Register as going or decline as not-going; interested returns rsvp_status_unverified because Luma has no interested state.", Implemented: true, Arguments: []string{"event-id"}, Mutation: true, DryRun: true, Flags: []flagSpec{{Name: "--status", Type: "string", Description: "Requested RSVP state.", Required: true, Values: []string{"going", "not-going", "interested"}}, {Name: "--message", Type: "string", Description: "Optional message to the host when declining with --status not-going."}}},
}

func (c *CLI) schema(_ Options, args []string) error {
	if err := noArguments(args); err != nil {
		return err
	}
	return c.writeJSON(struct {
		SchemaVersion int           `json:"schemaVersion"`
		Name          string        `json:"name"`
		Version       string        `json:"version"`
		GlobalFlags   []flagSpec    `json:"globalFlags"`
		Commands      []commandSpec `json:"commands"`
	}{
		SchemaVersion: 1, Name: "luma", Version: c.version,
		GlobalFlags: []flagSpec{
			{Name: "--json", Type: "boolean", Description: "Force JSON output; JSON is the default when stdout is not a TTY."},
			{Name: "--dry-run", Type: "boolean", Description: "Validate and print the normalized request without dispatching (mutations only)."},
		},
		Commands: catalog,
	})
}
