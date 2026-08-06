package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type toolStreamRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn toolStreamRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type cancelAsEOFBody struct {
	ctx     context.Context
	started chan struct{}
}

func (b *cancelAsEOFBody) Read([]byte) (int, error) {
	close(b.started)
	<-b.ctx.Done()
	return 0, io.EOF
}

func (*cancelAsEOFBody) Close() error { return nil }

func TestToolStreamAccumulatorMergesIndexesAndValidatesArguments(t *testing.T) {
	acc := NewToolStreamAccumulator()
	acc.AddText("answer")
	acc.AddToolCallDelta(1, "call-2", "search", `{"query":`)
	acc.AddToolCallDelta(0, "call-1", "read", `{"path":"a"}`)
	acc.AddToolCallDelta(1, "", "search", `"agent"}`)
	acc.AddToolCallDelta(1, "", "search", "") // repeated complete name is not concatenated
	resp, err := acc.Response()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "answer" || len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("unexpected accumulated response: %+v", resp)
	}
	if resp.Message.ToolCalls[0].ID != "call-1" || resp.Message.ToolCalls[0].Name != "read" {
		t.Fatalf("unexpected first tool call: %+v", resp.Message.ToolCalls[0])
	}
	if resp.Message.ToolCalls[1].ID != "call-2" || resp.Message.ToolCalls[1].Name != "search" || string(resp.Message.ToolCalls[1].Arguments) != `{"query":"agent"}` {
		t.Fatalf("unexpected second tool call: %+v", resp.Message.ToolCalls[1])
	}

	bad := NewToolStreamAccumulator()
	bad.AddToolCallDelta(0, "call", "bad", `{"query":`)
	if _, err := bad.Response(); err == nil {
		t.Fatal("expected invalid tool arguments to fail finalization")
	}
}

func TestOpenAICompatibleStreamChatWithTools(t *testing.T) {
	firstDelta := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization: %s", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		flusher.Flush()
		close(firstDelta)
		<-release
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"search\",\"arguments\":\"{\\\"q\\\":\\\"\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"search\",\"arguments\":\"agent\\\"}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call-2\",\"function\":{\"name\":\"read\",\"arguments\":\"{}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := &OpenAICompatibleChatClient{StreamClient: server.Client()}
	var events []ModelStreamEvent
	resultCh := make(chan struct {
		resp *ToolChatResponse
		err  error
	}, 1)
	go func() {
		resp, err := client.StreamChatWithTools(context.Background(), ChatProviderConfig{
			ProviderType: "openai_compatible",
			BaseURL:      server.URL,
			APIKey:       "test-key",
		}, ToolChatRequest{Model: "test-model"}, func(event ModelStreamEvent) error {
			events = append(events, event)
			return nil
		})
		resultCh <- struct {
			resp *ToolChatResponse
			err  error
		}{resp: resp, err: err}
	}()
	select {
	case <-firstDelta:
	case <-time.After(time.Second):
		t.Fatal("first stream delta was not observed before body completion")
	}
	close(release)
	result := <-resultCh
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.resp.Message.Content != "hello world" || result.resp.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected final response: %+v", result.resp)
	}
	if len(result.resp.Message.ToolCalls) != 2 {
		t.Fatalf("unexpected tool calls: %+v", result.resp.Message.ToolCalls)
	}
	if string(result.resp.Message.ToolCalls[0].Arguments) != `{"q":"agent"}` {
		t.Fatalf("unexpected first arguments: %s", result.resp.Message.ToolCalls[0].Arguments)
	}

	var kinds []ModelStreamKind
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	joined := strings.Join(modelStreamKindsToStrings(kinds), ",")
	for _, want := range []ModelStreamKind{
		ModelTextStart, ModelTextDelta, ModelReasoningStart, ModelReasoningDelta,
		ModelReasoningEnd, ModelTextStart, ModelTextDelta, ModelTextEnd,
		ModelToolCallStart, ModelToolCallDelta, ModelToolCallEnd, ModelDone,
	} {
		if !strings.Contains(joined, string(want)) {
			t.Fatalf("missing %s in event sequence %v", want, kinds)
		}
	}
}

func TestOpenAICompatibleStreamChatWithToolsRequiresDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer server.Close()

	client := &OpenAICompatibleChatClient{StreamClient: server.Client()}
	var gotErrorEvent bool
	_, err := client.StreamChatWithTools(context.Background(), ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: server.URL}, ToolChatRequest{}, func(event ModelStreamEvent) error {
		if event.Kind == ModelError {
			gotErrorEvent = true
		}
		return nil
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) || !gotErrorEvent {
		t.Fatalf("expected unexpected EOF error event, err=%v errorEvent=%v", err, gotErrorEvent)
	}
}

func TestOpenAICompatibleStreamChatWithToolsOrdersInterleavedCallsByIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call-2\",\"function\":{\"name\":\"second\",\"arguments\":\"{}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"name\":\"first\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := &OpenAICompatibleChatClient{StreamClient: server.Client()}
	var endedIndexes []int
	response, err := client.StreamChatWithTools(context.Background(), ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: server.URL}, ToolChatRequest{}, func(event ModelStreamEvent) error {
		if event.Kind == ModelToolCallEnd {
			endedIndexes = append(endedIndexes, event.Index)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Message.ToolCalls) != 2 || response.Message.ToolCalls[0].ID != "call-1" || response.Message.ToolCalls[1].ID != "call-2" {
		t.Fatalf("final calls lost provider index order: %+v", response.Message.ToolCalls)
	}
	if len(endedIndexes) != 2 || endedIndexes[0] != 0 || endedIndexes[1] != 1 {
		t.Fatalf("tool end events lost provider index order: %v", endedIndexes)
	}
}

func TestOpenAICompatibleStreamChatWithToolsPreservesContextCancellation(t *testing.T) {
	started := make(chan struct{})
	streamClient := &http.Client{Transport: toolStreamRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       &cancelAsEOFBody{ctx: request.Context(), started: started},
			Request:    request,
		}, nil
	})}
	client := &OpenAICompatibleChatClient{StreamClient: streamClient}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []ModelStreamEvent
	result := make(chan error, 1)
	go func() {
		_, err := client.StreamChatWithTools(ctx, ChatProviderConfig{ProviderType: "openai_compatible", BaseURL: "http://provider.test"}, ToolChatRequest{}, func(event ModelStreamEvent) error {
			events = append(events, event)
			return nil
		})
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream body was not read")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation must survive transport EOF, got %v", err)
	}
	if len(events) != 1 || events[0].Kind != ModelError || !errors.Is(events[0].Err, context.Canceled) {
		t.Fatalf("cancelled stream must emit one cancellation error: %+v", events)
	}
}

func modelStreamKindsToStrings(kinds []ModelStreamKind) []string {
	result := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		result = append(result, string(kind))
	}
	return result
}
