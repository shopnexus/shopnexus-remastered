package litellm_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"shopnexus/internal/provider/llm"
	"shopnexus/internal/provider/llm/litellm"
	"shopnexus/internal/shared/httpx"
)

// newClient starts a stub proxy that serves handler and returns a client aimed at it.
func newClient(t *testing.T, cfg litellm.Config, handler http.HandlerFunc) *litellm.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cfg.BaseURL = srv.URL
	if cfg.APIKey == "" {
		cfg.APIKey = "sk-test"
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 5 * time.Second
	}
	if cfg.StreamTimeout == 0 {
		cfg.StreamTimeout = 10 * time.Second
	}
	c, err := litellm.NewClient(cfg)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

// stall drains the request body, then waits for the client to hang up. Draining
// matters: an HTTP/1 server only notices a disconnect once the body is consumed.
// The cap keeps a missed cancellation from wedging the stub server on teardown.
func stall(r *http.Request) {
	io.Copy(io.Discard, r.Body)
	select {
	case <-r.Context().Done():
	case <-time.After(2 * time.Second):
	}
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return body
}

func TestNewClient_RequiresConfig(t *testing.T) {
	full := litellm.Config{
		BaseURL:        "http://litellm:4000",
		APIKey:         "sk-test",
		RequestTimeout: time.Second,
		StreamTimeout:  time.Minute,
	}
	if _, err := litellm.NewClient(full); err != nil {
		t.Fatalf("full config: %v", err)
	}

	// Every field above is required — no defaults, no fallback.
	for name, mangle := range map[string]func(*litellm.Config){
		"base url":        func(c *litellm.Config) { c.BaseURL = "" },
		"api key":         func(c *litellm.Config) { c.APIKey = "" },
		"request timeout": func(c *litellm.Config) { c.RequestTimeout = 0 },
		"stream timeout":  func(c *litellm.Config) { c.StreamTimeout = 0 },
	} {
		cfg := full
		mangle(&cfg)
		if _, err := litellm.NewClient(cfg); err == nil {
			t.Errorf("expected error when %s is missing", name)
		}
	}
}

