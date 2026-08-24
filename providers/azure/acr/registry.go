package acr

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

const (
	defaultRegistrySKU  = "Standard"
	classicSKU          = "Classic"
	registryProvisioned = "Succeeded"
	// emulatorTenantID is the single Azure AD directory all emulated resources
	// belong to.
	emulatorTenantID = "11111111-1111-1111-1111-111111111111"
	identityNone     = "None"
)

// Compile-time checks that Mock satisfies the Azure-specific optional surfaces.
var (
	_ driver.AzureRegistryManager  = (*Mock)(nil)
	_ driver.AzureRepositoryWriter = (*Mock)(nil)
)

// registryData is the stored ARM registry plus its admin credential pair.
type registryData struct {
	reg       driver.AzureRegistry
	password  string
	password2 string
}

func registryStoreKey(rg, name string) string {
	return rg + "/" + name
}

func subResourceKey(rg, registry, name string) string {
	return rg + "/" + registry + "/" + name
}

// CreateOrUpdateRegistry creates or replaces an ACR registry (ARM PUT).
func (m *Mock) CreateOrUpdateRegistry(
	_ context.Context, rg, name string, cfg driver.AzureRegistryConfig,
) (*driver.AzureRegistry, error) {
	if name == "" {
		return nil, errors.New(errors.InvalidArgument, "registry name is required")
	}

	if rg == "" {
		return nil, errors.New(errors.InvalidArgument, "resource group is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := registryStoreKey(rg, name)
	now := m.opts.Clock.Now().UTC()

	rd, existing := m.registries.Get(key)
	if !existing {
		rd = &registryData{
			password:  synthCredential("password/" + rg + "/" + name),
			password2: synthCredential("password2/" + rg + "/" + name),
		}
		rd.reg.CreationDate = now
	}

	sku := defaultIfEmpty(cfg.SKUName, defaultRegistrySKU)

	rd.reg.Name = name
	rd.reg.ResourceGroup = rg
	rd.reg.Location = cfg.Location
	rd.reg.SKUName = sku
	rd.reg.SKUTier = skuTier(sku)
	rd.reg.LoginServer = strings.ToLower(name) + ".azurecr.io"
	rd.reg.AdminUserEnabled = cfg.AdminUserEnabled
	rd.reg.ProvisioningState = registryProvisioned
	rd.reg.Tags = copyTags(cfg.Tags)
	applyIdentity(&rd.reg, cfg.IdentityType, rg, name)

	m.registries.Set(key, rd)

	out := rd.reg

	return &out, nil
}

// applyIdentity echoes the submitted managed-identity block, generating a
// deterministic principal/tenant pair for a system-assigned identity.
func applyIdentity(reg *driver.AzureRegistry, identityType, rg, name string) {
	reg.IdentityType = identityType
	reg.PrincipalID = ""
	reg.TenantID = ""

	if identityType == "" || identityType == identityNone {
		return
	}

	if strings.Contains(identityType, "SystemAssigned") {
		reg.PrincipalID = idgen.SyntheticGUID("principal/registry/" + rg + "/" + name)
		reg.TenantID = emulatorTenantID
	}
}

func defaultIfEmpty(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

func skuTier(sku string) string {
	if sku == classicSKU {
		return classicSKU
	}

	return sku
}

// GetRegistry returns a registry by name within a resource group.
func (m *Mock) GetRegistry(_ context.Context, rg, name string) (*driver.AzureRegistry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.registries.Get(registryStoreKey(rg, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "registry %q not found in resource group %q", name, rg)
	}

	out := rd.reg

	return &out, nil
}

// DeleteRegistry removes a registry and its webhooks/replications.
func (m *Mock) DeleteRegistry(_ context.Context, rg, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.registries.Delete(registryStoreKey(rg, name)) {
		return errors.Newf(errors.NotFound, "registry %q not found in resource group %q", name, rg)
	}

	prefix := rg + "/" + name + "/"
	for _, k := range m.webhooks.Keys() {
		if strings.HasPrefix(k, prefix) {
			m.webhooks.Delete(k)
		}
	}

	for _, k := range m.replications.Keys() {
		if strings.HasPrefix(k, prefix) {
			m.replications.Delete(k)
		}
	}

	return nil
}

// ListRegistries returns registries in a resource group, or across the whole
// subscription when rg is empty.
func (m *Mock) ListRegistries(_ context.Context, rg string) ([]driver.AzureRegistry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	all := m.registries.All()
	out := make([]driver.AzureRegistry, 0, len(all))

	for _, rd := range all {
		if rg != "" && rd.reg.ResourceGroup != rg {
			continue
		}

		out = append(out, rd.reg)
	}

	return out, nil
}

// ListRegistryCredentials returns the admin username / password pair.
func (m *Mock) ListRegistryCredentials(_ context.Context, rg, name string) (*driver.AzureRegistryCredentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.registries.Get(registryStoreKey(rg, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "registry %q not found in resource group %q", name, rg)
	}

	if !rd.reg.AdminUserEnabled {
		return nil, errors.Newf(
			errors.FailedPrecondition, "admin user is not enabled for registry %q", name,
		)
	}

	return &driver.AzureRegistryCredentials{
		Username:  name,
		Password:  rd.password,
		Password2: rd.password2,
	}, nil
}

// RegenerateRegistryCredential rotates the named password (password / password2).
func (m *Mock) RegenerateRegistryCredential(
	_ context.Context, rg, name, passwordName string,
) (*driver.AzureRegistryCredentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rd, ok := m.registries.Get(registryStoreKey(rg, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "registry %q not found in resource group %q", name, rg)
	}

	seed := fmt.Sprintf("%s/%s/%d", rg, name, m.opts.Clock.Now().UnixNano())

	switch passwordName {
	case "password":
		rd.password = synthCredential("password/regen/" + seed)
	case "password2":
		rd.password2 = synthCredential("password2/regen/" + seed)
	default:
		return nil, errors.Newf(errors.InvalidArgument, "invalid password name %q", passwordName)
	}

	m.registries.Set(registryStoreKey(rg, name), rd)

	return &driver.AzureRegistryCredentials{
		Username:  name,
		Password:  rd.password,
		Password2: rd.password2,
	}, nil
}

// ListRegistryUsages returns the registry's quota usages.
func (m *Mock) ListRegistryUsages(_ context.Context, rg, name string) ([]driver.AzureRegistryUsage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.registries.Has(registryStoreKey(rg, name)) {
		return nil, errors.Newf(errors.NotFound, "registry %q not found in resource group %q", name, rg)
	}

	const storageLimitBytes = 536870912000 // 500 GiB, the Standard-SKU included storage.

	return []driver.AzureRegistryUsage{
		{Name: "Size", Limit: storageLimitBytes, CurrentValue: 0, Unit: "Bytes"},
		{Name: "Webhooks", Limit: webhookQuota, CurrentValue: int64(m.countSubResources(m.webhooks.Keys(), rg, name)), Unit: "Count"},
	}, nil
}

const webhookQuota = 100

func (*Mock) countSubResources(keys []string, rg, registry string) int {
	prefix := rg + "/" + registry + "/"
	n := 0

	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			n++
		}
	}

	return n
}

// synthCredential derives a deterministic base64-ish password from a seed.
func synthCredential(seed string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))

	return fmt.Sprintf("%016x%016x", h.Sum64(), fnvSalted(seed))
}

