package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"agent-transcript/internal/harness"
	"agent-transcript/internal/reader"
)

// default configuration
func defaultOptions() options {
	return options{reader: reader.Default, position: "top", size: "90%", focus: "on"}
}
func parseViewOption(name string) bool {
	switch name {
	case "--position", "--size", "--focus":
		return true
	}
	return false
}
func setViewOption(o *options, name, value string) error {
	switch name {
	case "--position":
		o.position = value
	case "--size":
		o.size = value
		o.sizeExplicit = true
	case "--focus":
		o.focus = value
	default:
		return fmt.Errorf("unknown view option %s", name)
	}
	return nil
}
func validateViewOptions(o options) error {
	switch o.position {
	case "right", "left", "bottom", "top":
	default:
		return fmt.Errorf("position must be right, left, bottom or top")
	}
	if o.focus != "on" && o.focus != "off" {
		return fmt.Errorf("focus must be on or off")
	}
	number := strings.TrimSuffix(o.size, "%")
	if number == "" || strings.Trim(number, "0123456789") != "" {
		return fmt.Errorf("size must be a positive cell count or percentage")
	}
	n, err := strconv.Atoi(number)
	if err != nil || n < 1 {
		return fmt.Errorf("size must be positive")
	}
	if strings.HasSuffix(o.size, "%") && n >= 100 {
		return fmt.Errorf("size percentage must be between 1 and 99")
	}
	return nil
}
func splitViewArgs(o options) []string {
	axis := "-h"
	if o.position == "top" || o.position == "bottom" {
		axis = "-v"
	}
	args := []string{axis, "-d", "-l", o.size}
	if o.position == "left" || o.position == "top" {
		args = append(args, "-b")
	}
	return args
}

type managedBinding struct{ Table, Key, Command, Listing string }

const bindingsOption = "@agent_transcript_bindings"

func bindingCommand() string {
	return "#{q:@agent_transcript_command} --reader=#{q:@agent_transcript_reader} --position=#{q:@agent_transcript_position} --focus=#{q:@agent_transcript_focus} --notify-client=#{q:client_name} '#{pane_id}'"
}

// configure registers keys after validating configuration. Previous keys are
// removed only when their exact saved binding still belongs to this plugin.
func configure(ctx context.Context) error {
	values := map[string]string{}
	keys := []string{"reader", "position", "size_all", "focus", "key", "copy_mode_key"}
	for _, name := range harness.Names() {
		keys = append(keys, "size_"+name)
	}
	for _, key := range keys {
		value, err := tmux(ctx, "show-option", "-gqv", "@agent_transcript_"+key)
		if err != nil {
			return err
		}
		values[key] = value
	}
	o := defaultOptions()
	o.reader = values["reader"]
	for _, key := range []string{"position", "focus"} {
		_ = setViewOption(&o, "--"+key, values[key])
	}
	if value := values["size_all"]; value != "" {
		o.size = value
	}
	if err := validateViewOptions(o); err != nil {
		return err
	}
	for _, name := range harness.Names() {
		if value := values["size_"+name]; value != "" {
			profile := o
			profile.size = value
			if err := validateViewOptions(profile); err != nil {
				return fmt.Errorf("@agent_transcript_size_%s: %w", name, err)
			}
		}
	}
	if !reader.Supported(o.reader) {
		return fmt.Errorf("unsupported reader: %s", o.reader)
	}
	var requested []managedBinding
	for _, spec := range []struct {
		option, table string
	}{{"key", "prefix"}, {"copy_mode_key", "copy-mode"}, {"copy_mode_key", "copy-mode-vi"}} {
		key := values[spec.option]
		if key == "none" {
			continue
		}
		if key == "" || strings.ContainsAny(key, "\r\n\t") {
			return fmt.Errorf("invalid %s key; use none to disable", spec.option)
		}
		for _, previous := range requested {
			if previous.Table == spec.table && previous.Key == key {
				return fmt.Errorf("key collision in %s: %s", spec.table, key)
			}
		}
		requested = append(requested, managedBinding{Table: spec.table, Key: key, Command: bindingCommand()})
	}
	// Validate tmux key spelling in an isolated temporary table before mutation.
	table := "agent-transcript-validate-" + strconv.Itoa(os.Getpid())
	for _, binding := range requested {
		if _, err := tmux(ctx, "bind-key", "-T", table, binding.Key, "display-message", ""); err != nil {
			return err
		}
		if _, err := tmux(ctx, "unbind-key", "-T", table, binding.Key); err != nil {
			return err
		}
	}
	var previous []managedBinding
	saved, err := tmux(ctx, "show-option", "-gqv", bindingsOption)
	if err != nil {
		return err
	}
	if saved != "" {
		if err := json.Unmarshal([]byte(saved), &previous); err != nil {
			return fmt.Errorf("invalid saved plugin binding metadata: %w", err)
		}
	}
	for _, binding := range previous {
		listing, err := tmux(ctx, "list-keys", "-T", binding.Table)
		if err != nil {
			continue
		}
		owned := false
		for _, line := range strings.Split(listing, "\n") {
			if strings.Join(strings.Fields(line), " ") == binding.Listing && binding.Listing != "" {
				owned = true
				break
			}
		}
		if owned {
			if _, err := tmux(ctx, "unbind-key", "-T", binding.Table, binding.Key); err != nil {
				return err
			}
		}
	}
	for i, binding := range requested {
		if _, err := tmux(ctx, "bind-key", "-T", binding.Table, binding.Key, "run-shell", "-b", binding.Command); err != nil {
			return err
		}
		listing, err := tmux(ctx, "list-keys", "-T", binding.Table)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(listing, "\n") {
			fields := strings.Fields(line)
			// tmux emits bind-key [-r] -T TABLE KEY COMMAND. Locate -T rather than
			// assuming alignment or whitespace widths.
			for j, field := range fields {
				if field == "-T" && j+2 < len(fields) && fields[j+1] == binding.Table && fields[j+2] == binding.Key {
					requested[i].Listing = strings.Join(fields, " ")
				}
			}
		}
	}
	data, err := json.Marshal(requested)
	if err != nil {
		return err
	}
	_, err = tmux(ctx, "set-option", "-g", bindingsOption, string(data))
	return err
}

// resolveViewSize selects geometry only after resolving the source agent, even
// when the invoking pane is a viewer. Explicit CLI geometry takes precedence.
func resolveViewSize(ctx context.Context, o options, name string) (options, error) {
	if _, ok := harness.Lookup(name); !ok {
		return o, fmt.Errorf("unsupported size profile: %s", name)
	}
	if !o.sizeExplicit {
		value, err := tmux(ctx, "show-option", "-gqv", "@agent_transcript_size_"+name)
		if err != nil {
			return o, err
		}
		if value == "" {
			value, err = tmux(ctx, "show-option", "-gqv", "@agent_transcript_size_all")
			if err != nil {
				return o, err
			}
		}
		if value != "" {
			o.size = value
		}
	}
	return o, validateViewOptions(o)
}
