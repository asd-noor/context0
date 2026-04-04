package memory

import (
	"encoding/json"
	"fmt"

	"context0/internal/sidecar"
)

const (
	// EmbedDim is the output dimension of mlx-community/bge-small-en-v1.5-4bit.
	EmbedDim = 384
)

// EmbedClient generates 384-dim embeddings via the context0 Python sidecar.
//
// The sidecar must be running (`context0 --start-sidecar`) before any Embed call is
// made.  If it is not running, Embed returns a clear error directing the user
// to start it.
type EmbedClient struct{}

// NewEmbedClient returns an EmbedClient.  No network calls are made at
// construction time; the sidecar connection is established per-call.
func NewEmbedClient() *EmbedClient {
	return &EmbedClient{}
}

// Embed sends text to the sidecar's embed endpoint and returns a normalised
// 384-dimensional float32 vector.
func (c *EmbedClient) Embed(text string) ([]float32, error) {
	raw, err := sidecar.SendRaw(sidecar.Request{
		"cmd":  "embed",
		"text": text,
	})
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}

	var resp struct {
		OK        bool      `json:"ok"`
		Error     string    `json:"error,omitempty"`
		Embedding []float32 `json:"embedding"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("embed: sidecar error: %s", resp.Error)
	}
	if len(resp.Embedding) != EmbedDim {
		return nil, fmt.Errorf("embed: expected %d dims, got %d", EmbedDim, len(resp.Embedding))
	}
	return resp.Embedding, nil
}
