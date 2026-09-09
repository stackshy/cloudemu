// Package applicationgateway provides an in-memory implementation of the Azure
// Application Gateway (Microsoft.Network/applicationGateways) store. The ARM
// gateway is stored natively (its deeply-nested, cross-referenced shape has no
// cross-cloud equivalent), keyed by (resourceGroup, name).
package applicationgateway

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/applicationgateway/driver"
)

// Compile-time check that Mock implements the Azure Application Gateway store.
var _ driver.AzureApplicationGateways = (*Mock)(nil)

// Mock is an in-memory Azure Application Gateway store.
type Mock struct {
	gws  *memstore.Store[driver.AzureAppGateway]
	opts *config.Options
}

// New creates a new Application Gateway mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		gws:  memstore.New[driver.AzureAppGateway](),
		opts: opts,
	}
}

// gwKey keys the store by (resourceGroup, name). ARM names are case-insensitive,
// so the key is lower-cased to resolve differently-cased GET/DELETE requests to
// the same gateway and keep a re-PUT an in-place update rather than a duplicate.
// Only the map key is normalized; the stored body preserves the original casing.
func gwKey(rg, name string) string {
	return strings.ToLower(rg) + "/" + strings.ToLower(name)
}

// CreateOrUpdateAzureApplicationGateway stores gw as a full replace and reports
// whether it did not previously exist.
//
//nolint:gocritic // hugeParam: value carries maps/slices copied defensively below.
func (m *Mock) CreateOrUpdateAzureApplicationGateway(
	_ context.Context, rg, name string, gw driver.AzureAppGateway,
) (*driver.AzureAppGateway, bool, error) {
	if name == "" {
		return nil, false, cerrors.New(cerrors.InvalidArgument, "application gateway name is required")
	}

	_, existed := m.gws.Get(gwKey(rg, name))

	stored := cloneGateway(gw)
	stored.Name = name
	stored.ResourceGroup = rg

	m.gws.Set(gwKey(rg, name), stored)

	out := cloneGateway(stored)

	return &out, !existed, nil
}

// GetAzureApplicationGateway returns the stored gateway.
func (m *Mock) GetAzureApplicationGateway(_ context.Context, rg, name string) (*driver.AzureAppGateway, error) {
	gw, ok := m.gws.Get(gwKey(rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "application gateway %q not found", name)
	}

	out := cloneGateway(gw)

	return &out, nil
}

// DeleteAzureApplicationGateway removes the stored gateway.
func (m *Mock) DeleteAzureApplicationGateway(_ context.Context, rg, name string) error {
	if !m.gws.Delete(gwKey(rg, name)) {
		return cerrors.Newf(cerrors.NotFound, "application gateway %q not found", name)
	}

	return nil
}

// ListAzureApplicationGateways returns the gateways in rg, or all when rg is
// empty (subscription-wide list).
func (m *Mock) ListAzureApplicationGateways(_ context.Context, rg string) ([]driver.AzureAppGateway, error) {
	all := m.gws.SortedValues()

	out := make([]driver.AzureAppGateway, 0, len(all))

	for i := range all {
		if rg != "" && !strings.EqualFold(all[i].ResourceGroup, rg) {
			continue
		}

		out = append(out, cloneGateway(all[i]))
	}

	return out, nil
}

// cloneGateway deep-copies a gateway so stored and returned values never alias a
// caller's maps or slices.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func cloneGateway(gw driver.AzureAppGateway) driver.AzureAppGateway {
	out := gw
	out.Zones = append([]string(nil), gw.Zones...)
	out.Identity = cloneAnyMap(gw.Identity)
	out.OtherProps = cloneAnyMap(gw.OtherProps)
	out.Tags = cloneStringMap(gw.Tags)
	out.Collections = cloneCollections(gw.Collections)

	return out
}

// cloneCollections deep-copies the modeled nested collections, preserving item
// order within each collection.
func cloneCollections(in map[string][]driver.AzureAppGatewayChild) map[string][]driver.AzureAppGatewayChild {
	if in == nil {
		return nil
	}

	out := make(map[string][]driver.AzureAppGatewayChild, len(in))

	for key, children := range in {
		copies := make([]driver.AzureAppGatewayChild, len(children))
		for i := range children {
			copies[i] = driver.AzureAppGatewayChild{
				Name:       children[i].Name,
				Properties: cloneAnyMap(children[i].Properties),
			}
		}

		out[key] = copies
	}

	return out
}

// cloneAnyMap deep-copies a generic JSON map so no request-owned map or slice is
// aliased into the store.
func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}

	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAnyValue(v)
	}

	return out
}

// cloneAnyValue recursively deep-copies a decoded JSON value.
func cloneAnyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneAnyMap(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = cloneAnyValue(t[i])
		}

		return out
	default:
		return v
	}
}

// cloneStringMap copies a string map, or returns nil for an empty one.
func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
