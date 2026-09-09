// Package datafactory provides an in-memory implementation of the Azure Data
// Factory (Microsoft.DataFactory/factories) control plane. The ARM body is
// stored natively (its shape has no cross-cloud equivalent), keyed by
// (resourceGroup, name).
//
// A system-assigned identity's principalId/tenantId are synthesized once and are
// stable per resource; createTime is stamped once at first create and preserved
// across later updates. Both are the prime Terraform drift suspects, so neither
// is ever regenerated on read.
package datafactory

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/datafactory/driver"
)

// Compile-time check that Mock implements the Data Factory store.
var _ driver.Factories = (*Mock)(nil)

const (
	providerNamespace = "Microsoft.DataFactory"
	factoriesType     = "factories"

	// emulatorTenantID is the single Azure AD directory (tenant) that all
	// system-assigned identities in this emulator belong to. Real Azure has one
	// tenant per directory, so this is a fixed emulator-wide value rather than a
	// per-resource synthesized GUID.
	emulatorTenantID = "11111111-1111-1111-1111-111111111111"
)

// Mock is an in-memory Data Factory store.
type Mock struct {
	factories *memstore.Store[driver.Factory]
	opts      *config.Options
}

// New creates a new Data Factory mock with the given options.
func New(opts *config.Options) *Mock {
	return &Mock{
		factories: memstore.New[driver.Factory](),
		opts:      opts,
	}
}

// key keys the store by (resourceGroup, name). ARM names are case-insensitive, so
// the key is lower-cased; the stored body preserves the original casing.
func key(rg, name string) string {
	return strings.ToLower(rg) + "/" + strings.ToLower(name)
}

// CreateOrUpdateFactory stores a factory as a full replace and reports whether it
// did not previously exist. On update the computed identity fields (createTime,
// version, provisioningState, id) are preserved so they stay stable across reads.
//
//nolint:gocritic // hugeParam: cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateOrUpdateFactory(_ context.Context, cfg driver.FactoryConfig) (*driver.Factory, bool, error) {
	switch {
	case cfg.Name == "":
		return nil, false, cerrors.New(cerrors.InvalidArgument, "factory name is required")
	case cfg.ResourceGroup == "":
		return nil, false, cerrors.New(cerrors.InvalidArgument, "resource group is required")
	}

	k := key(cfg.ResourceGroup, cfg.Name)

	existing, existed := m.factories.Get(k)

	f := driver.Factory{
		Name:                cfg.Name,
		ResourceGroup:       cfg.ResourceGroup,
		Location:            cfg.Location,
		Tags:                copyStringMap(cfg.Tags),
		Identity:            resolveIdentity(cfg.Identity, cfg.ResourceGroup, cfg.Name),
		ProvisioningState:   driver.StateSucceeded,
		Version:             driver.Version,
		PublicNetworkAccess: defaultPublicNetworkAccess(cfg.PublicNetworkAccess),
		GlobalParameters:    copyGlobalParameters(cfg.GlobalParameters),
		OtherProps:          cloneAnyMap(cfg.OtherProps),
	}

	if existed {
		// Preserve the resource identity and the computed, immutable-on-update
		// fields so createTime (the prime drift suspect) and the ARM id stay
		// stable across the resource's lifetime.
		f.ID = existing.ID
		f.Subscription = existing.Subscription
		f.CreateTime = existing.CreateTime
	} else {
		sub := m.subOrDefault(cfg.Subscription)
		f.Subscription = sub
		f.ID = idgen.AzureID(sub, cfg.ResourceGroup, providerNamespace, factoriesType, cfg.Name)
		f.CreateTime = m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05.0000000Z07:00")
	}

	m.factories.Set(k, f)

	return cloneFactory(f), !existed, nil
}

// GetFactory returns a factory by resource group and name.
func (m *Mock) GetFactory(_ context.Context, resourceGroup, name string) (*driver.Factory, error) {
	f, ok := m.factories.Get(key(resourceGroup, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "factory %q not found", name)
	}

	return cloneFactory(f), nil
}

// UpdateFactory applies a PATCH (Factories_Update): tags and identity are
// REPLACED wholesale (resource-level UpdateTags = replace, not merge). A nil
// tags/identity leaves that field unchanged.
func (m *Mock) UpdateFactory(
	_ context.Context, resourceGroup, name string, tags map[string]string, identity *driver.ManagedIdentity,
) (*driver.Factory, error) {
	k := key(resourceGroup, name)

	f, ok := m.factories.Get(k)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "factory %q not found", name)
	}

	if tags != nil {
		f.Tags = copyStringMap(tags)
	}

	if identity != nil {
		f.Identity = resolveIdentity(identity, resourceGroup, name)
	}

	m.factories.Set(k, f)

	return cloneFactory(f), nil
}

