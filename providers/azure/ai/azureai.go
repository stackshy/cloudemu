// Package azureai provides an in-memory mock implementation of Azure AI across
// both ARM providers — Microsoft.CognitiveServices (AI Foundry / AI Studio /
// the AI Services resource / Azure OpenAI) and Microsoft.MachineLearningServices
// (Azure Machine Learning) — plus the Azure OpenAI inference, AI Foundry
// Agents/Assistants, and AML scoring data planes.
package azureai

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/azureai/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// Compile-time check that Mock implements the CognitiveServices surface.
var _ driver.CognitiveServices = (*Mock)(nil)

const (
	csProvider   = "Microsoft.CognitiveServices"
	defaultCSSKU = "S0"
	kindDefault  = "Default"
)

// Mock is the in-memory Azure AI service.
type Mock struct {
	// CognitiveServices stores.
	accounts         *memstore.Store[*driver.Account]
	accountKeys      *memstore.Store[*driver.AccountKeys]
	deployments      *memstore.Store[*driver.Deployment]
	projects         *memstore.Store[*driver.Project]
	raiPolicies      *memstore.Store[*driver.RaiPolicy]
	commitmentPlans  *memstore.Store[*driver.CommitmentPlan]
	privateEndpoints *memstore.Store[*driver.PrivateEndpointConnection]

	// Data-plane stores (assistants API).
	assistants *memstore.Store[*driver.Assistant]
	threads    *memstore.Store[*driver.Thread]
	messages   *memstore.Store[*driver.ThreadMessage]
	runs       *memstore.Store[*driver.Run]

	// MachineLearningServices stores.
	mlWorkspaces *memstore.Store[*driver.MLWorkspace]
	computes     *memstore.Store[*driver.Compute]
	mlEndpoints  *memstore.Store[*driver.Endpoint]
	mlDeploys    *memstore.Store[*driver.EndpointDeployment]
	jobs         *memstore.Store[*driver.Job]
	assets       *memstore.Store[*driver.Asset]
	datastores   *memstore.Store[*driver.Datastore]
	connections  *memstore.Store[*driver.Connection]
	mlSchedules  *memstore.Store[*driver.MLSchedule]
	registries   *memstore.Store[*driver.Registry]

	seq atomic.Int64

	opts       *config.Options
	monitoring mondriver.Monitoring
}

// New creates a new Azure AI mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		accounts:         memstore.New[*driver.Account](),
		accountKeys:      memstore.New[*driver.AccountKeys](),
		deployments:      memstore.New[*driver.Deployment](),
		projects:         memstore.New[*driver.Project](),
		raiPolicies:      memstore.New[*driver.RaiPolicy](),
		commitmentPlans:  memstore.New[*driver.CommitmentPlan](),
		privateEndpoints: memstore.New[*driver.PrivateEndpointConnection](),
		assistants:       memstore.New[*driver.Assistant](),
		threads:          memstore.New[*driver.Thread](),
		messages:         memstore.New[*driver.ThreadMessage](),
		runs:             memstore.New[*driver.Run](),
		mlWorkspaces:     memstore.New[*driver.MLWorkspace](),
		computes:         memstore.New[*driver.Compute](),
		mlEndpoints:      memstore.New[*driver.Endpoint](),
		mlDeploys:        memstore.New[*driver.EndpointDeployment](),
		jobs:             memstore.New[*driver.Job](),
		assets:           memstore.New[*driver.Asset](),
		datastores:       memstore.New[*driver.Datastore](),
		connections:      memstore.New[*driver.Connection](),
		mlSchedules:      memstore.New[*driver.MLSchedule](),
		registries:       memstore.New[*driver.Registry](),
		opts:             opts,
	}
}

// SetMonitoring wires a monitoring backend for auto-metric emission. Optional;
// when unset, metric emission is a no-op.
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) {
	m.monitoring = mon
}

// --- shared helpers ---

func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(time.RFC3339)
}

