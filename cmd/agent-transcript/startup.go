package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"golang.org/x/sys/unix"
)

const startupKeyOption = "@agent_transcript_start_key"

// All generated transcripts begin with a plain heading. Ignore Markdown syntax,
// case, wrapping and border glyphs while checking that document content, not a
// loading screen, has appeared. This text never gets sent to the reader as input.
func normalizeVisible(text string) string {
	var out strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			out.WriteRune(unicode.ToLower(r))
		}
	}
	return out.String()
}

func documentAnchor(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	lines := bufio.NewReader(file)
	for {
		line, err := lines.ReadString('\n')
		if anchor := normalizeVisible(line); anchor != "" {
			// Only require a visible prefix, not text below the first viewport.
			if runes := []rune(anchor); len(runes) > 32 {
				anchor = string(runes[:32])
			}
			return anchor, nil
		}
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
	}
}

func rawTerminal(tty string) (bool, error) {
	fd, err := unix.Open(tty, unix.O_RDONLY|unix.O_NOCTTY, 0)
	if err != nil {
		return false, err
	}
	defer unix.Close(fd)
	state, err := terminalAttributes(fd)
	if err != nil {
		return false, err
	}
	return state.Lflag&unix.ICANON == 0 && state.Lflag&unix.ECHO == 0, nil
}

// readerReady checks the real application, terminal mode and rendered content.
// It does not guess an initialization delay or depend on configurable help bars.
func readerReady(ctx context.Context, pane, name, anchor string) (string, bool, error) {
	state, err := tmux(ctx, "display-message", "-p", "-t", pane,
		"#{pane_dead}\t#{pane_current_command}\t#{alternate_on}\t#{pane_tty}\t#{"+startupKeyOption+"}")
	if err != nil {
		return "", false, err
	}
	fields := strings.Split(state, "\t")
	if len(fields) > 0 && fields[0] != "0" {
		return "", false, fmt.Errorf("reader exited before startup completed")
	}
	if len(fields) != 5 || fields[1] != name || fields[2] != "1" || fields[3] == "" || fields[4] == "" {
		return "", false, nil
	}
	raw, err := rawTerminal(fields[3])
	if err != nil {
		return "", false, err
	}
	if !raw {
		return "", false, nil
	}
	if anchor != "" {
		screen, err := tmux(ctx, "capture-pane", "-p", "-t", pane)
		if err != nil {
			return "", false, err
		}
		if !strings.Contains(normalizeVisible(screen), anchor) {
			return "", false, nil
		}
	}
	return fields[4], true, nil
}
