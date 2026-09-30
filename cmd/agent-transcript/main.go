package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"agent-transcript/internal/reader"
)

var version = "dev"

type options struct {
	target, notifyClient, reader string
	position, size, focus        string
	sizeExplicit                 bool
	help, version                bool
}

// usual tmux plugin execution path
func parse(args []string) (options, error) {
	o := defaultOptions()
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--help" || arg == "-h" {
			o.help = true
			continue
		}
		if arg == "--version" {
			o.version = true
			continue
		}
		if arg == "--" {
			if len(args[i+1:]) != 1 || o.target != "" {
				return o, fmt.Errorf("expected one target pane")
			}
			o.target = args[i+1]
			break
		}
		if !strings.HasPrefix(arg, "-") {
			if o.target != "" {
				return o, fmt.Errorf("expected only one target pane")
			}
			o.target = arg
			continue
		}
		name, value, hasValue := strings.Cut(arg, "=")
		var destination *string
		switch name {
		case "--reader":
			destination = &o.reader
		case "--notify-client":
			destination = &o.notifyClient
		default:
			if !parseViewOption(name) {
				return o, fmt.Errorf("unknown argument: %s", name)
			}
			if !hasValue {
				i++
				if i >= len(args) {
					return o, fmt.Errorf("missing value for %s", name)
				}
				value = args[i]
			}
			if value == "" || strings.HasPrefix(value, "--") {
				return o, fmt.Errorf("missing value for %s", name)
			}
			if err := setViewOption(&o, name, value); err != nil {
				return o, err
			}
			continue
		}
		if !hasValue {
			i++
			if i >= len(args) {
				return o, fmt.Errorf("missing value for %s", name)
			}
			value = args[i]
		}
		if value == "" || strings.HasPrefix(value, "--") {
			return o, fmt.Errorf("missing value for %s", name)
		}
		*destination = value
	}
	if !reader.Supported(o.reader) {
		return o, fmt.Errorf("unsupported reader: %s (choose %s)", o.reader, strings.Join(reader.Names(), ", "))
	}
	if err := validateViewOptions(o); err != nil {
		return o, err
	}
	return o, nil
}

func tmux(ctx context.Context, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "tmux", args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("tmux: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
func quote(arg string) string { return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'" }

func usage() string {
	return `Usage: agent-transcript [options] [target-pane]

Open or refresh the foreground agent's transcript.
  --reader READER                       Markdown viewer (default leaf)
  --position right|left|top|bottom      Viewer split position
  --size SIZE                           Viewer size; overrides agent settings
  --focus on|off                        Focus the viewer after startup
  --notify-client CLIENT                Brief error notice for this tmux client
  --version                             Print version
  -h, --help                            Print help
`
}

func run(ctx context.Context, args []string, out io.Writer) error {
	// agent-transcript _view [path readerName]
	if len(args) > 0 && args[0] == "_view" {
		if len(args) != 3 {
			return fmt.Errorf("invalid internal viewer arguments")
		}
		return view(ctx, args[1], args[2])
	}

	// agent-transcript _configure [args...]
	if len(args) > 0 && args[0] == "_configure" {
		if len(args) != 1 {
			return fmt.Errorf("invalid internal configure arguments")
		}
		return configure(ctx)
	}

	// agent-transcript [args...]
	o, err := parse(args)
	if err != nil {
		return presentError(ctx, o, err)
	}
	if o.help {
		_, err = io.WriteString(out, usage())
		return err
	}
	if o.version {
		_, err = fmt.Fprintf(out, "agent-transcript %s\n", version)
		return err
	}
	return presentError(ctx, o, open(ctx, o))
}
func main() {
	// create ctx to deliver exit signal
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "agent-transcript:", err)
		os.Exit(1)
	}
}
