package databricks

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

const accessConnectorsType = "accessConnectors"

// emulatorTenantID is the single Azure AD directory (tenant) that all
// system-assigned identities in this emulator belong to. Real Azure has one
// tenant per directory, so this is a fixed emulator-wide value rather than a
// per-resource synthesized GUID.
const emulatorTenantID = "11111111-1111-1111-1111-111111111111"

// CreateOrUpdateAccessConnector creates or updates an access connector,
// completing provisioning synchronously (store-and-echo).
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateOrUpdateAccessConnector(
	_ context.Context, cfg driver.AccessConnectorConfig,
) (*driver.AccessConnector, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "access connector name is required")
	case cfg.ResourceGroup == "":
		return nil, errors.New(errors.InvalidArgument, "resource group is required")
	case cfg.Location == "":
		return nil, errors.New(errors.InvalidArgument, "location is required")
	}

	k := key(cfg.ResourceGroup, cfg.Name)

	if existing, ok := m.accessConnectors.Get(k); ok {
		// ARM PUT is create-or-update: apply the mutable fields to a copy and
		// swap it in, preserving identity fields (ID, created time). Location is
		// immutable in real Azure, so it is left untouched.
		updated := *existing
		updated.Tags = copyMap(cfg.Tags)
		updated.Identity = resolveIdentity(cfg.Identity, cfg.ResourceGroup, cfg.Name)
		m.accessConnectors.Set(k, &updated)

		return cloneAccessConnector(&updated), nil
	}

	sub := m.subOrDefault(cfg.Subscription)
	ac := &driver.AccessConnector{
		ID:                idgen.AzureID(sub, cfg.ResourceGroup, providerNamespace, accessConnectorsType, cfg.Name),
		Name:              cfg.Name,
		Subscription:      sub,
		ResourceGroup:     cfg.ResourceGroup,
		Location:          cfg.Location,
		Tags:              copyMap(cfg.Tags),
		Identity:          resolveIdentity(cfg.Identity, cfg.ResourceGroup, cfg.Name),
		ProvisioningState: driver.StateSucceeded,
		CreatedAt:         m.opts.Clock.Now().UTC().Format(time.RFC3339),
	}

	m.accessConnectors.Set(k, ac)

	return cloneAccessConnector(ac), nil
}

// GetAccessConnector returns an access connector by resource group and name.
func (m *Mock) GetAccessConnector(_ context.Context, resourceGroup, name string) (*driver.AccessConnector, error) {
	ac, ok := m.accessConnectors.Get(key(resourceGroup, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "access connector %q not found", name)
	}

	return cloneAccessConnector(ac), nil
}

// UpdateAccessConnector applies a PATCH (tags and/or identity) to an access
// connector.
func (m *Mock) UpdateAccessConnector(
	_ context.Context, resourceGroup, name string, tags map[string]string, identity *driver.ManagedIdentity,
) (*driver.AccessConnector, error) {
	k := key(resourceGroup, name)

	ac, ok := m.accessConnectors.Get(k)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "access connector %q not found", name)
	}

	// Mutate a copy and swap it in so concurrent readers never observe a torn
	// update. A nil tags/identity leaves that field unchanged (PATCH semantics).
	updated := *ac
	if tags != nil {
		updated.Tags = copyMap(tags)
	}

	if identity != nil {
		updated.Identity = resolveIdentity(identity, resourceGroup, name)
	}

	m.accessConnectors.Set(k, &updated)

	return cloneAccessConnector(&updated), nil
}

// DeleteAccessConnector deletes an access connector.
func (m *Mock) DeleteAccessConnector(_ context.Context, resourceGroup, name string) error {
	if !m.accessConnectors.Delete(key(resourceGroup, name)) {
		return errors.Newf(errors.NotFound, "access connector %q not found", name)
	}

	return nil
}

// ListAccessConnectorsByResourceGroup lists access connectors in a resource group.
func (m *Mock) ListAccessConnectorsByResourceGroup(
	_ context.Context, resourceGroup string,
) ([]driver.AccessConnector, error) {
	out := make([]driver.AccessConnector, 0)

	for _, ac := range m.accessConnectors.All() {
		if ac.ResourceGroup == resourceGroup {
			out = append(out, *cloneAccessConnector(ac))
		}
	}

	sortAccessConnectors(out)

	return out, nil
}

// ListAccessConnectors lists all access connectors in the subscription.
func (m *Mock) ListAccessConnectors(_ context.Context) ([]driver.AccessConnector, error) {
	all := m.accessConnectors.All()
	out := make([]driver.AccessConnector, 0, len(all))

	for _, ac := range all {
		out = append(out, *cloneAccessConnector(ac))
	}

	sortAccessConnectors(out)

	return out, nil
}

func sortAccessConnectors(in []driver.AccessConnector) {
	sort.SliceStable(in, func(i, j int) bool { return in[i].ID < in[j].ID })
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
		// PrincipalID is per-resource: keying on (resource group, name) means two
		// connectors with the same name in different RGs get distinct principals,
		// while the value stays stable across gets/restarts for the same resource.
		out.PrincipalID = synthGUID("principal/" + resourceGroup + "/" + name)
		// TenantID is the emulator's single directory (one tenant per directory).
		out.TenantID = emulatorTenantID
	}

	return out
}

func cloneAccessConnector(ac *driver.AccessConnector) *driver.AccessConnector {
	clone := *ac
	clone.Tags = copyMap(ac.Tags)

	if ac.Identity != nil {
		id := *ac.Identity
		id.UserAssigned = append([]string(nil), ac.Identity.UserAssigned...)
		clone.Identity = &id
	}

	return &clone
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