func fnvSalted(seed string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed + "#cred"))

	return h.Sum64()
}

// CreateOrUpdateWebhook creates or replaces a registry webhook.
//
//nolint:gocritic // cfg mirrors the driver interface's value-type config; pointer would invite caller mutation.
func (m *Mock) CreateOrUpdateWebhook(
	_ context.Context, rg, registry, name string, cfg driver.AzureWebhookConfig,
) (*driver.AzureWebhook, error) {
	if name == "" {
		return nil, errors.New(errors.InvalidArgument, "webhook name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.registries.Has(registryStoreKey(rg, registry)) {
		return nil, errors.Newf(errors.NotFound, "registry %q not found in resource group %q", registry, rg)
	}

	wh := &driver.AzureWebhook{
		Name:              name,
		RegistryName:      registry,
		ResourceGroup:     rg,
		Location:          cfg.Location,
		Tags:              copyTags(cfg.Tags),
		ServiceURI:        cfg.ServiceURI,
		Actions:           append([]string(nil), cfg.Actions...),
		Scope:             cfg.Scope,
		Status:            defaultIfEmpty(cfg.Status, "enabled"),
		CustomHeaders:     copyTags(cfg.CustomHeaders),
		ProvisioningState: registryProvisioned,
	}

	m.webhooks.Set(subResourceKey(rg, registry, name), wh)

	out := *wh

	return &out, nil
}

// GetWebhook returns a webhook by name.
func (m *Mock) GetWebhook(_ context.Context, rg, registry, name string) (*driver.AzureWebhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	wh, ok := m.webhooks.Get(subResourceKey(rg, registry, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "webhook %q not found on registry %q", name, registry)
	}

	out := *wh

	return &out, nil
}

// DeleteWebhook removes a webhook.
func (m *Mock) DeleteWebhook(_ context.Context, rg, registry, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.webhooks.Delete(subResourceKey(rg, registry, name)) {
		return errors.Newf(errors.NotFound, "webhook %q not found on registry %q", name, registry)
	}

	return nil
}

// ListWebhooks returns all webhooks on a registry.
//
//nolint:dupl // webhook and replication sub-resource lists are intentionally typed; sharing via generics adds noise.
func (m *Mock) ListWebhooks(_ context.Context, rg, registry string) ([]driver.AzureWebhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.registries.Has(registryStoreKey(rg, registry)) {
		return nil, errors.Newf(errors.NotFound, "registry %q not found in resource group %q", registry, rg)
	}

	prefix := rg + "/" + registry + "/"
	all := m.webhooks.All()
	out := make([]driver.AzureWebhook, 0)

	for k, wh := range all {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *wh)
		}
	}

	return out, nil
}

