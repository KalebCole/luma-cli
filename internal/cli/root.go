// Package cli implements the Luma command line and its output conventions.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"golang.org/x/term"
)

// Options are shared by all command handlers. Mutations must honor DryRun
// before dispatching a request.
type Options struct {
	JSON   bool
	DryRun bool
}

type command struct {
	name string
	run  func(Options, []string) error
}

// CLI owns command registration and output. New command groups can register
// handlers here while keeping their implementation in separate files.
type CLI struct {
	version      string
	out          io.Writer
	errOut       io.Writer
	isTTY        bool
	commands     []command
	doctorClient *http.Client
}

func New(version string, stdout, stderr io.Writer) *CLI {
	c := &CLI{version: version, out: stdout, errOut: stderr, doctorClient: newDoctorClient()}
	if f, ok := stdout.(*os.File); ok {
		c.isTTY = term.IsTerminal(int(f.Fd()))
	}
	c.commands = []command{
		{name: "schema", run: c.schema},
		{name: "doctor", run: c.doctor},
		{name: "auth", run: c.auth},
	}
	return c
}

// Run returns a process exit code. Command errors always use a JSON envelope
// on stderr, including when stdout is a terminal.
func (c *CLI) Run(args []string) int {
	if err := c.execute(args); err != nil {
		e, ok := err.(*Error)
		if !ok {
			e = &Error{Type: "io", Code: "output_failed", Message: "Unable to write command output."}
		}
		_ = json.NewEncoder(c.errOut).Encode(struct {
			OK    bool   `json:"ok"`
			Error *Error `json:"error"`
		}{Error: e})
		return 1
	}
	return 0
}

// Error is the public error representation. Messages must never contain
// credentials, request headers, or untrusted server response bodies.
type Error struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func usageError(code, message string) error {
	return &Error{Type: "usage", Code: code, Message: message}
}

func (c *CLI) execute(args []string) error {
	opts := Options{JSON: !c.isTTY}
	var positional []string
	var version, help, endFlags bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if endFlags {
			positional = append(positional, arg)
			continue
		}
		switch arg {
		case "--":
			endFlags = true
		case "--json", "--json=true":
			opts.JSON = true
		case "--json=false":
			opts.JSON = !c.isTTY
		case "--dry-run", "--dry-run=true":
			opts.DryRun = true
		case "--dry-run=false":
			opts.DryRun = false
		case "--version":
			version = true
		case "--help", "-h":
			help = true
		default:
			if len(positional) == 2 && positional[0] == "auth" && positional[1] == "login" && (arg == "--key" || strings.HasPrefix(arg, "--key=")) {
				positional = append(positional, arg)
				if arg == "--key" {
					if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
						return usageError("missing_key", "--key requires a browser session key.")
					}
					i++
					positional = append(positional, args[i])
				}
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return usageError("unknown_flag", "Unknown flag. Use --help for available flags.")
			}
			positional = append(positional, arg)
		}
	}
	if version {
		_, err := fmt.Fprintln(c.out, c.version)
		return err
	}
	if len(positional) == 0 {
		return c.help(opts)
	}
	for _, cmd := range c.commands {
		if positional[0] == cmd.name {
			if help {
				return c.help(opts)
			}
			return cmd.run(opts, positional[1:])
		}
	}
	return usageError("unknown_command", "Unknown command. Use --help for available commands.")
}

func (c *CLI) help(opts Options) error {
	if opts.JSON {
		return c.writeJSON(map[string]any{
			"ok": true,
			"data": map[string]any{
				"usage":    "luma [--json] [--dry-run] <command>",
				"commands": []string{"schema", "doctor", "auth"},
				"flags":    []string{"--version", "--json", "--dry-run", "--help"},
			},
		})
	}
	_, err := fmt.Fprintln(c.out, "Usage: luma [--json] [--dry-run] <command>\n\nCommands:\n  schema   Print the full v1 command catalog as JSON\n  doctor   Check authentication and connectivity (read-only)\n  auth     login [--key <key>], status, logout\n\nFlags:\n  --version   Print version\n  --json      Force JSON output\n  --dry-run   Validate without dispatching (mutations only)\n  --help, -h  Show help")
	return err
}

func (c *CLI) writeJSON(value any) error {
	return json.NewEncoder(c.out).Encode(value)
}

func noArguments(args []string) error {
	if len(args) != 0 {
		return usageError("unexpected_arguments", "This command does not accept positional arguments.")
	}
	return nil
}