func TestComplete_RequestTimeout(t *testing.T) {
	// The handler never answers; it just waits for the client to give up.
	c := newClient(t, litellm.Config{ChatModel: "gpt-5", RequestTimeout: 20 * time.Millisecond},
		func(_ http.ResponseWriter, r *http.Request) { stall(r) })

	_, err := c.Complete(context.Background(), llm.CompleteParams{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

// A deadline already on the caller's context is shorter, so it must win.
func TestComplete_CallerDeadlineWins(t *testing.T) {
	c := newClient(t, litellm.Config{ChatModel: "gpt-5", RequestTimeout: time.Hour},
		func(_ http.ResponseWriter, r *http.Request) { stall(r) })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := c.Complete(ctx, llm.CompleteParams{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s: the client budget overrode the caller's deadline", elapsed)
	}
}

// The stream budget spans the whole read, not just the connect.
func TestStream_TimeoutMidStream(t *testing.T) {
	c := newClient(t, litellm.Config{ChatModel: "gpt-5", StreamTimeout: 50 * time.Millisecond},
		func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `data: {"choices":[{"delta":{"content":"Hel"}}]}`+"\n\n")
			w.(http.Flusher).Flush()
			stall(r) // then go quiet instead of finishing the stream
		})

	var text string
	var gotErr error
	for chunk, err := range c.Stream(context.Background(), llm.CompleteParams{}) {
		if err != nil {
			gotErr = err
			continue
		}
		text += chunk.ContentDelta
	}
	if text != "Hel" {
		t.Errorf("text = %q, want the chunk delivered before the deadline", text)
	}
	if !errors.Is(gotErr, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", gotErr)
	}
}

func TestComplete(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	c := newClient(t, litellm.Config{ChatModel: "gpt-5"}, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		gotBody = decodeBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
			"model": "gpt-5",
			"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": "hi there"}}],
			"usage": {"prompt_tokens": 11, "completion_tokens": 3, "total_tokens": 14}
		}`)
	})

	temp := 0.2
	res, err := c.Complete(context.Background(), llm.CompleteParams{
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Temperature: &temp,
		MaxTokens:   256,
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if gotBody["model"] != "gpt-5" { // configured default applied
		t.Errorf("model = %v", gotBody["model"])
	}
	if gotBody["temperature"] != 0.2 || gotBody["max_tokens"] != float64(256) {
		t.Errorf("knobs not forwarded: %v", gotBody)
	}
	if _, ok := gotBody["stream"]; ok {
		t.Errorf("non-streaming request must not set stream: %v", gotBody)
	}
	if res.Message.Content != "hi there" || res.Message.Role != llm.RoleAssistant {
		t.Errorf("message = %+v", res.Message)
	}
	if res.FinishReason != llm.FinishReasonStop {
		t.Errorf("finish reason = %q", res.FinishReason)
	}
	if res.Usage != (llm.Usage{PromptTokens: 11, CompletionTokens: 3, TotalTokens: 14}) {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestComplete_ToolCallsAndSchema(t *testing.T) {
	var gotBody map[string]any
	c := newClient(t, litellm.Config{ChatModel: "gpt-5"}, func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{
			"model": "gemini/gemini-2.5-pro",
			"choices": [{"finish_reason": "tool_calls", "message": {
				"role": "assistant",
				"content": "",
				"tool_calls": [{"id": "call_1", "type": "function",
					"function": {"name": "search_products", "arguments": "{\"q\":\"shoes\"}"}}]
			}}]
		}`)
	})

	res, err := c.Complete(context.Background(), llm.CompleteParams{
		Model:    "gemini/gemini-2.5-pro", // per-request override
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "find shoes"}},
		Tools: []llm.Tool{{
			Name:        "search_products",
			Description: "search the catalog",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}},
		ToolChoice:     llm.ToolChoiceRequired,
		ResponseFormat: &llm.ResponseFormat{Name: "hit", Schema: json.RawMessage(`{"type":"object"}`), Strict: true},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	if gotBody["model"] != "gemini/gemini-2.5-pro" {
		t.Errorf("model override ignored: %v", gotBody["model"])
	}
	if gotBody["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v", gotBody["tool_choice"])
	}
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", gotBody["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["function"].(map[string]any)["name"] != "search_products" {
		t.Errorf("tool = %v", tool)
	}
	format := gotBody["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("response_format = %v", format)
	}
	if schema := format["json_schema"].(map[string]any); schema["name"] != "hit" || schema["strict"] != true {
		t.Errorf("json_schema = %v", schema)
	}

	if res.FinishReason != llm.FinishReasonToolCalls {
		t.Errorf("finish reason = %q, want tool-calls", res.FinishReason)
	}
	if len(res.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", res.Message.ToolCalls)
	}
	call := res.Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "search_products" || string(call.Arguments) != `{"q":"shoes"}` {
		t.Errorf("tool call = %+v", call)
	}
}

// A tool result turn must round-trip as role=tool with its tool_call_id.
func TestComplete_SendsToolResultTurn(t *testing.T) {
	var gotBody map[string]any
	c := newClient(t, litellm.Config{ChatModel: "gpt-5"}, func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`)
	})

	_, err := c.Complete(context.Background(), llm.CompleteParams{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "find shoes"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "search_products", Arguments: json.RawMessage(`{"q":"shoes"}`)}}},
		{Role: llm.RoleTool, ToolCallID: "call_1", Content: `{"hits":2}`},
	}})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	msgs := gotBody["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %v", msgs)
	}
	assistant := msgs[1].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["arguments"] != `{"q":"shoes"}` { // arguments must be a JSON string, not an object
		t.Errorf("arguments = %#v", fn["arguments"])
	}
	toolTurn := msgs[2].(map[string]any)
	if toolTurn["role"] != "tool" || toolTurn["tool_call_id"] != "call_1" {
		t.Errorf("tool turn = %v", toolTurn)
	}
}

func TestComplete_APIError(t *testing.T) {
	c := newClient(t, litellm.Config{ChatModel: "gpt-5"}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limit exceeded","type":"rate_limit_error","code":429}}`)
	})

	_, err := c.Complete(context.Background(), llm.CompleteParams{})
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *llm.APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || apiErr.Message != "rate limit exceeded" {
		t.Errorf("api error = %+v", apiErr)
	}
	if apiErr.Code != "429" || apiErr.Type != "rate_limit_error" {
		t.Errorf("api error = %+v", apiErr)
	}
}

// A gateway in front of the proxy can fail with a non-JSON body.
func TestComplete_APIErrorPlainBody(t *testing.T) {
	c := newClient(t, litellm.Config{}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "upstream unreachable")
	})

	_, err := c.Complete(context.Background(), llm.CompleteParams{Model: "gpt-5"})
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *llm.APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadGateway || apiErr.Message != "upstream unreachable" {
		t.Errorf("api error = %+v", apiErr)
	}
}

