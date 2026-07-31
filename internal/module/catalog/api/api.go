// Package catalogapi is the published contract of the catalog service.
//
// Other modules and the gateway depend on this, never on the service package. Methods
// are added one slice at a time, matching api/openapi/*.yaml.
package catalogapi

type Service interface {
}
