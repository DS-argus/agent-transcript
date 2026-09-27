package main

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

const noticeMilliseconds = "2500"

func errorNotice(err error) string {
	// tmux >=3.2 expands formats and strftime sequences. Do not feed filenames
	// or parser diagnostics into that language. Preserve readability without
	// permitting #(...) commands, #{...} formats, styles or % substitutions.
	text := strings.SplitN(err.Error(), "\n", 2)[0]
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		if r == '#' {
			return '＃'
		}
		if r == '%' {
			return '％'
		}
		return r
	}, text)
	runes := []rune(text)
	if len(runes) > 180 {
		text = string(runes[:177]) + "..."
	}
	return "Agent Transcript: " + text
}
func notifyMessage(ctx context.Context, client, message string) error {
	clients, err := tmux(ctx, "list-clients", "-F", "#{client_name}")
	if err != nil {
		return err
	}
	found := false
	for _, candidate := range strings.Split(clients, "\n") {
		if candidate == client && client != "" {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("notification client is no longer attached: %q", client)
	}
	_, err = tmux(ctx, "display-message", "-c", client, "-d", noticeMilliseconds, message)
	return err
}
func presentError(ctx context.Context, o options, err error) error {
	if err == nil || o.notifyClient == "" {
		return err
	}
	if notifyErr := notifyMessage(ctx, o.notifyClient, errorNotice(err)); notifyErr != nil {
		return fmt.Errorf("%w (notification failed: %v)", err, notifyErr)
	}
	// The key action has been handled; no run-shell error overlay is necessary.
	// Direct CLI invocation retains its stderr and nonzero exit behavior.
	return nil
}
