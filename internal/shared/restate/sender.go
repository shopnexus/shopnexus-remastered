package restatec

import (
	"context"

	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/ingress"
)

// SendClient makes one-way (fire-and-forget) calls to Restate services via the ingress endpoint.
type SendClient struct {
	ingress *ingress.Client
}

func NewSendClient(baseURL string) *SendClient {
	return &SendClient{ingress: newIngressClient(baseURL)}
}

// Send invokes a Restate service method one-way; the result is discarded.
// Over HTTP ingress this returns as soon as the invocation is durably
// enqueued (202 Accepted), not when it completes.
func Send(ctx context.Context, c *SendClient, service, method string, input any) error {
	if rctx, ok := ctx.(restate.Context); ok {
		restate.ServiceSend(rctx, service, method).Send(input)
		return nil
	}
	_, err := ingress.ServiceSend[any](c.ingress, service, method).Send(ctx, input)
	return err
}

// SendWorkflow invokes a workflow handler keyed by workflowID one-way; the result is discarded.
func SendWorkflow(ctx context.Context, c *SendClient, service, workflowID, method string, input any) error {
	if rctx, ok := ctx.(restate.Context); ok {
		restate.WorkflowSend(rctx, service, workflowID, method).Send(input)
		return nil
	}
	_, err := ingress.WorkflowSend[any](c.ingress, service, workflowID, method).Send(ctx, input)
	return err
}
