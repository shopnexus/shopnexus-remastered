package litellm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"shopnexus/internal/provider/llm"
)

// maxSSELine caps one server-sent event line; a chunk carrying a long tool-call
// argument fragment can be well past bufio's default 64 KiB.
const maxSSELine = 1 << 20

// Stream runs a chat completion with server-sent events, yielding one Chunk per
// event until the proxy sends "[DONE]". The response body is closed when
// iteration ends, including on an early break.
func (c *Client) Stream(ctx context.Context, params llm.CompleteParams) iter.Seq2[llm.Chunk, error] {
	return func(yield func(llm.Chunk, error) bool) {
		// The budget covers the whole stream, so the deadline is set inside the
		// iterator and released when iteration ends — including on early break.
		ctx, cancel := context.WithTimeout(ctx, c.streamTimeout)
		defer cancel()

		resp, err := c.post(ctx, chatPath, c.buildChatRequest(params, true), true)
		if err != nil {
			yield(llm.Chunk{}, fmt.Errorf("stream chat completion: %w", err))
			return
		}
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxSSELine)
		for scanner.Scan() {
			// Events are separated by blank lines and may carry comment lines
			// (": keep-alive"); only "data:" lines hold a chunk.
			payload, ok := sseData(scanner.Text())
			if !ok {
				continue
			}
			if payload == "[DONE]" {
				return
			}
			var event streamResponse
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				yield(llm.Chunk{}, fmt.Errorf("decode stream chunk: %w", err))
				return
			}
			chunk, ok := event.toChunk()
			if !ok {
				continue
			}
			if !yield(chunk, nil) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			yield(llm.Chunk{}, fmt.Errorf("read stream: %w", err))
		}
	}
}

// sseData extracts the payload of a "data:" line, reporting false for any other line.
func sseData(line string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// toChunk maps one stream event, reporting false for events that carry nothing
// (keep-alive style chunks with an empty delta and no usage).
func (e streamResponse) toChunk() (llm.Chunk, bool) {
	chunk := llm.Chunk{}
	if e.Usage != nil {
		usage := e.Usage.toUsage()
		chunk.Usage = &usage
	}
	if len(e.Choices) > 0 {
		choice := e.Choices[0]
		chunk.ContentDelta = choice.Delta.Content
		chunk.FinishReason = toFinishReason(choice.FinishReason)
		for _, tc := range choice.Delta.ToolCalls {
			chunk.ToolCalls = append(chunk.ToolCalls, llm.ToolCallDelta{
				Index:          tc.Index,
				ID:             tc.ID,
				Name:           tc.Function.Name,
				ArgumentsDelta: tc.Function.Arguments,
			})
		}
	}
	empty := chunk.ContentDelta == "" && len(chunk.ToolCalls) == 0 && chunk.FinishReason == "" && chunk.Usage == nil
	return chunk, !empty
}