// CreateOrUpdateReplication creates or replaces a geo-replication.
func (m *Mock) CreateOrUpdateReplication(
	_ context.Context, rg, registry, name string, cfg driver.AzureReplicationConfig,
) (*driver.AzureReplication, error) {
	if name == "" {
		return nil, errors.New(errors.InvalidArgument, "replication name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.registries.Has(registryStoreKey(rg, registry)) {
		return nil, errors.Newf(errors.NotFound, "registry %q not found in resource group %q", registry, rg)
	}

	rep := &driver.AzureReplication{
		Name:                  name,
		RegistryName:          registry,
		ResourceGroup:         rg,
		Location:              cfg.Location,
		Tags:                  copyTags(cfg.Tags),
		RegionEndpointEnabled: cfg.RegionEndpointEnabled,
		ProvisioningState:     registryProvisioned,
		Status:                "Ready",
	}

	m.replications.Set(subResourceKey(rg, registry, name), rep)

	out := *rep

	return &out, nil
}

// GetReplication returns a replication by name.
func (m *Mock) GetReplication(_ context.Context, rg, registry, name string) (*driver.AzureReplication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rep, ok := m.replications.Get(subResourceKey(rg, registry, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "replication %q not found on registry %q", name, registry)
	}

	out := *rep

	return &out, nil
}

// DeleteReplication removes a replication.
func (m *Mock) DeleteReplication(_ context.Context, rg, registry, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.replications.Delete(subResourceKey(rg, registry, name)) {
		return errors.Newf(errors.NotFound, "replication %q not found on registry %q", name, registry)
	}

	return nil
}

// ListReplications returns all replications on a registry.
//
//nolint:dupl // webhook and replication sub-resource lists are intentionally typed; sharing via generics adds noise.
func (m *Mock) ListReplications(_ context.Context, rg, registry string) ([]driver.AzureReplication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.registries.Has(registryStoreKey(rg, registry)) {
		return nil, errors.Newf(errors.NotFound, "registry %q not found in resource group %q", registry, rg)
	}

	prefix := rg + "/" + registry + "/"
	all := m.replications.All()
	out := make([]driver.AzureReplication, 0)

	for k, rep := range all {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *rep)
		}
	}

	return out, nil
}