// key joins path segments into a unique store key.
func key(parts ...string) string {
	return strings.Join(parts, "/")
}

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// emitMetric pushes an auto-metric to the wired monitoring backend (no-op when
// monitoring is unset). Failures never affect the control plane.
func (m *Mock) emitMetric(metricName string, value float64, dims map[string]string) {
	if m.monitoring == nil {
		return
	}

	_ = m.monitoring.PutMetricData(context.Background(), []mondriver.MetricDatum{{
		Namespace: csProvider, MetricName: metricName, Value: value,
		Unit: "Count", Dimensions: dims, Timestamp: m.opts.Clock.Now(),
	}})
}

func csOrDefaultSKU(name string) string {
	if name == "" {
		return defaultCSSKU
	}

	return name
}

// accountEndpoint derives the data-plane endpoint for an account from its kind.
func accountEndpoint(name, kind string) string {
	host := "cognitiveservices"
	if strings.EqualFold(kind, "OpenAI") {
		host = "openai"
	} else if strings.EqualFold(kind, "AIServices") {
		host = "services.ai"
	}

	return "https://" + name + "." + host + ".azure.com/"
}

// --- Accounts ---

func cloneAccount(a *driver.Account) *driver.Account {
	out := *a
	out.Tags = copyMap(a.Tags)

	return &out
}

//nolint:gocritic // cfg matches the driver signature; copied once on entry.
func (m *Mock) CreateAccount(_ context.Context, cfg driver.AccountConfig) (*driver.Account, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "account name is required")
	case cfg.ResourceGroup == "":
		return nil, errors.New(errors.InvalidArgument, "resource group is required")
	case cfg.Location == "":
		return nil, errors.New(errors.InvalidArgument, "location is required")
	}

	k := key(cfg.ResourceGroup, cfg.Name)

	kind := cfg.Kind
	if kind == "" {
		kind = "AIServices"
	}

	// ARM PUT is create-or-update: apply mutable fields to a copy of an
	// existing account, preserving identity fields.
	if existing, ok := m.accounts.Get(k); ok {
		updated := *existing
		updated.SKUName = csOrDefaultSKU(cfg.SKUName)
		updated.Tags = copyMap(cfg.Tags)

		if cfg.CustomDomain != "" {
			updated.CustomDomain = cfg.CustomDomain
		}

		m.accounts.Set(k, &updated)

		return cloneAccount(&updated), nil
	}

	acct := &driver.Account{
		ID:                idgen.AzureID(m.opts.AccountID, cfg.ResourceGroup, csProvider, "accounts", cfg.Name),
		Name:              cfg.Name,
		ResourceGroup:     cfg.ResourceGroup,
		Location:          cfg.Location,
		Kind:              kind,
		SKUName:           csOrDefaultSKU(cfg.SKUName),
		Endpoint:          accountEndpoint(cfg.Name, kind),
		CustomDomain:      cfg.CustomDomain,
		ProvisioningState: driver.StateSucceeded,
		Tags:              copyMap(cfg.Tags),
		CreatedAt:         m.now(),
	}
	m.accounts.Set(k, acct)
	m.emitMetric("account/count", 1, map[string]string{"kind": kind})

	return cloneAccount(acct), nil
}

func (m *Mock) GetAccount(_ context.Context, resourceGroup, name string) (*driver.Account, error) {
	a, ok := m.accounts.Get(key(resourceGroup, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "account %q not found", name)
	}

	return cloneAccount(a), nil
}

func (m *Mock) DeleteAccount(_ context.Context, resourceGroup, name string) error {
	if !m.accounts.Delete(key(resourceGroup, name)) {
		return errors.Newf(errors.NotFound, "account %q not found", name)
	}

	return nil
}

func (m *Mock) UpdateAccountTags(
	_ context.Context, resourceGroup, name string, tags map[string]string,
) (*driver.Account, error) {
	k := key(resourceGroup, name)

	a, ok := m.accounts.Get(k)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "account %q not found", name)
	}

	updated := *a
	updated.Tags = copyMap(tags)
	m.accounts.Set(k, &updated)

	return cloneAccount(&updated), nil
}

func (m *Mock) ListAccountsByResourceGroup(_ context.Context, resourceGroup string) ([]driver.Account, error) {
	out := make([]driver.Account, 0)

	for _, a := range m.accounts.All() {
		if a.ResourceGroup == resourceGroup {
			out = append(out, *cloneAccount(a))
		}
	}

	return out, nil
}

