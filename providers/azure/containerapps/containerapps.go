// Package containerapps provides an in-memory mock of Azure Container Apps
// (Microsoft.App): managed environments and the container apps that run inside
// them.
//
// A managed environment is the isolation boundary a set of container apps share
// (a Log Analytics workspace, a default DNS domain, a static ingress IP). A
// container app runs one revision's worth of containers behind an optional
// ingress. Both are Azure-only ARM resources with no cross-cloud portable
// driver, so their state lives here on a provider mock, exactly like Azure
// user-assigned managed identities. The values a discoverer prices on — a
// container's cpu/memory and the app's scale.minReplicas — are preserved
// verbatim from create so they survive a create -> discover round trip.
package containerapps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

const (
	// providerNamespace is the ARM provider namespace both resource types share.
	providerNamespace = "Microsoft.App"
	// typeEnvironments / typeContainerApps are the two ARM resource-type segments.
	typeEnvironments  = "managedEnvironments"
	typeContainerApps = "containerApps"

	// domainSuffix is the DNS suffix Azure gives every managed environment.
	domainSuffix = ".azurecontainerapps.io"
	// shortHashLen is how many hex chars of a resource's id hash seed the
	// synthesized domain label, revision suffix and static IP, so those minted
	// values are stable per resource yet distinct between resources.
	shortHashLen = 8
)

// AppLogsConfiguration mirrors the managed environment's log-export setting.
type AppLogsConfiguration struct {
	Destination string `json:"destination,omitempty"`
}

// Environment is a stored managed environment. Subscription/ResourceGroup/Name
// preserve the caller's casing; DefaultDomain and StaticIP are minted once at
// create and preserved across updates so a captured fqdn stays valid.
type Environment struct {
	Subscription  string                `json:"subscription"`
	ResourceGroup string                `json:"resourceGroup"`
	Name          string                `json:"name"`
	Location      string                `json:"location"`
	Tags          map[string]string     `json:"tags,omitempty"`
	AppLogs       *AppLogsConfiguration `json:"appLogs,omitempty"`
	DefaultDomain string                `json:"defaultDomain"`
	StaticIP      string                `json:"staticIp"`
}

// ARMID returns the fully-qualified ARM id for the environment.
func (e *Environment) ARMID() string {
	return armID(e.Subscription, e.ResourceGroup, typeEnvironments, e.Name)
}

// EnvVar is a container environment variable.
type EnvVar struct {
	Name      string `json:"name"`
	Value     string `json:"value,omitempty"`
	SecretRef string `json:"secretRef,omitempty"`
}

// ContainerResources carries the vCPU/memory a discoverer prices a container on.
type ContainerResources struct {
	CPU    float64 `json:"cpu,omitempty"`
	Memory string  `json:"memory,omitempty"`
}

// Container is one container in a container app's template.
type Container struct {
	Name      string              `json:"name,omitempty"`
	Image     string              `json:"image,omitempty"`
	Env       []EnvVar            `json:"env,omitempty"`
	Resources *ContainerResources `json:"resources,omitempty"`
}

