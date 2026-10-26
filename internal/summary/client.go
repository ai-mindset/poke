package summary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	OllamaModel          = "qwen3.8:27b"
	MistralModel         = "mistral-medium-latest"
	DefaultContextTokens = 32768
	maxResponseBytes     = 1024 * 1024
)

type Config struct {
	Provider      string
	OllamaURL     string
	APIKey        string
	ContextTokens int
}

type Client struct {
	provider, model, endpoint, apiKey string
	contextTokens                     int
	http                              *http.Client
}

func ModelFor(provider string) (string, error) {
	switch provider {
	case "ollama":
		return OllamaModel, nil
	case "mistral":
		return MistralModel, nil
	default:
		return "", fmt.Errorf("Unknown summary provider. Use ollama or mistral.")
	}
}

func NewClient(config Config) (*Client, error) {
	name, err := ModelFor(config.Provider)
	if err != nil {
		return nil, err
	}
	client := &Client{provider: config.Provider, model: name}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client.http = &http.Client{Transport: transport, Timeout: 5 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("Model endpoint redirects are disabled.")
		}}
	if config.Provider == "mistral" {
		client.apiKey = strings.TrimSpace(config.APIKey)
		if client.apiKey == "" {
			return nil, fmt.Errorf("Mistral authentication required. Set MISTRAL_API_KEY.")
		}
		client.endpoint = "https://api.mistral.ai/v1/chat/completions"
		return client, nil
	}
	base := config.OllamaURL
	if base == "" {
		base = "http://127.0.0.1:11434"
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("Invalid local Ollama URL.")
	}
	ip := net.ParseIP(u.Hostname())
	if (u.Scheme != "http" && u.Scheme != "https") || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("Ollama URL must be an HTTP(S) loopback address without credentials, a path, query, or fragment.")
	}
	client.contextTokens = config.ContextTokens
	if client.contextTokens == 0 {
		client.contextTokens = DefaultContextTokens
	}
	if client.contextTokens < 1024 || client.contextTokens > 1048576 {
		return nil, fmt.Errorf("Ollama context size must be between 1024 and 1048576 tokens.")
	}
	transport.Proxy = nil
	client.endpoint = strings.TrimRight(u.String(), "/") + "/api/chat"
	return client, nil
}

func (c *Client) Generate(ctx context.Context, input string) (Completion, error) {
	messages := []map[string]string{{"role": "system", "content": systemMessage()}, {"role": "user", "content": input}}
	var payload map[string]any
	if c.provider == "ollama" {
		payload = map[string]any{"model": c.model, "messages": messages, "format": json.RawMessage(outputSchema),
			"stream": false, "think": false, "truncate": false, "shift": false,
			"options": map[string]any{"temperature": 0, "num_ctx": c.contextTokens, "num_predict": 4096}}
	} else {
		payload = map[string]any{"model": c.model, "messages": messages, "stream": false, "temperature": 0, "max_tokens": 4096,
			"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "issue_summary", "strict": true, "schema": json.RawMessage(outputSchema)}}}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Completion{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return Completion{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "poke")
	if c.provider == "mistral" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return Completion{}, fmt.Errorf("%s summary request failed: %w", c.provider, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Provider errors can echo input. Do not expose bodies, credentials, or prompts.
		hint := "Check model availability, context capacity, and API compatibility."
		if c.provider == "mistral" && (response.StatusCode == 401 || response.StatusCode == 403) {
			hint = "Check MISTRAL_API_KEY and model access."
		}
		if response.StatusCode == 429 {
			hint = "Rate limited; retry later. No automatic retry was made."
		}
		return Completion{}, fmt.Errorf("%s API error %d. %s", c.provider, response.StatusCode, hint)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Completion{}, err
	}
	if len(body) > maxResponseBytes {
		return Completion{}, fmt.Errorf("Model response exceeded the 1 MiB limit.")
	}
	if c.provider == "ollama" {
		return decodeOllama(body)
	}
	return decodeMistral(body)
}

func decodeOllama(body []byte) (Completion, error) {
	var response struct {
		Model      string `json:"model"`
		Done       bool   `json:"done"`
		DoneReason string `json:"done_reason"`
		Error      string `json:"error"`
		Message    struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Completion{}, fmt.Errorf("Cannot decode Ollama response.")
	}
	if response.Error != "" || !response.Done || response.DoneReason != "stop" || response.Model == "" || strings.TrimSpace(response.Message.Content) == "" {
		return Completion{}, fmt.Errorf("Ollama did not return a complete summary. Check model context and output limits.")
	}
	return Completion{Content: response.Message.Content, Model: response.Model}, nil
}

func decodeMistral(body []byte) (Completion, error) {
	var response struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Completion{}, fmt.Errorf("Cannot decode Mistral response.")
	}
	if response.Model == "" || len(response.Choices) != 1 || response.Choices[0].FinishReason != "stop" {
		return Completion{}, fmt.Errorf("Mistral did not return a complete summary. Check model context and output limits.")
	}
	content, err := mistralText(response.Choices[0].Message.Content)
	if err != nil {
		return Completion{}, err
	}
	return Completion{Content: content, Model: response.Model}, nil
}

func mistralText(raw json.RawMessage) (string, error) {
	var content string
	if err := json.Unmarshal(raw, &content); err != nil {
		var chunks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &chunks); err != nil {
			return "", fmt.Errorf("Mistral returned unsupported message content.")
		}
		var text strings.Builder
		for _, chunk := range chunks {
			if chunk.Type != "text" {
				return "", fmt.Errorf("Mistral returned non-text content instead of a summary.")
			}
			text.WriteString(chunk.Text)
		}
		content = text.String()
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("Mistral returned an empty summary.")
	}
	return content, nil
}