func (m *Mock) ListAccounts(_ context.Context) ([]driver.Account, error) {
	all := m.accounts.All()
	out := make([]driver.Account, 0, len(all))

	for _, a := range all {
		out = append(out, *cloneAccount(a))
	}

	return out, nil
}

// currentAccountKeys returns the persisted keys, or the deterministic defaults
// when the account has never had a key regenerated.
func (m *Mock) currentAccountKeys(resourceGroup, name string) *driver.AccountKeys {
	if k, ok := m.accountKeys.Get(key(resourceGroup, name)); ok {
		out := *k

		return &out
	}

	return &driver.AccountKeys{
		Key1: deterministicKey(resourceGroup, name, "1"),
		Key2: deterministicKey(resourceGroup, name, "2"),
	}
}

func (m *Mock) ListAccountKeys(_ context.Context, resourceGroup, name string) (*driver.AccountKeys, error) {
	if !m.accounts.Has(key(resourceGroup, name)) {
		return nil, errors.Newf(errors.NotFound, "account %q not found", name)
	}

	return m.currentAccountKeys(resourceGroup, name), nil
}

func (m *Mock) RegenerateAccountKey(_ context.Context, resourceGroup, name, keyName string) (*driver.AccountKeys, error) {
	if !m.accounts.Has(key(resourceGroup, name)) {
		return nil, errors.Newf(errors.NotFound, "account %q not found", name)
	}

	keys := m.currentAccountKeys(resourceGroup, name)

	// Salt the named key with a monotonic counter so the rotation is distinct
	// and observable via a subsequent ListAccountKeys; the other key is stable.
	salt := "regen-" + strconv.FormatInt(m.seq.Add(1), 16)
	if strings.EqualFold(keyName, "Key2") {
		keys.Key2 = deterministicKey(resourceGroup, name, salt)
	} else {
		keys.Key1 = deterministicKey(resourceGroup, name, salt)
	}

	m.accountKeys.Set(key(resourceGroup, name), keys)

	out := *keys

	return &out, nil
}

func (m *Mock) ListAccountUsages(_ context.Context, resourceGroup, name string) ([]driver.Usage, error) {
	if !m.accounts.Has(key(resourceGroup, name)) {
		return nil, errors.Newf(errors.NotFound, "account %q not found", name)
	}

	deps, _ := m.ListDeployments(context.Background(), resourceGroup, name)

	return []driver.Usage{
		{Name: "TokensPerMinute", CurrentValue: float64(len(deps)) * 1000, Limit: 240000, Unit: "Count"},
		{Name: "DeploymentCount", CurrentValue: float64(len(deps)), Limit: 32, Unit: "Count"},
	}, nil
}

func (m *Mock) ListAccountModels(_ context.Context, resourceGroup, name string) ([]driver.AccountModel, error) {
	a, ok := m.accounts.Get(key(resourceGroup, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "account %q not found", name)
	}

	// A representative catalog scoped to the account's kind.
	return []driver.AccountModel{
		{Name: "gpt-4o", Version: "2024-08-06", Format: "OpenAI", Kind: a.Kind},
		{Name: "gpt-4o-mini", Version: "2024-07-18", Format: "OpenAI", Kind: a.Kind},
		{Name: "text-embedding-3-large", Version: "1", Format: "OpenAI", Kind: a.Kind},
	}, nil
}

func (m *Mock) ListAccountSkus(_ context.Context, resourceGroup, name string) ([]driver.AccountSKU, error) {
	if !m.accounts.Has(key(resourceGroup, name)) {
		return nil, errors.Newf(errors.NotFound, "account %q not found", name)
	}

	return []driver.AccountSKU{
		{Name: "F0", Tier: "Free"},
		{Name: "S0", Tier: "Standard"},
	}, nil
}

// deterministicKey derives a stable 32-hex-char access key from its inputs.
func deterministicKey(parts ...string) string {
	s := strings.Join(parts, ":")
	h1 := fnv.New64a()
	_, _ = h1.Write([]byte(s))
	h2 := fnv.New64a()
	_, _ = h2.Write([]byte(s + "#salt"))

	return fmt.Sprintf("%016x%016x", h1.Sum64(), h2.Sum64())
}
