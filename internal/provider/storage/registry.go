package storage

import "fmt"

// Registry is every store this deployment can read from, plus the one it writes to.
//
// The two are not the same question, and conflating them is what makes a switch of object
// store silently break every object that predates it. A `resource` row records the store
// that holds it, and that name outlives the deployment's current choice: moving to a bucket
// changes where the *next* upload goes, not where last year's photos are. So a read resolves
// against the store the row names, and only a write goes to the preferred one.
//
// Resolving everything through the preferred store instead does not fail — it is worse than
// that. A store signs whatever key it is handed, so an object it has never held still comes
// back as a perfectly-formed link, and the failure only surfaces when a browser fetches it.
type Registry struct {
	write  Client
	byName map[string]Client
}

// NewRegistry takes the store new uploads go to, then every other store still holding objects
// this deployment has to be able to serve. A store is only ever removed from this list once
// nothing references it, which `resource.provider` is how you check.
func NewRegistry(write Client, readable ...Client) (*Registry, error) {
	if write == nil {
		return nil, fmt.Errorf("storage registry: no write store")
	}
	r := &Registry{write: write, byName: map[string]Client{}}
	for _, c := range append([]Client{write}, readable...) {
		if _, taken := r.byName[c.Name()]; taken {
			// Two stores under one name means a row cannot say which of them holds it.
			return nil, fmt.Errorf("storage registry: duplicate provider %q", c.Name())
		}
		r.byName[c.Name()] = c
	}
	return r, nil
}

// Write is where a new upload goes, and the name stamped on its row.
func (r *Registry) Write() Client { return r.write }

// For is the store holding a given resource. An unknown name is an error rather than a
// fallback to the write store, because the fallback is exactly the bug: it answers with a
// link that verifies and then serves nothing.
func (r *Registry) For(provider string) (Client, error) {
	c, ok := r.byName[provider]
	if !ok {
		return nil, ErrProviderUnknown
	}
	return c, nil
}

// Lookup asks whether a particular store is wired at all. The gateway's own upload routes
// exist only when the `local` backend does, and that is the question they have to ask.
func (r *Registry) Lookup(provider string) (Client, bool) {
	c, ok := r.byName[provider]
	return c, ok
}
