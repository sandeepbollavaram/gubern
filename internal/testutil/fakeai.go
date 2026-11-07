//go:build integration

package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// FakeAI is a deterministic, Ollama-compatible AI provider for integration
// tests. A real gubern server is pointed at it with GUBERN_OLLAMA_URL, so agent
// paths (chat, /agent tool loops, approvals) run end to end without a model,
// a network or an API key, and give the same answer every time.
//
// Behaviour is driven by directives in the user's own message, so a test reads
// as what it does:
//
//   - /api/generate (text completion, streamed or not): replies
//     "fake-ai: ok", or the text of a [[reply:...]] directive in the prompt.
//     The coordinator's intent classifier gets GENERAL_QUESTION, or the
//     intent named by an [[intent:...]] directive.
//   - /api/chat (native tool calling): when the conversation ends with a tool
//     result, replies "fake-ai: done" (the run finishes). Otherwise, when the
//     latest user message has a [[tool:NAME {json args}]] directive, proposes
//     exactly that tool call; else replies "fake-ai: ok".
//
// FakeAI never echoes request bodies into test logs: they can hold workspace
// content, and nothing here needs them beyond the counters.
type FakeAI struct {
	URL string

	mu       sync.Mutex
	generate int
	chat     int
	tools    []string // tool names proposed, in order
}

var (
	replyDirective  = regexp.MustCompile(`\[\[reply:([^\]]*)\]\]`)
	intentDirective = regexp.MustCompile(`\[\[intent:([A-Z_]+)\]\]`)
	toolDirective   = regexp.MustCompile(`\[\[tool:([a-z_]+)\s+(\{.*?\})\]\]`)
)

// StartFakeAI starts the fake provider and points every gubern server this test
// starts at it. Other provider variables are blanked for the test, so a
// developer's real GUBERN_ANTHROPIC_KEY (higher priority than Ollama) can't
// make the run non-deterministic or spend money.
func StartFakeAI(t *testing.T) *FakeAI {
	t.Helper()
	f := &FakeAI{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/generate", f.handleGenerate)
	mux.HandleFunc("/api/chat", f.handleChat)
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"models": []map[string]any{{"name": "llama3"}}})
	})
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"version": "fake"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.URL = srv.URL

	for _, k := range []string{
		"GUBERN_ANTHROPIC_KEY", "GUBERN_OPENAI_KEY", "GUBERN_DEEPSEEK_KEY", "GUBERN_GEMINI_KEY",
		"GUBERN_GROQ_KEY", "GUBERN_BEDROCK_REGION", "GUBERN_AZURE_OPENAI_KEY", "GUBERN_OPENROUTER_KEY",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("GUBERN_OLLAMA_URL", f.URL)
	return f
}

// Counts returns how many text-completion and tool-chat calls the fake served.
func (f *FakeAI) Counts() (generate, chat int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.generate, f.chat
}

// ProposedTools returns the tool names the fake has proposed, in order.
func (f *FakeAI) ProposedTools() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.tools...)
}

func (f *FakeAI) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
		System string `json:"system"`
		Stream bool   `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.generate++
	f.mu.Unlock()

	text := "fake-ai: ok"
	switch {
	case strings.Contains(req.System, "Classify the user's request"):
		// The coordinator's intent classifier: a plain question unless the
		// message names another intent with [[intent:...]].
		text = "GENERAL_QUESTION"
		if m := intentDirective.FindStringSubmatch(req.Prompt); m != nil {
			text = m[1]
		}
	case replyDirective.MatchString(req.Prompt):
		text = replyDirective.FindStringSubmatch(req.Prompt)[1]
	}
	if !req.Stream {
		writeJSON(w, map[string]any{"response": text, "done": true, "eval_count": len(strings.Fields(text))})
		return
	}
	// Ollama streams NDJSON: one object per line, then a final done line.
	w.Header().Set("Content-Type", "application/x-ndjson")
	enc := json.NewEncoder(w)
	for _, word := range strings.SplitAfter(text, " ") {
		_ = enc.Encode(map[string]any{"response": word, "done": false})
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}
	_ = enc.Encode(map[string]any{"response": "", "done": true, "eval_count": len(strings.Fields(text))})
}

func (f *FakeAI) handleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.chat++
	f.mu.Unlock()

	reply := func(msg map[string]any, reason string) {
		writeJSON(w, map[string]any{
			"message": msg, "done": true, "done_reason": reason,
			"eval_count": 3, "prompt_eval_count": 7,
		})
	}
	n := len(req.Messages)
	if n > 0 && req.Messages[n-1].Role == "tool" {
		reply(map[string]any{"role": "assistant", "content": "fake-ai: done"}, "stop")
		return
	}
	for i := n - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		m := toolDirective.FindStringSubmatch(req.Messages[i].Content)
		if m == nil {
			break
		}
		var args map[string]any
		if json.Unmarshal([]byte(m[2]), &args) != nil {
			break
		}
		f.mu.Lock()
		f.tools = append(f.tools, m[1])
		f.mu.Unlock()
		reply(map[string]any{
			"role": "assistant", "content": "",
			"tool_calls": []map[string]any{{"function": map[string]any{"name": m[1], "arguments": args}}},
		}, "stop")
		return
	}
	reply(map[string]any{"role": "assistant", "content": "fake-ai: ok"}, "stop")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
