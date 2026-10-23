package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStripHTML(t *testing.T) {
	in := `<p>Hello <b>world</b></p><script>alert(1)</script><style>p{}</style>`
	if got := stripHTML(in); got != "Hello world" {
		t.Errorf("got %q, want %q", got, "Hello world")
	}
}

func TestParseResults(t *testing.T) {
	page := `
<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F&amp;rut=abc">Go <b>Docs</b></a>
<a class="result__snippet" href="//duckduckgo.com/l/?uddg=x">Official <b>Go</b> documentation.</a>`

	got := parseResults(page, 5)
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].URL != "https://go.dev/doc/" {
		t.Errorf("URL = %q", got[0].URL)
	}
	if got[0].Title != "Go Docs" {
		t.Errorf("Title = %q", got[0].Title)
	}
	if got[0].Snippet != "Official Go documentation." {
		t.Errorf("Snippet = %q", got[0].Snippet)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abcdef", 3); got != "abc..." {
		t.Errorf("got %q", got)
	}
	if got := truncate("abc", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
}

func TestLoadDotEnv(t *testing.T) {
	// t.Setenv restores the original values when the test ends.
	t.Setenv("DOTENV_TEST_A", "")
	t.Setenv("DOTENV_TEST_B", "from-real-env")
	t.Setenv("DOTENV_TEST_C", "")

	content := "# a comment\n\nDOTENV_TEST_A=hello\nDOTENV_TEST_B=should-not-override\nexport DOTENV_TEST_C=\"quoted value\"\nnot a valid line\n"
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("DOTENV_TEST_A"); got != "hello" {
		t.Errorf("A = %q, want hello", got)
	}
	if got := os.Getenv("DOTENV_TEST_B"); got != "from-real-env" {
		t.Errorf("B = %q, real env must win", got)
	}
	if got := os.Getenv("DOTENV_TEST_C"); got != "quoted value" {
		t.Errorf("C = %q, want quoted value", got)
	}
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("missing file should not be an error, got %v", err)
	}
}

func TestFunctionCallsAndFinalText(t *testing.T) {
	reply := &Content{Role: "model", Parts: []json.RawMessage{
		json.RawMessage(`{"text":"thinking...","thought":true}`),
		json.RawMessage(`{"functionCall":{"name":"search_web","args":{"query":"go"}},"thoughtSignature":"abc"}`),
		json.RawMessage(`{"text":"Final answer"}`),
	}}

	calls := functionCalls(reply)
	if len(calls) != 1 || calls[0].Name != "search_web" || string(calls[0].Args) != `{"query":"go"}` {
		t.Errorf("unexpected calls: %+v", calls)
	}
	if got := finalText(reply); got != "Final answer" {
		t.Errorf("finalText = %q, thought parts must be skipped", got)
	}
}

// echoTool is a fake tool used only in tests.
type echoTool struct{}

func (echoTool) Name() string           { return "echo" }
func (echoTool) Description() string    { return "returns its input" }
func (echoTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (echoTool) Run(_ context.Context, in json.RawMessage) (string, error) {
	return string(in), nil
}

type fnResponse struct {
	FunctionResponse struct {
		Name     string            `json:"name"`
		Response map[string]string `json:"response"`
	} `json:"functionResponse"`
}

func parseResponse(t *testing.T, raw json.RawMessage) fnResponse {
	t.Helper()
	var r fnResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("bad functionResponse JSON: %v", err)
	}
	return r
}

func TestRunToolsKeepsOrderAndHandlesErrors(t *testing.T) {
	tools := map[string]Tool{"echo": echoTool{}}
	calls := []ToolCall{
		{Name: "echo", Args: json.RawMessage(`{"q":"one"}`)},
		{Name: "missing", Args: json.RawMessage(`{}`)},
		{Name: "echo", Args: json.RawMessage(`{"q":"three"}`)},
	}

	results := runTools(context.Background(), tools, calls)
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	r0 := parseResponse(t, results[0])
	if r0.FunctionResponse.Name != "echo" || r0.FunctionResponse.Response["result"] != `{"q":"one"}` {
		t.Errorf("result 0 wrong: %+v", r0)
	}

	r1 := parseResponse(t, results[1])
	if r1.FunctionResponse.Name != "missing" || r1.FunctionResponse.Response["error"] == "" {
		t.Errorf("unknown tool should return an error: %+v", r1)
	}

	r2 := parseResponse(t, results[2])
	if r2.FunctionResponse.Response["result"] != `{"q":"three"}` {
		t.Errorf("result 2 wrong: %+v", r2)
	}
}
