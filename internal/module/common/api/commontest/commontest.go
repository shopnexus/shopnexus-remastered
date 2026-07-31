// Package commontest provides a stub commonapi.Service for tests.
//
// Embed Stub and override the one method a test is about. Anything left over answers 501,
// which is what an embedded nil interface does not do — it panics.
package commontest

import (
	"context"

	commonapi "shopnexus/internal/module/common/api"
	"shopnexus/internal/shared/errx"
)

// Stub implements commonapi.Service by refusing everything.
type Stub struct{}

var _ commonapi.Service = Stub{}

func (Stub) RegisterResource(context.Context, commonapi.RegisterResourceRequest) (commonapi.Resource, error) {
	return commonapi.Resource{}, errx.ErrNotImplemented
}

func (Stub) GetResources(context.Context, commonapi.GetResourcesRequest) ([]commonapi.Resource, error) {
	return nil, errx.ErrNotImplemented
}

func (Stub) ListOptions(context.Context, commonapi.ListOptionsRequest) ([]commonapi.Option, error) {
	return nil, errx.ErrNotImplemented
}
