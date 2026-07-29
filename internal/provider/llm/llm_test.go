package llm_test

import (
	"testing"

	"shopnexus/internal/provider/llm"
)

func TestToolCallAccumulator_ParallelCalls(t *testing.T) {
	var acc llm.ToolCallAccumulator
	// Two calls interleaved across chunks, as providers stream them.
	acc.Add(llm.ToolCallDelta{Index: 0, ID: "a", Name: "search", ArgumentsDelta: `{"q":`})
	acc.Add(llm.ToolCallDelta{Index: 1, ID: "b", Name: "quote", ArgumentsDelta: `{"id`})
	acc.Add(
		llm.ToolCallDelta{Index: 0, ArgumentsDelta: `"shoes"}`},
		llm.ToolCallDelta{Index: 1, ArgumentsDelta: `":7}`},
	)

	calls := acc.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].ID != "a" || calls[0].Name != "search" || string(calls[0].Arguments) != `{"q":"shoes"}` {
		t.Errorf("call 0 = %+v", calls[0])
	}
	if calls[1].ID != "b" || calls[1].Name != "quote" || string(calls[1].Arguments) != `{"id":7}` {
		t.Errorf("call 1 = %+v", calls[1])
	}
}

// Providers may start at a non-zero index; the gap must not panic.
func TestToolCallAccumulator_SparseIndexes(t *testing.T) {
	var acc llm.ToolCallAccumulator
	acc.Add(llm.ToolCallDelta{Index: 2, ID: "c", Name: "third", ArgumentsDelta: `{}`})

	calls := acc.Calls()
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[2].Name != "third" || calls[0].Name != "" {
		t.Errorf("calls = %+v", calls)
	}
}

func TestToolCallAccumulator_EmptyIsNil(t *testing.T) {
	var acc llm.ToolCallAccumulator
	if calls := acc.Calls(); calls != nil {
		t.Errorf("calls = %+v, want nil", calls)
	}
}

func TestAPIError_Message(t *testing.T) {
	withCode := &llm.APIError{StatusCode: 429, Code: "rate_limit", Message: "slow down"}
	if got := withCode.Error(); got != "llm api error 429 (rate_limit): slow down" {
		t.Errorf("error = %q", got)
	}
	bare := &llm.APIError{StatusCode: 500, Message: "boom"}
	if got := bare.Error(); got != "llm api error 500: boom" {
		t.Errorf("error = %q", got)
	}
}