// Scale carries the replica bounds; MinReplicas is a primary cost input.
type Scale struct {
	MinReplicas *int32 `json:"minReplicas,omitempty"`
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`
}

// Template is the versioned application definition of a container app.
type Template struct {
	Containers     []Container `json:"containers,omitempty"`
	Scale          *Scale      `json:"scale,omitempty"`
	RevisionSuffix string      `json:"revisionSuffix,omitempty"`
}

// Ingress is the app's inbound configuration. Fqdn is synthesized on create.
// Traffic splits inbound requests across the app's revisions.
type Ingress struct {
	External      bool            `json:"external,omitempty"`
	TargetPort    int32           `json:"targetPort,omitempty"`
	Transport     string          `json:"transport,omitempty"`
	AllowInsecure bool            `json:"allowInsecure,omitempty"`
	Traffic       []TrafficWeight `json:"traffic,omitempty"`
}

// ContainerApp is a stored container app. Fqdn and LatestRevisionName are minted
// once at create and preserved across updates. Revisions is the app's revision
// history — a new entry is materialized every time the template changes.
type ContainerApp struct {
	Subscription       string            `json:"subscription"`
	ResourceGroup      string            `json:"resourceGroup"`
	Name               string            `json:"name"`
	Location           string            `json:"location"`
	Tags               map[string]string `json:"tags,omitempty"`
	EnvironmentID      string            `json:"environmentId,omitempty"`
	ActiveRevMode      string            `json:"activeRevisionsMode,omitempty"`
	Ingress            *Ingress          `json:"ingress,omitempty"`
	SecretNames        []string          `json:"secretNames,omitempty"`
	Template           Template          `json:"template"`
	Fqdn               string            `json:"fqdn,omitempty"`
	LatestRevisionName string            `json:"latestRevisionName"`
	Revisions          []Revision        `json:"revisions,omitempty"`
}

// ARMID returns the fully-qualified ARM id for the container app.
func (a *ContainerApp) ARMID() string {
	return armID(a.Subscription, a.ResourceGroup, typeContainerApps, a.Name)
}

// EnvironmentInput carries the mutable fields of an environment create/update.
type EnvironmentInput struct {
	Location string
	Tags     map[string]string
	AppLogs  *AppLogsConfiguration
}

// AppInput carries the mutable fields of a container-app create/update.
type AppInput struct {
	Location      string
	Tags          map[string]string
	EnvironmentID string
	ActiveRevMode string
	Ingress       *Ingress
	SecretNames   []string
	Template      Template
}

// Mock is the in-memory backend for Container Apps.
type Mock struct {
	mu    sync.RWMutex
	clock config.Clock
	envs  *memstore.Store[Environment]
	apps  *memstore.Store[ContainerApp]
}

// New creates an empty Container Apps mock. The clock stamps revision createdTime
// values; it falls back to the real clock when opts (or its clock) is nil so the
// mock stays usable standalone (e.g. New(nil) in tests).
func New(opts *config.Options) *Mock {
	clock := config.Clock(config.RealClock{})
	if opts != nil && opts.Clock != nil {
		clock = opts.Clock
	}

	return &Mock{
		clock: clock,
		envs:  memstore.New[Environment](),
		apps:  memstore.New[ContainerApp](),
	}
}

func armID(sub, rg, resourceType, name string) string {
	return "/subscriptions/" + sub +
		"/resourceGroups/" + rg +
		"/providers/" + providerNamespace +
		"/" + resourceType +
		"/" + name
}

// key is the case-insensitive store key shared by both resource types (their
// ARM ids differ by resourceType, so a single key space is unambiguous).
func key(sub, rg, resourceType, name string) string {
	return strings.ToLower(armID(sub, rg, resourceType, name))
}

// shortHash returns the first shortHashLen hex chars of the SHA-256 of s, a
// deterministic seed for minted domain/revision/IP values.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(s)))

	return hex.EncodeToString(sum[:])[:shortHashLen]
}

// CreateOrUpdateEnvironment creates or updates a managed environment. The minted
// DefaultDomain and StaticIP are set only on first create and preserved on
// update. Returns the stored environment and whether it was newly created.
func (m *Mock) CreateOrUpdateEnvironment(
	_ context.Context, sub, rg, name string, in EnvironmentInput,
) (Environment, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, typeEnvironments, name)

	env, existed := m.envs.Get(k)
	created := !existed

	if created {
		env = Environment{Subscription: sub, ResourceGroup: rg, Name: name}
		h := shortHash(env.ARMID())
		env.DefaultDomain = strings.ToLower(name) + "-" + h + "." + defaultRegion(in.Location) + domainSuffix
		env.StaticIP = staticIP(h)
	}

	env.Location = in.Location
	env.Tags = maps.Clone(in.Tags)
	env.AppLogs = cloneAppLogs(in.AppLogs)

	m.envs.Set(k, env)

	return env, created, nil
}

// GetEnvironment returns the environment or a NotFound error.
func (m *Mock) GetEnvironment(_ context.Context, sub, rg, name string) (Environment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	env, ok := m.envs.Get(key(sub, rg, typeEnvironments, name))
	if !ok {
		return Environment{}, cerrors.Newf(cerrors.NotFound, "managed environment %q not found", name)
	}

	return env, nil
}

// DeleteEnvironment removes an environment, reporting whether it existed.
func (m *Mock) DeleteEnvironment(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.envs.Delete(key(sub, rg, typeEnvironments, name)), nil
}

// ListEnvironmentsByResourceGroup returns every environment in sub/rg.
func (m *Mock) ListEnvironmentsByResourceGroup(_ context.Context, sub, rg string) ([]Environment, error) {
	return m.filterEnvs(func(e *Environment) bool {
		return strings.EqualFold(e.Subscription, sub) && strings.EqualFold(e.ResourceGroup, rg)
	}), nil
}

// ListEnvironmentsBySubscription returns every environment in sub.
func (m *Mock) ListEnvironmentsBySubscription(_ context.Context, sub string) ([]Environment, error) {
	return m.filterEnvs(func(e *Environment) bool {
		return strings.EqualFold(e.Subscription, sub)
	}), nil
}

// CreateOrUpdateApp creates or updates a container app. Fqdn and
// LatestRevisionName are minted only on first create and preserved on update;
// the fqdn is derived from the referenced environment's default domain when that
// environment exists.
func (m *Mock) CreateOrUpdateApp(
	_ context.Context, sub, rg, name string, in *AppInput,
) (ContainerApp, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, typeContainerApps, name)

	app, existed := m.apps.Get(k)
	created := !existed

	if created {
		app = ContainerApp{Subscription: sub, ResourceGroup: rg, Name: name}
	} else {
		// Get returns a value copy but the revision slice shares its backing
		// array; clone it so a validation failure below leaves the stored app
		// untouched (we only Set on success).
		app.Revisions = cloneRevisions(app.Revisions)
	}

	app.Location = in.Location
	app.Tags = maps.Clone(in.Tags)
	app.EnvironmentID = in.EnvironmentID
	app.ActiveRevMode = in.ActiveRevMode
	app.Ingress = cloneIngress(in.Ingress)
	app.SecretNames = append([]string(nil), in.SecretNames...)
	app.Template = cloneTemplate(in.Template)
	app.Fqdn = m.appFqdnLocked(name, in.EnvironmentID, in.Ingress)

	if err := m.materializeRevisionLocked(&app); err != nil {
		return ContainerApp{}, false, err
	}

	if err := validateTrafficLocked(&app); err != nil {
		return ContainerApp{}, false, err
	}

	m.apps.Set(k, app)

	return app, created, nil
}

// GetApp returns the container app or a NotFound error.
func (m *Mock) GetApp(_ context.Context, sub, rg, name string) (ContainerApp, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	app, ok := m.apps.Get(key(sub, rg, typeContainerApps, name))
	if !ok {
		return ContainerApp{}, cerrors.Newf(cerrors.NotFound, "container app %q not found", name)
	}

	return app, nil
}

// DeleteApp removes a container app, reporting whether it existed.
func (m *Mock) DeleteApp(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.apps.Delete(key(sub, rg, typeContainerApps, name)), nil
}

// ListAppsByResourceGroup returns every container app in sub/rg.
func (m *Mock) ListAppsByResourceGroup(_ context.Context, sub, rg string) ([]ContainerApp, error) {
	return m.filterApps(func(a *ContainerApp) bool {
		return strings.EqualFold(a.Subscription, sub) && strings.EqualFold(a.ResourceGroup, rg)
	}), nil
}

// ListAppsBySubscription returns every container app in sub.
func (m *Mock) ListAppsBySubscription(_ context.Context, sub string) ([]ContainerApp, error) {
	return m.filterApps(func(a *ContainerApp) bool {
		return strings.EqualFold(a.Subscription, sub)
	}), nil
}

// DiscoverEnvironments returns every stored environment, for the inventory walk.
func (m *Mock) DiscoverEnvironments(_ context.Context) ([]Environment, error) {
	return m.filterEnvs(func(*Environment) bool { return true }), nil
}

// DiscoverApps returns every stored container app, for the inventory walk.
func (m *Mock) DiscoverApps(_ context.Context) ([]ContainerApp, error) {
	return m.filterApps(func(*ContainerApp) bool { return true }), nil
}

// PurgeResourceGroup deletes every environment and container app under sub/rg, so
// a resource-group delete cascades into its Container Apps resources.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	envs := m.envs.All()
	for k := range envs {
		if strings.EqualFold(envs[k].Subscription, sub) && strings.EqualFold(envs[k].ResourceGroup, rg) {
			m.envs.Delete(k)
		}
	}

	apps := m.apps.All()
	for k := range apps {
		if strings.EqualFold(apps[k].Subscription, sub) && strings.EqualFold(apps[k].ResourceGroup, rg) {
			m.apps.Delete(k)
		}
	}

	return nil
}

// appFqdnLocked derives an app's ingress fqdn from the referenced environment's
// default domain. Returns "" when the app has no ingress. Callers hold m.mu.
func (m *Mock) appFqdnLocked(name, environmentID string, ingress *Ingress) string {
	if ingress == nil {
		return ""
	}

	domain := m.envDomainLocked(environmentID)
	if domain == "" {
		return ""
	}

	return strings.ToLower(name) + "." + domain
}

// envDomainLocked resolves the default domain of the environment an app id
// points at, or "" when the id is unparsable or the environment is absent.
// Callers hold m.mu.
func (m *Mock) envDomainLocked(environmentID string) string {
	sub, rg, name, ok := parseEnvironmentID(environmentID)
	if !ok {
		return ""
	}

	env, ok := m.envs.Get(key(sub, rg, typeEnvironments, name))
	if !ok {
		return ""
	}

	return env.DefaultDomain
}

func (m *Mock) filterEnvs(pred func(*Environment) bool) []Environment {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return filterSorted(m.envs.SortedValues(), pred, func(e *Environment) string { return e.Name })
}

func (m *Mock) filterApps(pred func(*ContainerApp) bool) []ContainerApp {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return filterSorted(m.apps.SortedValues(), pred, func(a *ContainerApp) string { return a.Name })
}

// filterSorted returns the elements of vals matching pred, sorted by the key
// nameOf projects, without copying whole records into the range variable.
func filterSorted[V any](vals []V, pred func(*V) bool, nameOf func(*V) string) []V {
	var out []V

	for i := range vals {
		if pred(&vals[i]) {
			out = append(out, vals[i])
		}
	}

	sort.Slice(out, func(i, j int) bool { return nameOf(&out[i]) < nameOf(&out[j]) })

	return out
}

// parseEnvironmentID extracts sub/rg/name from a managedEnvironments ARM id.
func parseEnvironmentID(id string) (sub, rg, name string, ok bool) {
	parts := strings.Split(strings.Trim(id, "/"), "/")
	sub = segAfter(parts, "subscriptions")
	rg = segAfter(parts, "resourceGroups")
	name = envNameFromParts(parts)

	return sub, rg, name, sub != "" && rg != "" && name != ""
}

// segAfter returns the path segment immediately following the first
// (case-insensitive) match of keyword, or "" when there is none.
func segAfter(parts []string, keyword string) string {
	for i := range parts {
		if strings.EqualFold(parts[i], keyword) && i+1 < len(parts) {
			return parts[i+1]
		}
	}

	return ""
}

// envNameFromParts returns the environment name from a
// .../Microsoft.App/managedEnvironments/{name} path, or "" when absent.
func envNameFromParts(parts []string) string {
	const nameOffset = 2

	for i := range parts {
		if strings.EqualFold(parts[i], providerNamespace) &&
			i+nameOffset < len(parts) && strings.EqualFold(parts[i+1], typeEnvironments) {
			return parts[i+nameOffset]
		}
	}

	return ""
}

func defaultRegion(loc string) string {
	if loc == "" {
		return "eastus"
	}

	return strings.ToLower(strings.ReplaceAll(loc, " ", ""))
}

// staticIP derives a deterministic public-shaped ingress IP from a hash seed, so
// each environment reports a stable, distinct static IP.
func staticIP(h string) string {
	const (
		hexBase   = 16
		bitSize   = 8
		octetLen  = 2
		decBase   = 10
		mod       = 254
		numOctets = 3
	)

	octets := make([]string, 0, numOctets)

	for i := 1; i <= numOctets; i++ {
		b, _ := strconv.ParseUint(h[i*octetLen:i*octetLen+octetLen], hexBase, bitSize)
		octets = append(octets, strconv.FormatUint(b%mod+1, decBase))
	}

	return "20." + strings.Join(octets, ".")
}

func cloneAppLogs(in *AppLogsConfiguration) *AppLogsConfiguration {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func cloneIngress(in *Ingress) *Ingress {
	if in == nil {
		return nil
	}

	out := *in
	out.Traffic = append([]TrafficWeight(nil), in.Traffic...)

	return &out
}

func cloneTemplate(in Template) Template {
	out := Template{RevisionSuffix: in.RevisionSuffix}

	if in.Scale != nil {
		s := *in.Scale
		out.Scale = &s
	}

	for _, c := range in.Containers {
		cc := Container{Name: c.Name, Image: c.Image}
		cc.Env = append([]EnvVar(nil), c.Env...)

		if c.Resources != nil {
			r := *c.Resources
			cc.Resources = &r
		}

		out.Containers = append(out.Containers, cc)
	}

	return out
}
