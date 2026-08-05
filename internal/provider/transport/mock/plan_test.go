package mock

import (
	"context"
	"log/slog"
	"testing"

	"shopnexus/internal/provider/transport"
)

// The checkpoint plan is the edge cases: everything below is a sequence a real carrier produces and
// this platform has a rule about. Timed by the plan rather than by waiting on the clock — a test that
// slept five seconds per case would only be slow.
func TestCheckpoints_TheSequenceEachScenarioReports(t *testing.T) {
	cases := []struct {
		option string
		want   []string
	}{
		{OptionStandard, []string{"processing", "success"}},
		// Accepted and then quiet: what an escrow window has to survive.
		{OptionStuck, []string{"processing"}},
		{OptionFailedDelivery, []string{"processing", "failed"}},
		// A carrier retries until it gets a 200, so the same checkpoint arrives twice and the second
		// must change nothing.
		{OptionRetried, []string{"processing", "processing", "success", "success"}},
		// Out of order, which they are: nothing may un-deliver a delivered parcel.
		{OptionLateCheckpoint, []string{"processing", "success", "processing"}},
		// A word nobody agreed the meaning of — ignored, not guessed at.
		{OptionUnknownStatus, []string{"processing", "held-at-customs"}},
	}
	for _, tc := range cases {
		t.Run(tc.option, func(t *testing.T) {
			got := scenarioFor(tc.option).checkpoints()
			if len(got) != len(tc.want) {
				t.Fatalf("checkpoints = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("checkpoint %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// A report the order module refused stops the rest of the plan: repeating it would only repeat the
// failure, and nothing here is the record of anything.
func TestCheckpoints_ARefusedReportStopsThePlan(t *testing.T) {
	count := 0
	hookMu.Lock()
	deliverHook = func(context.Context, transport.WebhookResult) error {
		count++
		return context.DeadlineExceeded
	}
	hookMu.Unlock()

	courier := &Client{log: slog.New(slog.DiscardHandler)}
	courier.reportPlan(scenarioFor(OptionRetried).checkpoints(), "MOCKTEST")

	if count != 1 {
		t.Fatalf("reported %d checkpoints, want to stop after the first refusal", count)
	}
}
