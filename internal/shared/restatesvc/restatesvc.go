package restatesvc

import (
	"time"

	restate "github.com/restatedev/sdk-go"
)

// Group is the fx value-group every module feeds its Restate service
// definitions into; app.SetupRestate binds the whole group. One binary can
// then run all modules (monolith) or a single module (--module), binding only
// whatever definitions are present.
const Group = `group:"restate"`

// Reflect wraps restate.Reflect with the shared invocation retry policy so
// every module registers services identically.
func Reflect(rcvr any) restate.ServiceDefinition {
	return restate.Reflect(rcvr, restate.WithInvocationRetryPolicy(
		restate.WithInitialInterval(time.Second),
		restate.WithMaxInterval(30*time.Second),
		restate.WithMaxAttempts(10),
		restate.PauseOnMaxAttempts()))
}
