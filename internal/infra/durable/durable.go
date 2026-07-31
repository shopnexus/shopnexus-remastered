// Package durable is the technology behind the waits this marketplace cannot hold in
// memory: a Restate server that hosts the workflow handlers, an ingress client that submits
// and signals runs, and a sweeper that drives the same transitions on a plain interval.
//
// Two mechanisms on purpose, and not two implementations of the same rule: a run is prompt
// and per entity, the sweep is periodic and in bulk, and both call the same idempotent
// service methods. That is why leaving the sweep on under Restate costs nothing — it finds
// what a lost run would otherwise have stranded, and nothing else.
//
// fx-free, like everything in infra: the providers live in cmd/gateway.
package durable

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/ingress"
	"github.com/restatedev/sdk-go/server"
)

// Run names a durable run to start: the workflow, the key it follows, and its input. A
// module builds one of these rather than naming a URL, so the ingress shape stays here.
type Run struct {
	Workflow string
	Key      string
	Input    any
}

// Signal names a promise resolution on a run that already exists. Input is restate.Void{}
// for a signal that carries nothing.
type Signal struct {
	Workflow string
	Key      string
	Handler  string
	Input    any
}

// Client submits and signals runs over the Restate ingress. Every call is a `send`: the
// caller is inside a request that has already committed its own write, so waiting on a
// workflow to finish would make a durable timer a synchronous dependency.
type Client struct {
	ingress *ingress.Client
	// timeout bounds one ingress call. Its own field rather than the caller's deadline
	// alone, because a runtime that is unreachable must not hold a checkout open.
	timeout time.Duration
	log     *slog.Logger
}

func NewClient(baseURL string, timeout time.Duration, log *slog.Logger) *Client {
	return &Client{ingress: ingress.NewClient(baseURL), timeout: timeout, log: log}
}

// Start submits a run. The workflow key is the idempotency: submitting the same key twice
// attaches to the run that exists rather than starting a second.
func (c *Client) Start(ctx context.Context, run Run) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	sender := ingress.WorkflowSend[any](c.ingress, run.Workflow, run.Key, runHandler)
	if _, err := sender.Send(ctx, run.Input); err != nil {
		return fmt.Errorf("start %s run: %w", run.Workflow, err)
	}
	return nil
}

// Signal resolves a promise on a run. A signal for a run that never started is not an error
// worth failing a request over — the sweep still moves the row — so it is logged and
// swallowed by the caller, not here.
func (c *Client) Signal(ctx context.Context, sig Signal) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	sender := ingress.WorkflowSend[any](c.ingress, sig.Workflow, sig.Key, sig.Handler)
	input := sig.Input
	if input == nil {
		input = restate.Void{}
	}
	if _, err := sender.Send(ctx, input); err != nil {
		return fmt.Errorf("signal %s.%s: %w", sig.Workflow, sig.Handler, err)
	}
	return nil
}

// runHandler is the name every workflow's entry point has. One constant, because a run
// submitted against the wrong handler name fails at the runtime rather than at compile time.
const runHandler = "Run"

// Server hosts the workflow handlers the runtime invokes. The definitions come from the
// modules that own them; this only serves them.
type Server struct {
	restate *server.Restate
	addr    string
	log     *slog.Logger
}

func NewServer(addr string, log *slog.Logger, definitions ...restate.ServiceDefinition) *Server {
	r := server.NewRestate().WithLogger(log.Handler(), true)
	for _, definition := range definitions {
		r = r.Bind(definition)
	}
	return &Server{restate: r, addr: addr, log: log}
}

// Serve blocks until the context is cancelled. Started in a goroutine by whoever owns the
// process lifecycle.
func (s *Server) Serve(ctx context.Context) error {
	s.log.Info("restate handlers listening", "addr", s.addr)
	if err := s.restate.Start(ctx, s.addr); err != nil {
		return fmt.Errorf("serve restate handlers: %w", err)
	}
	return nil
}

// Sweep is one module's periodic pass over whatever its clocks have missed. It reports
// nothing: every pass is best-effort and logs its own failures, because a sweep that can
// fail the process is a worse outage than the transition it was going to make.
type Sweep func(ctx context.Context, log *slog.Logger)

// Sweeper runs every registered sweep on one interval. One ticker for all of them rather
// than one per module: they are all "catch up on what a timer missed", and staggering them
// would only spread the same queries out.
type Sweeper struct {
	sweeps   []Sweep
	interval time.Duration
	log      *slog.Logger
}

func NewSweeper(interval time.Duration, log *slog.Logger, sweeps ...Sweep) *Sweeper {
	return &Sweeper{sweeps: sweeps, interval: interval, log: log}
}

// Run sweeps until the context is cancelled. The first pass waits one interval: at startup
// every clock was just read by whoever restarted the process, and a sweep racing the
// module's own initialisation buys nothing.
func (s *Sweeper) Run(ctx context.Context) {
	if len(s.sweeps) == 0 {
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, sweep := range s.sweeps {
				sweep(ctx, s.log)
			}
		}
	}
}
