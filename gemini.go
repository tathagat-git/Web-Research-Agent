package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// Default model. Override with GEMINI_MODEL in your .env file.
// Model names change often: check https://ai.google.dev/gemini-api/docs/models
const defaultModel = "gemini-3.5-flash-lite"

var httpClient = &http.Client{Timeout: 60 * time.Second}

// ---------------------------------------------------------------------
// Gemini generateContent API types
// ---------------------------------------------------------------------

// Content is one message in the conversation (role "user" or "model").
// Parts are kept as raw JSON on purpose: Gemini 3 models attach extra fields
// (like thought signatures) to parts, and these MUST be sent back unchanged
// when we continue a function-calling conversation.
type Content struct {
	Role  string            `json:"role,omitempty"`
	Parts []json.RawMessage `json:"parts"`
}

type functionDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiTool struct {
	FunctionDeclarations []functionDecl `json:"functionDeclarations"`
}

type generateRequest struct {
	SystemInstruction *Content       `json:"systemInstruction,omitempty"`
	Contents          []Content      `json:"contents"`
	Tools             []geminiTool   `json:"tools,omitempty"`
	GenerationConfig  map[string]any `json:"generationConfig,omitempty"`
}

type generateResponse struct {
	Candidates []struct {
		Content      Content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// textPart builds {"text": "..."}.
func textPart(s string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"text": s})
	return b
}

// ---------------------------------------------------------------------
// API call
// ---------------------------------------------------------------------

// generate sends the whole conversation to Gemini and returns the model's reply.
// It retries a few times when the API is busy (HTTP 429 / 503).
func (a *Agent) generate(ctx context.Context, contents []Content) (*Content, error) {
	body, err := json.Marshal(generateRequest{
		SystemInstruction: &Content{Parts: []json.RawMessage{textPart(systemPrompt)}},
		Contents:          contents,
		Tools:             []geminiTool{{FunctionDeclarations: a.decls}},
		GenerationConfig:  map[string]any{"maxOutputTokens": 4096},
	})
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", a.model)

	var raw []byte
	var status int
	for attempt := 0; attempt < 3; attempt++ {
		raw, status, err = post(ctx, endpoint, body)
		if err != nil {
			return nil, err
		}
		if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
			break
		}
		wait := time.Duration(2<<attempt) * time.Second // 2s, 4s, 8s
		log.Printf("API busy (HTTP %d), retrying in %s", status, wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	var out generateResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("bad API response (HTTP %d): %s", status, truncate(string(raw), 300))
	}
	if out.Error != nil {
		return nil, fmt.Errorf("gemini API error: %s", out.Error.Message)
	}
	if len(out.Candidates) == 0 {
		if out.PromptFeedback != nil && out.PromptFeedback.BlockReason != "" {
			return nil, fmt.Errorf("request blocked: %s", out.PromptFeedback.BlockReason)
		}
		return nil, errors.New("gemini returned no candidates")
	}

	c := out.Candidates[0]
	if len(c.Content.Parts) == 0 {
		return nil, fmt.Errorf("gemini returned an empty reply (finishReason=%s)", c.FinishReason)
	}
	c.Content.Role = "model"
	return &c.Content, nil
}

func post(ctx context.Context, endpoint string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", os.Getenv("GEMINI_API_KEY"))

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, err
}
