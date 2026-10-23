package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

const systemPrompt = `You are a web research agent.
1. Use search_web to find sources for the user's topic.
2. Use fetch_url to read the 2-3 most relevant pages. If a page fails, try another one.
3. Write a short summary (max ~200 words) as bullet points, then list the source URLs.
Only state facts you actually read in the pages. Do not make things up.`

type Agent struct {
	tools    map[string]Tool
	decls    []functionDecl
	maxSteps int
	model    string
}

func NewAgent(tools []Tool, maxSteps int) *Agent {
	a := &Agent{tools: map[string]Tool{}, maxSteps: maxSteps, model: defaultModel}
	if m := os.Getenv("GEMINI_MODEL"); m != "" {
		a.model = m
	}
	for _, t := range tools {
		a.tools[t.Name()] = t
		a.decls = append(a.decls, functionDecl{t.Name(), t.Description(), t.Schema()})
	}
	return a
}

// Run is the agent loop:
//
//	ask model -> if it wants tools: run them, send results back -> repeat
//	until the model replies with plain text (the final answer).
func (a *Agent) Run(ctx context.Context, question string) (string, error) {
	contents := []Content{{Role: "user", Parts: []json.RawMessage{textPart(question)}}}

	for step := 1; step <= a.maxSteps; step++ {
		reply, err := a.generate(ctx, contents)
		if err != nil {
			return "", err
		}
		contents = append(contents, *reply)

		calls := functionCalls(reply)
		if len(calls) == 0 {
			return finalText(reply), nil // no tools requested: this is the answer
		}

		log.Printf("step %d: model wants to use %d tool(s)", step, len(calls))
		results := runTools(ctx, a.tools, calls)
		contents = append(contents, Content{Role: "user", Parts: results})
	}
	return "", errors.New("stopped: max steps reached before the agent finished (try -steps 12)")
}

// ---------------------------------------------------------------------
// Reading the model's reply
// ---------------------------------------------------------------------

// ToolCall is one function call requested by the model.
type ToolCall struct {
	Name string
	Args json.RawMessage
}

func functionCalls(c *Content) []ToolCall {
	var calls []ToolCall
	for _, raw := range c.Parts {
		var p struct {
			FunctionCall *struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall"`
		}
		if json.Unmarshal(raw, &p) == nil && p.FunctionCall != nil {
			calls = append(calls, ToolCall{Name: p.FunctionCall.Name, Args: p.FunctionCall.Args})
		}
	}
	return calls
}

// finalText joins the text parts of a reply (skipping internal "thought" parts).
func finalText(c *Content) string {
	var parts []string
	for _, raw := range c.Parts {
		var p struct {
			Text    string `json:"text"`
			Thought bool   `json:"thought"`
		}
		if json.Unmarshal(raw, &p) == nil && p.Text != "" && !p.Thought {
			parts = append(parts, p.Text)
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return "(the model returned no text)"
	}
	return text
}

// ---------------------------------------------------------------------
// Running tools
// ---------------------------------------------------------------------

// runTools executes every tool call from one model turn IN PARALLEL
// (one goroutine per call). Results keep the same order as the calls.
func runTools(ctx context.Context, tools map[string]Tool, calls []ToolCall) []json.RawMessage {
	results := make([]json.RawMessage, len(calls))

	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func(i int, c ToolCall) {
			defer wg.Done()
			results[i] = execute(ctx, tools, c) // each goroutine writes only its own index
		}(i, c)
	}
	wg.Wait()
	return results
}

// execute runs one tool with a timeout and returns a Gemini "functionResponse" part.
// A failing tool does NOT crash the agent: the error is sent back to the model
// so it can try something else.
func execute(ctx context.Context, tools map[string]Tool, call ToolCall) json.RawMessage {
	log.Printf("  -> %s %s", call.Name, string(call.Args))

	response := map[string]string{}

	tool, ok := tools[call.Name]
	if !ok {
		response["error"] = "unknown tool: " + call.Name
	} else {
		toolCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()

		out, err := tool.Run(toolCtx, call.Args)
		switch {
		case err != nil:
			log.Printf("  !! %s failed: %v", call.Name, err)
			response["error"] = err.Error()
		case out == "":
			response["result"] = "(empty result)"
		default:
			response["result"] = out
		}
	}

	part, _ := json.Marshal(map[string]any{
		"functionResponse": map[string]any{"name": call.Name, "response": response},
	})
	return part
}