func TestComplete_NoChoices(t *testing.T) {
	c := newClient(t, litellm.Config{ChatModel: "gpt-5"}, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"model":"gpt-5","choices":[]}`)
	})

	if _, err := c.Complete(context.Background(), llm.CompleteParams{}); err == nil {
		t.Fatal("expected error on empty choices")
	}
}

func TestStream(t *testing.T) {
	var gotBody map[string]any
	var gotAccept string
	c := newClient(t, litellm.Config{ChatModel: "gpt-5"}, func(w http.ResponseWriter, r *http.Request) {
		gotBody, gotAccept = decodeBody(t, r), r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, ": keep-alive\n\n"+
			`data: {"choices":[{"delta":{"content":"Hel"}}]}`+"\n\n"+
			`data: {"choices":[{"delta":{"content":"lo"}}]}`+"\n\n"+
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n"+
			`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`+"\n\n"+
			"data: [DONE]\n\n")
	})

	var text string
	var finish llm.FinishReason
	var usage *llm.Usage
	for chunk, err := range c.Stream(context.Background(), llm.CompleteParams{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		text += chunk.ContentDelta
		if chunk.FinishReason != "" {
			finish = chunk.FinishReason
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}

	if gotAccept != "text/event-stream" {
		t.Errorf("accept = %q", gotAccept)
	}
	if gotBody["stream"] != true {
		t.Errorf("stream flag = %v", gotBody["stream"])
	}
	if opts, ok := gotBody["stream_options"].(map[string]any); !ok || opts["include_usage"] != true {
		t.Errorf("stream_options = %v", gotBody["stream_options"])
	}
	if text != "Hello" {
		t.Errorf("text = %q", text)
	}
	if finish != llm.FinishReasonStop {
		t.Errorf("finish reason = %q", finish)
	}
	if usage == nil || usage.TotalTokens != 7 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestStream_ToolCallDeltas(t *testing.T) {
	c := newClient(t, litellm.Config{ChatModel: "gpt-5"}, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search_products","arguments":"{\"q\":"}}]}}]}`+"\n\n"+
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"shoes\"}"}}]}}]}`+"\n\n"+
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n"+
				"data: [DONE]\n\n")
	})

	var acc llm.ToolCallAccumulator
	for chunk, err := range c.Stream(context.Background(), llm.CompleteParams{}) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		acc.Add(chunk.ToolCalls...)
	}

	calls := acc.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].ID != "call_1" || calls[0].Name != "search_products" || string(calls[0].Arguments) != `{"q":"shoes"}` {
		t.Errorf("call = %+v", calls[0])
	}
}

func TestStream_RequestErrorIsYielded(t *testing.T) {
	c := newClient(t, litellm.Config{ChatModel: "gpt-5"}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid key","type":"auth_error","code":"invalid_api_key"}}`)
	})

	var chunks int
	var gotErr error
	for chunk, err := range c.Stream(context.Background(), llm.CompleteParams{}) {
		if err != nil {
			gotErr = err
			continue
		}
		_ = chunk
		chunks++
	}
	if chunks != 0 {
		t.Errorf("chunks = %d, want 0", chunks)
	}
	var apiErr *llm.APIError
	if !errors.As(gotErr, &apiErr) || apiErr.Code != "invalid_api_key" {
		t.Fatalf("err = %v", gotErr)
	}
}

// Breaking out of the loop must stop consuming without hanging or leaking.
func TestStream_EarlyBreak(t *testing.T) {
	c := newClient(t, litellm.Config{ChatModel: "gpt-5"}, func(w http.ResponseWriter, _ *http.Request) {
		for range 100 {
			io.WriteString(w, `data: {"choices":[{"delta":{"content":"x"}}]}`+"\n\n")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	})

	var seen int
	for chunk, err := range c.Stream(context.Background(), llm.CompleteParams{}) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if chunk.ContentDelta != "" {
			seen++
		}
		if seen == 2 {
			break
		}
	}
	if seen != 2 {
		t.Errorf("seen = %d, want 2", seen)
	}
}

