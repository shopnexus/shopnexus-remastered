package common

import (
	"fmt"
	"maps"
	"slices"
)

// Registry resolves the `provider` an option row names to the client that serves it.
//
// This is what makes a provider a *row* rather than a deployment: every implementation the binary
// has is registered at startup, and which one a given rail or carrier uses is the row's business.
// So moving a carrier from GHN to GHTK is an admin editing one field — the slug, and every order
// that already names it, do not move — where a `TRANSPORT_PROVIDER` env var would have moved every
// carrier at once and needed a restart.
//
// A mock is an ordinary member. It is not a mode the code branches on: it serves whatever its rows
// ask for, and rows only exist where an operator asked for them (see MOCK_ENABLED).
type Registry[T any] struct{ clients map[string]T }

// NewRegistry takes the implementations this binary has, keyed by the name a row uses.
func NewRegistry[T any](clients map[string]T) *Registry[T] {
	return &Registry[T]{clients: maps.Clone(clients)}
}

// Client answers the implementation for a provider name. A row naming one nobody registered is the
// single invalid case — a deployment lost the vendor that row was written for — and it is an error
// rather than a fallback: charging through whichever rail happened to be first is worse than
// refusing.
func (r *Registry[T]) Client(provider string) (T, error) {
	client, ok := r.clients[provider]
	if !ok {
		var zero T
		return zero, fmt.Errorf("no provider registered as %q (have %v)", provider, r.Providers())
	}
	return client, nil
}

// Providers is every registered name, sorted — what an admin may set a row's provider to.
func (r *Registry[T]) Providers() []string {
	return slices.Sorted(maps.Keys(r.clients))
}

// Each calls fn for every registered client. Used where something has to happen once per
// implementation rather than once per row: mounting each provider's webhook route.
func (r *Registry[T]) Each(fn func(provider string, client T) error) error {
	for _, provider := range r.Providers() {
		if err := fn(provider, r.clients[provider]); err != nil {
			return fmt.Errorf("provider %s: %w", provider, err)
		}
	}
	return nil
}
