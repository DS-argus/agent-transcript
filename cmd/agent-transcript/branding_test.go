package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPublicCLIName(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "agent-transcript "+version+"\n" {
		t.Fatalf("unexpected version banner: %q", out.String())
	}
	if !strings.HasPrefix(usage(), "Usage: agent-transcript ") {
		t.Fatalf("unexpected usage banner: %s", usage())
	}
	if startupKeyOption != "@agent_transcript_start_key" {
		t.Fatal("unexpected option namespace", startupKeyOption)
	}
}