// DeleteFactory deletes a factory.
func (m *Mock) DeleteFactory(_ context.Context, resourceGroup, name string) error {
	if !m.factories.Delete(key(resourceGroup, name)) {
		return cerrors.Newf(cerrors.NotFound, "factory %q not found", name)
	}

	return nil
}

// ListFactoriesByResourceGroup lists factories in a resource group, ordered by ID.
func (m *Mock) ListFactoriesByResourceGroup(_ context.Context, resourceGroup string) ([]driver.Factory, error) {
	all := m.factories.SortedValues()
	out := make([]driver.Factory, 0, len(all))

	for i := range all {
		if strings.EqualFold(all[i].ResourceGroup, resourceGroup) {
			out = append(out, *cloneFactory(all[i]))
		}
	}

	return out, nil
}

// ListFactories lists every factory in the subscription, ordered by ID.
func (m *Mock) ListFactories(_ context.Context) ([]driver.Factory, error) {
	all := m.factories.SortedValues()
	out := make([]driver.Factory, 0, len(all))

	for i := range all {
		out = append(out, *cloneFactory(all[i]))
	}

	return out, nil
}

func (m *Mock) subOrDefault(sub string) string {
	if sub != "" {
		return sub
	}

	return m.opts.AccountID
}

// defaultPublicNetworkAccess applies the Azure default (Enabled) when a request
// omits publicNetworkAccess.
func defaultPublicNetworkAccess(v string) string {
	if v == "" {
		return driver.PublicNetworkAccessEnabled
	}

	return v
}

// resolveIdentity normalizes an incoming managed identity: for a system-assigned
// identity it synthesizes deterministic principal/tenant GUIDs (as Azure does on
// assignment); a nil or "None" identity resolves to nil.
func resolveIdentity(in *driver.ManagedIdentity, resourceGroup, name string) *driver.ManagedIdentity {
	if in == nil || in.Type == "" || strings.EqualFold(in.Type, "None") {
		return nil
	}

	out := &driver.ManagedIdentity{
		Type:         in.Type,
		UserAssigned: append([]string(nil), in.UserAssigned...),
	}

	if strings.Contains(strings.ToLower(in.Type), "systemassigned") {
		// PrincipalID is per-resource: keying on (resource group, name) keeps it
		// stable across gets/restarts for the same factory while distinguishing
		// same-named factories in different resource groups.
		out.PrincipalID = synthGUID("principal/" + resourceGroup + "/" + name)
		// TenantID is the emulator's single directory (one tenant per directory).
		out.TenantID = emulatorTenantID
	}

	return out
}

// cloneFactory deep-copies a factory so stored and returned values never alias a
// caller's maps or slices.
//
//nolint:gocritic // hugeParam: clone by value is the intent.
func cloneFactory(f driver.Factory) *driver.Factory {
	out := f
	out.Tags = copyStringMap(f.Tags)
	out.GlobalParameters = copyGlobalParameters(f.GlobalParameters)
	out.OtherProps = cloneAnyMap(f.OtherProps)

	if f.Identity != nil {
		id := *f.Identity
		id.UserAssigned = append([]string(nil), f.Identity.UserAssigned...)
		out.Identity = &id
	}

	return &out
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyGlobalParameters(in map[string]driver.GlobalParameterSpec) map[string]driver.GlobalParameterSpec {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]driver.GlobalParameterSpec, len(in))
	for k, v := range in {
		out[k] = driver.GlobalParameterSpec{Type: v.Type, Value: cloneAnyValue(v.Value)}
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

// guidNodeMask isolates the low 48 bits used as a GUID's final node segment.
const guidNodeMask = 0xffffffffffff

// synthGUID derives a deterministic GUID-shaped string from s, used for
// synthesized identity principal/tenant IDs.
func synthGUID(s string) string {
	h1 := fnv.New64a()
	_, _ = h1.Write([]byte(s))
	a := h1.Sum64()

	h2 := fnv.New64a()
	_, _ = h2.Write([]byte(s + "#salt"))
	b := h2.Sum64()

	// Deliberate truncation + bit-shifting to assemble a GUID-shaped string from
	// hash bits; the value is synthetic, not a real security identifier, and the
	// shift widths are the fixed GUID field boundaries.
	//nolint:gosec,mnd // intentional narrowing + GUID field-width shifts
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(a>>32), uint16(a>>16), uint16(a), uint16(b>>48), b&guidNodeMask)
}
