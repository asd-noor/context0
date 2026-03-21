package memory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// EmbedDim is the output dimension of qllama/bge-small-en-v1.5.
	EmbedDim = 384

	defaultOllamaEndpoint   = "http://localhost:11434"
	defaultLMStudioEndpoint = "http://localhost:1234"
	defaultModel            = "qllama/bge-small-en-v1.5"
)

// EmbedClient generates embeddings via an OpenAI-compatible HTTP API
// (Ollama or LM Studio).
type EmbedClient struct {
	endpoint string
	model    string
	isOllama bool
	http     *http.Client
	pullHTTP *http.Client // separate client with no timeout for long pulls
}

// NewEmbedClient creates an embedding client. The endpoint and model are
// resolved from environment variables CTX0_EMBED_ENDPOINT and CTX0_EMBED_MODEL,
// falling back to the Ollama default.
//
// When the endpoint is the Ollama default, the client will automatically pull
// the model if it is not already available, streaming progress to stderr.
func NewEmbedClient() *EmbedClient {
	endpoint := os.Getenv("CTX0_EMBED_ENDPOINT")
	if endpoint == "" {
		endpoint = defaultOllamaEndpoint
	}
	model := os.Getenv("CTX0_EMBED_MODEL")
	if model == "" {
		model = defaultModel
	}
	isOllama := endpoint == defaultOllamaEndpoint
	c := &EmbedClient{
		endpoint: endpoint,
		model:    model,
		isOllama: isOllama,
		http:     &http.Client{Timeout: 30 * time.Second},
		pullHTTP: &http.Client{}, // no timeout — pulls can take minutes
	}
	if isOllama {
		c.ensureModel()
	}
	return c
}

// ensureModel checks whether the model is present in Ollama and pulls it if
// not. Progress lines are streamed to stderr.
func (c *EmbedClient) ensureModel() {
	// Check if the model is already available via /api/tags.
	tagsURL := c.endpoint + "/api/tags"
	resp, err := c.http.Get(tagsURL)
	if err != nil {
		// Ollama may not be running yet; skip pull and let Embed() surface the error.
		return
	}
	defer resp.Body.Close()

	var tagsResp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return
	}
	for _, m := range tagsResp.Models {
		// Ollama names are "model:tag"; match on the base name too.
		if m.Name == c.model || strings.SplitN(m.Name, ":", 2)[0] == c.model {
			return // already present
		}
	}

	// Model not found — pull it.
	fmt.Fprintf(os.Stderr, "context0: pulling embedding model %q from Ollama...\n", c.model)
	if err := c.pullModel(); err != nil {
		fmt.Fprintf(os.Stderr, "context0: warning: model pull failed: %v\n", err)
	}
}

type pullRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

type pullStatus struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// pullModel calls POST /api/pull and streams progress lines to stderr.
func (c *EmbedClient) pullModel() error {
	body, err := json.Marshal(pullRequest{Model: c.model, Stream: true})
	if err != nil {
		return fmt.Errorf("marshal pull request: %w", err)
	}

	url := c.endpoint + "/api/pull"
	resp, err := c.pullHTTP.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, raw)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var s pullStatus
		if err := json.Unmarshal(line, &s); err != nil {
			continue
		}
		if s.Total > 0 {
			pct := float64(s.Completed) / float64(s.Total) * 100
			fmt.Fprintf(os.Stderr, "\r  %s — %.1f%%", s.Status, pct)
		} else {
			fmt.Fprintf(os.Stderr, "\r  %s", s.Status)
		}
	}
	fmt.Fprintln(os.Stderr) // newline after progress
	return scanner.Err()
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
