# Web Research Agent (Go + Google Gemini)

A small command-line AI agent written in Go. You give it a topic; it searches the web, reads a few pages, and prints a short summary with sources.

The goal of this project is to show the **agent loop** in plain Go: the LLM decides which tool to call, Go runs the tool, and the result goes back to the LLM. This repeats until the LLM has enough information to answer.

**Highlights**

- Uses the Google Gemini API with function calling (plain `net/http`, no SDK)
- Standard library only, no third-party Go dependencies
- API key and settings loaded from a `.env` file (tiny built-in loader)
- Tool calls from the same model turn run in parallel with goroutines
- Per-tool timeouts, retry on rate limits, and unit tests

## Contents

- [How it works](#how-it-works)
- [Requirements](#requirements)
- [Setup](#setup)
- [Usage](#usage)
- [Configuration](#configuration)
- [Project structure](#project-structure)
- [Running the tests](#running-the-tests)
- [Go concepts used](#go-concepts-used)
- [Adding your own tool](#adding-your-own-tool)
- [Notes on the Gemini API](#notes-on-the-gemini-api)
- [Troubleshooting](#troubleshooting)
- [Limitations](#limitations)
- [Ideas to extend](#ideas-to-extend)

## How it works

```
 your topic
     |
     v
 +-----------+  functionCall (search_web / fetch_url)  +--------------+
 |  Gemini   | -------------------------------------> |  Go tools    |
 |   model   | <------------------------------------- |  (parallel)  |
 +-----------+           functionResponse             +--------------+
     |
     | no more tools needed
     v
 final summary + source URLs
```

1. The program sends your topic and the list of tools (name, description, input schema) to Gemini.
2. If the reply contains `functionCall` parts, the agent runs those tools **in parallel**, one goroutine per call, each with a 20-second timeout.
3. Each result (or error) is sent back to the model as a `functionResponse`.
4. When the model replies with plain text, that text is the final answer.
5. The `-steps` limit (default 8) prevents an endless loop.

If a tool fails (for example a page cannot be downloaded), the error is passed to the model instead of crashing the program, so the model can try another source.

### Tools

| Tool | What it does |
|---|---|
| `search_web` | Searches DuckDuckGo (no API key needed) and returns the top 5 results with title, URL and snippet |
| `fetch_url` | Downloads a page (max 1 MB), strips the HTML, and returns up to 6000 characters of text |

## Requirements

- Go 1.22 or newer
- A Gemini API key. You can create one in Google AI Studio: https://aistudio.google.com/apikey

## Setup

```bash
# 1. Get the code and enter the folder
cd web-research-agent

# 2. Create your .env file from the template
cp .env.example .env        # Windows CMD: copy .env.example .env

# 3. Open .env and paste your key
#    GEMINI_API_KEY=your_real_key
```

There is nothing to install: the project has no external dependencies, so `go mod tidy` is not needed.

> **Security:** `.env` is listed in `.gitignore` so your key is not committed. Never push your real key to GitHub. If you leak it by mistake, delete it in AI Studio and create a new one.

## Usage

```bash
go run . "What is new in Go 1.23?"
```

Allow more steps for bigger topics:

```bash
go run . -steps 12 "Compare gRPC and REST for microservices"
```

Build a binary:

```bash
go build -o research
./research "How does the Go garbage collector work?"
```

(Run the binary from the project folder, or copy `.env` next to it, because `.env` is read from the current directory.)

While it runs, progress logs go to **stderr** (which tool is being called). The final answer goes to **stdout**, so you can save it:

```bash
go run . "Go generics explained" > report.txt
```

Example of what the progress logs look like (the exact output will differ):

```
step 1: model wants to use 1 tool(s)
  -> search_web {"query":"Go 1.23 new features"}
step 2: model wants to use 2 tool(s)
  -> fetch_url {"url":"https://..."}
  -> fetch_url {"url":"https://..."}
```

## Configuration

Settings are read from the `.env` file. A variable that is already set in your real environment takes priority over `.env`.

| Variable | Required | Default | Description |
|---|---|---|---|
| `GEMINI_API_KEY` | Yes | none | Your Gemini API key |
| `GEMINI_MODEL` | No | `gemini-3.8-flash` | Gemini model name |

Command-line flags:

| Flag | Default | Description |
|---|---|---|
| `-steps` | `8` | Maximum number of agent steps |

`.env` format: one `KEY=value` per line, `#` for comment lines, optional quotes around values. Inline comments (`KEY=value # note`) are not supported.

## Project structure

```
main.go         CLI entry point: flags, loads .env, starts the agent
agent.go        Agent loop, reading model replies, parallel tool runner
gemini.go       Gemini API types and the generateContent call (with retry)
tools.go        Tool interface, search_web, fetch_url, HTML helpers
env.go          Minimal .env file loader
agent_test.go   Unit tests
.env.example    Template for your settings
```

## Running the tests

```bash
go test ./...
```

The tests do not use the network or the Gemini API. They cover HTML cleaning, search-result parsing, the `.env` loader, parsing of model replies, and parallel tool execution (result order and error handling).

## Go concepts used

- **Interfaces:** `Tool` is an interface; a new tool only needs 4 methods.
- **Goroutines and `sync.WaitGroup`:** tool calls from one model turn run concurrently; each goroutine writes only to its own slice index, so no mutex is needed.
- **`context`:** a 3-minute deadline for the whole run and a 20-second timeout for each tool.
- **Error handling:** errors are wrapped with `%w`, and tool failures are returned to the model instead of stopping the program.
- **`encoding/json`:** struct tags, `json.RawMessage` to pass through data unchanged, building requests with `map[string]any`.
- **`net/http`:** requests with context, headers, body size limits with `io.LimitReader`.
- **Regular expressions** for simple HTML cleaning and result parsing.
- **Testing:** the standard `testing` package, `t.TempDir`, `t.Setenv`, and a fake tool.

## Adding your own tool

1. Create a type in `tools.go` that implements `Tool` (for example `weatherTool`): `Name`, `Description`, `Schema`, `Run`.
2. Register it in `main.go`:

```go
agent := NewAgent([]Tool{searchTool{}, fetchTool{}, weatherTool{}}, *steps)
```

Gemini learns about the new tool from its name, description and input schema. Write a clear description: it is what the model uses to decide when to call the tool.

## Notes on the Gemini API

- This project calls the REST endpoint `v1beta/models/{model}:generateContent` with the `x-goog-api-key` header.
- Google's newest documentation also describes a newer **Interactions API** for function calling. This project uses `generateContent` because it is simple and widely documented. All Gemini-specific code is in `gemini.go` (plus the small part-parsing helpers in `agent.go`), so switching later means changing those files only.
- Gemini 3 models return extra data (thought signatures) inside function-call parts. The program keeps message parts as raw JSON and sends them back unchanged, which is why `Content.Parts` is `[]json.RawMessage`.
- Model names and free-tier limits change often. If the default model is not available for your key, pick another name from https://ai.google.dev/gemini-api/docs/models and set `GEMINI_MODEL` in `.env`.
- When the API answers HTTP 429 (rate limit) or 503 (busy), the program waits 2, 4, then 8 seconds and retries.

## Troubleshooting

| Problem | Likely cause and fix |
|---|---|
| `GEMINI_API_KEY is not set` | Create `.env` from `.env.example` and run the command from the project folder |
| `API key not valid` | Check for extra spaces or quotes in `.env`; create a new key in AI Studio |
| `model ... not found` (HTTP 404) | Set `GEMINI_MODEL` in `.env` to a model listed in the Gemini docs |
| HTTP 429 after retries | Free-tier rate limit; wait a minute or use a smaller model |
| Search returns `HTTP 202` or "No results found" | DuckDuckGo may be blocking automated requests; try again later or replace `searchTool.Run` with an official search API |
| `stopped: max steps reached` | Use a larger `-steps` value or a more specific topic |
| Empty or short page text | The page needs JavaScript to render; the agent will usually try another source |

## Limitations

- Search uses DuckDuckGo's HTML page, which is not an official API. Its layout can change and it can block automated requests.
- Pages that need JavaScript return little or no text.
- `fetch_url` accepts only `http` and `https` URLs, but it has no protection against requests to internal network addresses. Do not expose this agent as a public web service as it is.
- The summary is only as good as the pages the agent reads. Always check the listed sources.
- There is no memory between runs.
- The code has been written to be simple, not to be a production system.

## Ideas to extend

- Save the report to a Markdown file with an `-out` flag
- Add an HTTP endpoint or a small web page
- Cache fetched pages
- Replace DuckDuckGo with an official search API
- Add a `Dockerfile`
- Add a `read_file` tool for local notes