func TestEmbed(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	c := newClient(t, litellm.Config{EmbedModel: "mgte"}, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotBody = r.URL.Path, decodeBody(t, r)
		// Out-of-order data must be mapped back onto the input order.
		io.WriteString(w, `{
			"model": "mgte",
			"data": [{"index": 1, "embedding": [0.3, 0.4]}, {"index": 0, "embedding": [0.1, 0.2]}],
			"usage": {"prompt_tokens": 4, "total_tokens": 4}
		}`)
	})

	res, err := c.Embed(context.Background(), llm.EmbedParams{Input: []string{"a", "b"}, Dimensions: 2})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	if gotPath != "/v1/embeddings" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["model"] != "mgte" || gotBody["encoding_format"] != "float" || gotBody["dimensions"] != float64(2) {
		t.Errorf("body = %v", gotBody)
	}
	if len(res.Vectors) != 2 || res.Vectors[0][0] != 0.1 || res.Vectors[1][0] != 0.3 {
		t.Errorf("vectors = %v", res.Vectors)
	}
	if res.Usage.PromptTokens != 4 {
		t.Errorf("usage = %+v", res.Usage)
	}
}

func TestEmbed_VectorCountMismatch(t *testing.T) {
	c := newClient(t, litellm.Config{EmbedModel: "mgte"}, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"model":"mgte","data":[{"index":0,"embedding":[0.1]}]}`)
	})

	if _, err := c.Embed(context.Background(), llm.EmbedParams{Input: []string{"a", "b"}}); err == nil {
		t.Fatal("expected error when vector count does not match input count")
	}
}

func TestRerank(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	c := newClient(t, litellm.Config{RerankModel: "rerank-v3"}, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotBody = r.URL.Path, decodeBody(t, r)
		io.WriteString(w, `{
			"results": [{"index": 2, "relevance_score": 0.91}, {"index": 0, "relevance_score": 0.42}],
			"meta": {"tokens": {"input_tokens": 30, "output_tokens": 0}}
		}`)
	})

	res, err := c.Rerank(context.Background(), llm.RerankParams{
		Query:     "running shoes",
		Documents: []string{"a", "b", "c"},
		TopN:      2,
	})
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}

	if gotPath != "/v1/rerank" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["model"] != "rerank-v3" || gotBody["top_n"] != float64(2) || gotBody["query"] != "running shoes" {
		t.Errorf("body = %v", gotBody)
	}
	if len(res.Hits) != 2 || res.Hits[0].Index != 2 || res.Hits[0].Score != 0.91 {
		t.Errorf("hits = %+v", res.Hits)
	}
	if res.Model != "rerank-v3" || res.Usage.TotalTokens != 30 {
		t.Errorf("result = %+v", res)
	}
}

// A base URL with a trailing slash must not produce a doubled path separator.
func TestBaseURLTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	c, err := litellm.NewClient(litellm.Config{
		BaseURL:        srv.URL + "/",
		APIKey:         "sk-test",
		ChatModel:      "gpt-5",
		RequestTimeout: 5 * time.Second,
		StreamTimeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := c.Complete(context.Background(), llm.CompleteParams{}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
}

// The client takes its instrumentation from the transport, so observing every
// call needs no change inside the client itself.
func TestObservedTransport(t *testing.T) {
	var mu sync.Mutex
	var seen []httpx.OutboundCall
	observe := func(_ context.Context, call httpx.OutboundCall) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, call)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/embeddings" {
			io.WriteString(w, `{"model":"mgte","data":[{"index":0,"embedding":[0.1]}]}`)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":{"message":"model overloaded","type":"server_error"}}`)
	}))
	defer srv.Close()

	c, err := litellm.NewClient(litellm.Config{
		BaseURL:        srv.URL,
		APIKey:         "sk-test",
		ChatModel:      "gpt-5",
		EmbedModel:     "mgte",
		RequestTimeout: 5 * time.Second,
		StreamTimeout:  10 * time.Second,
		HTTPClient:     &http.Client{Transport: httpx.ObserveOutbound("litellm", nil, observe)},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if _, err := c.Embed(context.Background(), llm.EmbedParams{Input: []string{"a"}}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if _, err := c.Complete(context.Background(), llm.CompleteParams{}); err == nil {
		t.Fatal("expected the 503 to surface")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("observed %d calls, want 2: %+v", len(seen), seen)
	}
	if seen[0].Path != "/v1/embeddings" || seen[0].StatusCode != http.StatusOK || seen[0].Failed() {
		t.Errorf("embed call = %+v", seen[0])
	}
	if seen[1].Path != "/v1/chat/completions" || seen[1].StatusCode != http.StatusServiceUnavailable || !seen[1].Failed() {
		t.Errorf("chat call = %+v", seen[1])
	}
	if seen[0].Provider != "litellm" || seen[1].Duration <= 0 {
		t.Errorf("calls = %+v", seen)
	}
}
