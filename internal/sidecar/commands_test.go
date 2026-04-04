package sidecar_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"context0/internal/sidecar"
)

// TestPing verifies the sidecar responds to a ping command.
func TestPing(t *testing.T) {
	if !sidecar.IsRunning() {
		t.Skip("sidecar not running — start with `context0 --daemon`")
	}
	resp, err := sidecar.Send(sidecar.Request{"cmd": "ping"})
	if err != nil {
		t.Fatalf("Send ping: %v", err)
	}
	if !resp.OK {
		t.Fatalf("ping returned ok=false: %s", resp.Error)
	}
}

// TestGenerate verifies the sidecar can generate text from a simple prompt.
func TestGenerate(t *testing.T) {
	if !sidecar.IsRunning() {
		t.Skip("sidecar not running — start with `context0 --daemon`")
	}
	raw, err := sidecar.SendRaw(sidecar.Request{
		"cmd": "generate",
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with exactly: hello"},
		},
		"max_tokens":  64,
		"temperature": 0.0,
	})
	if err != nil {
		t.Fatalf("SendRaw generate: %v", err)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Text  string `json:"text"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Fatalf("generate returned ok=false: %s", resp.Error)
	}
	if resp.Text == "" {
		t.Fatal("generate returned empty text")
	}
	fmt.Printf("    generate ok: text=%q\n", resp.Text)
}

// TestUnknownCommand verifies the sidecar returns ok=false for unknown commands.
func TestUnknownCommand(t *testing.T) {
	if !sidecar.IsRunning() {
		t.Skip("sidecar not running — start with `context0 --daemon`")
	}
	resp, err := sidecar.Send(sidecar.Request{"cmd": "nonexistent_command_xyz"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.OK {
		t.Fatal("expected ok=false for unknown command, got ok=true")
	}
	if resp.Error == "" {
		t.Fatal("expected non-empty error for unknown command")
	}
}
