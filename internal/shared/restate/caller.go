package restatec

import (
	"context"

	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/ingress"
)

// CallClient makes request-response calls to Restate services via the ingress endpoint.
type CallClient struct {
	ingress *ingress.Client
}

func NewCallClient(baseURL string) *CallClient {
	return &CallClient{ingress: newIngressClient(baseURL)}
}

// Call invokes a Restate service method and waits for its result.
func Call[O any](ctx context.Context, c *CallClient, service, method string, input any) (O, error) {
	if rctx, ok := ctx.(restate.Context); ok {
		return restate.Service[O](rctx, service, method).Request(input)
	}
	return ingress.Service[any, O](c.ingress, service, method).Request(ctx, input)
}

// CallVoid invokes a Restate service method, waits for completion, and discards the result.
func CallVoid(ctx context.Context, c *CallClient, service, method string, input any) error {
	if rctx, ok := ctx.(restate.Context); ok {
		_, err := restate.Service[restate.Void](rctx, service, method).Request(input)
		return err
	}
	_, err := ingress.Service[any, restate.Void](c.ingress, service, method).Request(ctx, input)
	return err
}

// CallWorkflow invokes a workflow handler keyed by workflowID and waits for its result.
func CallWorkflow[O any](ctx context.Context, c *CallClient, service, workflowID, method string, input any) (O, error) {
	if rctx, ok := ctx.(restate.Context); ok {
		return restate.Workflow[O](rctx, service, workflowID, method).Request(input)
	}
	return ingress.Workflow[any, O](c.ingress, service, workflowID, method).Request(ctx, input)
}

// CallWorkflowVoid invokes a workflow handler keyed by workflowID, waits for completion, and discards the result.
func CallWorkflowVoid(ctx context.Context, c *CallClient, service, workflowID, method string, input any) error {
	if rctx, ok := ctx.(restate.Context); ok {
		_, err := restate.Workflow[restate.Void](rctx, service, workflowID, method).Request(input)
		return err
	}
	_, err := ingress.Workflow[any, restate.Void](c.ingress, service, workflowID, method).Request(ctx, input)
	return err
}
