package sidecar_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"context0/internal/sidecar"
)

func TestEmbedViaSidecar(t *testing.T) {
	if !sidecar.IsRunning() {
		t.Skip("sidecar not running — start with `context0 --daemon`")
	}
	raw, err := sidecar.SendRaw(sidecar.Request{"cmd": "embed", "text": "hello world"})
	if err != nil {
		t.Fatalf("SendRaw: %v", err)
	}
	var resp struct {
		OK        bool      `json:"ok"`
		Embedding []float32 `json:"embedding"`
		Error     string    `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Fatalf("sidecar error: %s", resp.Error)
	}
	if len(resp.Embedding) != 384 {
		t.Fatalf("expected 384 dims, got %d", len(resp.Embedding))
	}
	fmt.Printf("    embed ok: dim=%d first3=[%.4f %.4f %.4f]\n",
		len(resp.Embedding), resp.Embedding[0], resp.Embedding[1], resp.Embedding[2])
}
