package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	userAgent    = "Mozilla/5.0 (compatible; ResearchAgent/1.0)"
	maxPageChars = 6000 // how much page text we send back to the model
)

// Tool is the one interface every agent tool implements.
// To add a new tool, create a type with these 4 methods and register it in main.go.
type Tool interface {
	Name() string
	Description() string
	Schema() map[string]any // JSON schema of the tool's input
	Run(ctx context.Context, input json.RawMessage) (string, error)
}

// ---------------------------------------------------------------------
// search_web
// ---------------------------------------------------------------------

type searchTool struct{}

func (searchTool) Name() string { return "search_web" }

func (searchTool) Description() string {
	return "Search the web. Returns the top 5 results with title, URL and a short snippet."
}

func (searchTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "The search query"},
		},
		"required": []string{"query"},
	}
}

func (searchTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &in); err != nil || strings.TrimSpace(in.Query) == "" {
		return "", errors.New("input must be {\"query\": \"...\"}")
	}

	// DuckDuckGo's HTML endpoint needs no API key.
	// If it ever breaks, replace this function with a Tavily/Brave API call.
	page, _, err := httpGet(ctx, "https://html.duckduckgo.com/html/?q="+url.QueryEscape(in.Query))
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	results := parseResults(page, 5)
	if len(results) == 0 {
		return "No results found.", nil
	}

	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return b.String(), nil
}

// Result is one search hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

var (
	reLink = regexp.MustCompile(`(?s)class="result__a" href="([^"]+)">(.*?)</a>`)
	reSnip = regexp.MustCompile(`(?s)class="result__snippet"[^>]*>(.*?)</a>`)
)

// parseResults pulls titles, URLs and snippets out of DuckDuckGo's HTML.
func parseResults(page string, max int) []Result {
	links := reLink.FindAllStringSubmatch(page, -1)
	snips := reSnip.FindAllStringSubmatch(page, -1)

	var out []Result
	for i, l := range links {
		if len(out) >= max {
			break
		}
		u := cleanURL(l[1])
		if strings.Contains(u, "duckduckgo.com/y.js") { // skip ads
			continue
		}
		r := Result{URL: u, Title: stripHTML(l[2])}
		if i < len(snips) {
			r.Snippet = stripHTML(snips[i][1])
		}
		out = append(out, r)
	}
	return out
}

// DuckDuckGo wraps links like //duckduckgo.com/l/?uddg=<real-url>. Unwrap it.
func cleanURL(raw string) string {
	raw = html.UnescapeString(raw)
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if real := u.Query().Get("uddg"); real != "" {
		return real
	}
	return raw
}

// ---------------------------------------------------------------------
// fetch_url
// ---------------------------------------------------------------------

type fetchTool struct{}

func (fetchTool) Name() string { return "fetch_url" }

func (fetchTool) Description() string {
	return "Download a web page and return its readable text (truncated). Use it to read a search result."
}

func (fetchTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string", "description": "Full http(s) URL of the page"},
		},
		"required": []string{"url"},
	}
}

func (fetchTool) Run(ctx context.Context, input json.RawMessage) (string, error) {
	var in struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", errors.New("input must be {\"url\": \"...\"}")
	}
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("only http/https URLs are allowed")
	}

	body, contentType, err := httpGet(ctx, in.URL)
	if err != nil {
		return "", err
	}
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "text/plain") {
		return "", fmt.Errorf("unsupported content type: %s", contentType)
	}

	text := stripHTML(body)
	if text == "" {
		return "", errors.New("page has no readable text (it may need JavaScript)")
	}
	return truncate(text, maxPageChars), nil
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

// httpGet downloads a URL (max 1 MB) and returns body + Content-Type.
func httpGet(ctx context.Context, rawURL string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(data), resp.Header.Get("Content-Type"), err
}

var (
	reScript = regexp.MustCompile(`(?is)<(script|style|noscript)[^>]*>.*?</(script|style|noscript)>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpace  = regexp.MustCompile(`\s+`)
)

// stripHTML turns an HTML page into plain, single-spaced text.
func stripHTML(s string) string {
	s = reScript.ReplaceAllString(s, " ")
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
