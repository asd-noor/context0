package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	// EmbedDim is the output dimension of BAAI/bge-small-en-v1.5.
	EmbedDim = 384

	defaultLMStudioEndpoint = "http://localhost:1234"
	defaultOllamaEndpoint   = "http://localhost:11434"
	defaultModel            = "BAAI/bge-small-en-v1.5"
)

// EmbedClient generates embeddings via an OpenAI-compatible HTTP API
// (LM Studio or Ollama).
type EmbedClient struct {
	endpoint string
	model    string
	http     *http.Client
}

// NewEmbedClient creates an embedding client. The endpoint and model are
// resolved from environment variables CTX0_EMBED_ENDPOINT and CTX0_EMBED_MODEL,
// falling back to the LM Studio default.
func NewEmbedClient() *EmbedClient {
	endpoint := os.Getenv("CTX0_EMBED_ENDPOINT")
	if endpoint == "" {
		endpoint = defaultLMStudioEndpoint
	}
	model := os.Getenv("CTX0_EMBED_MODEL")
	if model == "" {
		model = defaultModel
	}
	return &EmbedClient{
		endpoint: endpoint,
		model:    model,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Embed generates an embedding vector for the given text.
func (c *EmbedClient) Embed(text string) ([]float32, error) {
	body, err := json.Marshal(embedRequest{Model: c.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("embed: marshal request: %w", err)
	}

	url := c.endpoint + "/v1/embeddings"
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("embed: server returned %d: %s", resp.StatusCode, raw)
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("embed: empty response data")
	}
	vec := result.Data[0].Embedding
	if len(vec) != EmbedDim {
		return nil, fmt.Errorf("embed: expected %d dims, got %d", EmbedDim, len(vec))
	}
	return vec, nil
}
