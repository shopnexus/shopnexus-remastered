package eventbus

import "testing"

// JetStream rejects durable names containing dots, wildcards, path separators or
// whitespace — and topics are dotted by convention.
func TestNATSConsumerName(t *testing.T) {
	cases := map[string]string{
		"telemetry.http_requests": "observability-writer_telemetry_http_requests",
		"order.placed":            "observability-writer_order_placed",
		"a>b*c/d\\e f":            "observability-writer_a_b_c_d_e_f",
	}
	for topic, want := range cases {
		if got := natsConsumerName("observability-writer", topic); got != want {
			t.Errorf("natsConsumerName(%q) = %q, want %q", topic, got, want)
		}
	}
}
