// Command ecommerce is the repository's developer CLI.
//
// It wraps the handful of Bazel invocations that every contributor needs, so
// that the correct flags live in one place instead of being copy-pasted out of
// the README and into shell history.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// OutputFormat selects how a command renders its result.
type OutputFormat string

const (
	// FormatText is human-readable output.
	FormatText OutputFormat = "text"
	// FormatJSON is machine-readable output.
	FormatJSON OutputFormat = "json"
)

// Command is a single CLI subcommand.
type Command struct {
	Name        string
	Description string
	Run         func(args []string) error
}

// CLI dispatches subcommands.
type CLI struct {
	Name         string
	Commands     []Command
	OutputFormat OutputFormat
	// Stdout and Stderr are injectable so the CLI is testable.
	Stdout *os.File
	Stderr *os.File
	// runner executes the Bazel process. Overridden in tests.
	runner func(ctx []string) error
}

// NewCLI creates a CLI with the given name and subcommands.
func NewCLI(name string, commands []Command, stdout, stderr *os.File) *CLI {
	return &CLI{
		Name:         name,
		Commands:     commands,
		OutputFormat: FormatText,
		Stdout:       stdout,
		Stderr:       stderr,
		runner:       runBazel,
	}
}

// Run executes the command named by args[0].
func (c *CLI) Run(args []string) error {
	if len(args) < 1 {
		return c.printHelp()
	}

	// Accept and validate --format before dispatching, so every command honours it.
	rest, format, err := c.parseFormat(args[1:])
	if err != nil {
		return err
	}
	c.OutputFormat = format

	cmdName := args[0]
	for _, cmd := range c.Commands {
		if cmd.Name == cmdName {
			return cmd.Run(rest)
		}
	}

	return fmt.Errorf("unknown command %q (run `%s help` for the list)", cmdName, c.Name)
}

func (c *CLI) parseFormat(args []string) ([]string, OutputFormat, error) {
	format := c.OutputFormat
	kept := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--format" || arg == "-f":
			if i+1 >= len(args) {
				return nil, format, fmt.Errorf("%s requires a value", arg)
			}
			i++
			format = OutputFormat(args[i])
		case strings.HasPrefix(arg, "--format="):
			format = OutputFormat(strings.TrimPrefix(arg, "--format="))
		default:
			kept = append(kept, arg)
		}
	}

	switch format {
	case FormatText, FormatJSON:
		return kept, format, nil
	default:
		return nil, format, fmt.Errorf("invalid --format %q (want %q or %q)", format, FormatText, FormatJSON)
	}
}

func (c *CLI) printHelp() error {
	fmt.Fprintf(c.Stdout, "%s - Ecommerce monorepo developer CLI\n\n", c.Name)
	fmt.Fprintf(c.Stdout, "Usage: %s <command> [flags]\n\nCommands:\n", c.Name)
	for _, cmd := range c.Commands {
		fmt.Fprintf(c.Stdout, "  %-10s %s\n", cmd.Name, cmd.Description)
	}
	fmt.Fprintf(c.Stdout, "\nGlobal flags:\n")
	fmt.Fprintf(c.Stdout, "  -f, --format <text|json>  output format (default text)\n")
	return nil
}

// OutputJSON writes data as JSON to stdout.
func (c *CLI) OutputJSON(data interface{}) error {
	enc := json.NewEncoder(c.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// OutputTextf writes a formatted line to stdout.
func (c *CLI) OutputTextf(format string, args ...interface{}) {
	fmt.Fprintf(c.Stdout, format+"\n", args...)
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

// newBuildCommand wraps `bazel build`.
func newBuildCommand(c *CLI) Command {
	return Command{
		Name:        "build",
		Description: "Build targets (bazel build)",
		Run: func(args []string) error {
			return c.runBazel("build", args, nil)
		},
	}
}

// newTestCommand wraps `bazel test`.
func newTestCommand(c *CLI) Command {
	return Command{
		Name:        "test",
		Description: "Run tests (bazel test)",
		Run: func(args []string) error {
			return c.runBazel("test", args, nil)
		},
	}
}

// newLintCommand applies the repository's analysis-time policies.
func newLintCommand(c *CLI) Command {
	return Command{
		Name:        "lint",
		Description: "Check architecture policies (bazel build --config=lint)",
		Run: func(args []string) error {
			targets := args
			if len(targets) == 0 {
				targets = []string{"//..."}
			}
			return c.runBazel("build", targets, []string{"--config=lint"})
		},
	}
}

// newGazelleCommand regenerates BUILD files.
func newGazelleCommand(c *CLI) Command {
	return Command{
		Name:        "gazelle",
		Description: "Regenerate BUILD files and Go import mappings",
		Run: func(args []string) error {
			return c.runBazel("run", append([]string{"//:gazelle"}, args...), nil)
		},
	}
}

// newDepsCommand prints the dependency graph for a target.
func newDepsCommand(c *CLI) Command {
	return Command{
		Name:        "deps",
		Description: "Show the dependency graph for a target",
		Run: func(args []string) error {
			if len(args) < 1 {
				return errors.New("deps requires a target, e.g. `ecommerce deps //services/...`")
			}
			return c.runBazel("query", append([]string{"deps(" + args[0] + ")"}, args[1:]...), nil)
		},
	}
}

// runBazel invokes Bazel, streaming its output straight through so the user
// sees the familiar progress output.
func runBazel(args []string) error {
	cmd := exec.Command("bazel", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// runBazelFiltered runs Bazel and discards the normal output, reporting only
// the error. Used by commands that add their own formatting.
func (c *CLI) runBazelFiltered(args []string, extraFlags []string) error {
	return c.runner(append(append([]string{}, args...), extraFlags...))
}

func (c *CLI) runBazel(command string, args, extraFlags []string) error {
	if c.OutputFormat == FormatJSON {
		// JSON output means "do not stream human output"; report the result
		// structurally instead.
		err := c.runBazelFiltered([]string{command}, append(args, extraFlags...))
		return c.OutputJSON(map[string]interface{}{
			"command": command,
			"args":    append(args, extraFlags...),
			"ok":      err == nil,
			"error":   errString(err),
		})
	}
	return c.runBazelFiltered([]string{command}, append(args, extraFlags...))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func main() {
	cli := NewCLI("ecommerce", nil, os.Stdout, os.Stderr)
	cli.Commands = []Command{
		newBuildCommand(cli),
		newTestCommand(cli),
		newLintCommand(cli),
		newGazelleCommand(cli),
		newDepsCommand(cli),
		{
			Name:        "help",
			Description: "Show this help",
			Run: func([]string) error {
				return cli.printHelp()
			},
		},
	}

	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
