package containerapps

import (
	"context"
	"slices"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// DaprMetadata is one name/value pair of a Dapr component; SecretRef names a
// component secret instead of an inline value.
type DaprMetadata struct {
	Name      string `json:"name"`
	Value     string `json:"value,omitempty"`
	SecretRef string `json:"secretRef,omitempty"`
}

// DaprSecret is one secret a Dapr component carries.
type DaprSecret struct {
	Name        string `json:"name"`
	Value       string `json:"value,omitempty"`
	KeyVaultURL string `json:"keyVaultUrl,omitempty"`
	Identity    string `json:"identity,omitempty"`
}

// DaprComponent is a managedEnvironments/{e}/daprComponents/{n} resource. The
// JSON names are the ARM properties names.
type DaprComponent struct {
	Name                 string         `json:"-"`
	ComponentType        string         `json:"componentType"`
	Version              string         `json:"version"`
	IgnoreErrors         bool           `json:"ignoreErrors"`
	InitTimeout          string         `json:"initTimeout,omitempty"`
	SecretStoreComponent string         `json:"secretStoreComponent,omitempty"`
	Metadata             []DaprMetadata `json:"metadata,omitempty"`
	Secrets              []DaprSecret   `json:"secrets,omitempty"`
	Scopes               []string       `json:"scopes,omitempty"`
}

// AzureFileStorage is the Azure Files share an environment storage mounts.
type AzureFileStorage struct {
	AccountName string `json:"accountName"`
	AccountKey  string `json:"accountKey,omitempty"`
	ShareName   string `json:"shareName"`
	AccessMode  string `json:"accessMode"`
}

// EnvStorage is a managedEnvironments/{e}/storages/{n} resource.
type EnvStorage struct {
	Name      string            `json:"-"`
	AzureFile *AzureFileStorage `json:"azureFile,omitempty"`
}

// envChildRecord wraps a child with its name so the snapshot keeps it.
type envChildRecord[T any] struct {
	Name string `json:"name"`
	Spec T      `json:"spec"`
}

func childKey(envKey, name string) string {
	return envKey + "/" + strings.ToLower(name)
}

// putEnvChild stores v under the environment, which must exist. Callers hold
// no lock; the environment check and the write happen under m.mu.
func putEnvChild[T any](m *Mock, store *memstore.Store[envChildRecord[T]], sub, rg, env, name string, v T) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	envKey := key(sub, rg, typeEnvironments, env)
	if !m.envs.Has(envKey) {
		return cerrors.Newf(cerrors.NotFound, "managed environment %q not found", env)
	}

	store.Set(childKey(envKey, name), envChildRecord[T]{Name: name, Spec: v})

	return nil
}

func getEnvChild[T any](m *Mock, store *memstore.Store[envChildRecord[T]], sub, rg, env, name, kind string,
) (envChildRecord[T], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, ok := store.Get(childKey(key(sub, rg, typeEnvironments, env), name))
	if !ok {
		return rec, cerrors.Newf(cerrors.NotFound, "%s %q not found in managed environment %q", kind, name, env)
	}

	return rec, nil
}

func deleteEnvChild[T any](m *Mock, store *memstore.Store[envChildRecord[T]], sub, rg, env, name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return store.Delete(childKey(key(sub, rg, typeEnvironments, env), name))
}

func listEnvChildren[T any](m *Mock, store *memstore.Store[envChildRecord[T]], sub, rg, env string,
) ([]envChildRecord[T], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	envKey := key(sub, rg, typeEnvironments, env)
	if !m.envs.Has(envKey) {
		return nil, cerrors.Newf(cerrors.NotFound, "managed environment %q not found", env)
	}

	recs := store.Filter(func(k string, _ envChildRecord[T]) bool { return strings.HasPrefix(k, envKey+"/") })

	out := make([]envChildRecord[T], 0, len(recs))
	for _, rec := range recs {
		out = append(out, rec)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// deleteEnvChildrenLocked drops every child of one environment. The trailing
// "/" bounds the prefix, so env1 never reaches env10. Callers hold m.mu.
func (m *Mock) deleteEnvChildrenLocked(envKey string) {
	prefix := envKey + "/"

	for _, k := range m.dapr.Keys() {
		if strings.HasPrefix(k, prefix) {
			m.dapr.Delete(k)
		}
	}

	for _, k := range m.storages.Keys() {
		if strings.HasPrefix(k, prefix) {
			m.storages.Delete(k)
		}
	}
}

// PutDaprComponent creates or replaces a Dapr component. componentType and
// version are required, as in real Azure.
func (m *Mock) PutDaprComponent(_ context.Context, sub, rg, env string, c *DaprComponent) (DaprComponent, error) {
	if c.ComponentType == "" || c.Version == "" {
		return DaprComponent{}, cerrors.New(cerrors.InvalidArgument,
			"properties.componentType and properties.version are required")
	}

	clone := cloneDapr(c)
	if err := putEnvChild(m, m.dapr, sub, rg, env, c.Name, clone); err != nil {
		return DaprComponent{}, err
	}

	return cloneDapr(&clone), nil
}

// GetDaprComponent returns a Dapr component with its secret values intact;
// the wire layer masks them on GET.
func (m *Mock) GetDaprComponent(_ context.Context, sub, rg, env, name string) (DaprComponent, error) {
	rec, err := getEnvChild(m, m.dapr, sub, rg, env, name, "dapr component")
	if err != nil {
		return DaprComponent{}, err
	}

	out := cloneDapr(&rec.Spec)
	out.Name = rec.Name

	return out, nil
}

// DeleteDaprComponent removes a Dapr component, reporting whether it existed.
func (m *Mock) DeleteDaprComponent(_ context.Context, sub, rg, env, name string) (bool, error) {
	return deleteEnvChild(m, m.dapr, sub, rg, env, name), nil
}

// ListDaprComponents returns the environment's Dapr components by name.
func (m *Mock) ListDaprComponents(_ context.Context, sub, rg, env string) ([]DaprComponent, error) {
	recs, err := listEnvChildren(m, m.dapr, sub, rg, env)
	if err != nil {
		return nil, err
	}

	out := make([]DaprComponent, 0, len(recs))

	for i := range recs {
		c := cloneDapr(&recs[i].Spec)
		c.Name = recs[i].Name
		out = append(out, c)
	}

	return out, nil
}

// PutEnvStorage creates or replaces an environment storage. accessMode is
// ReadOnly or ReadWrite, as in real Azure.
func (m *Mock) PutEnvStorage(_ context.Context, sub, rg, env string, s *EnvStorage) (EnvStorage, error) {
	f := s.AzureFile
	if f == nil || f.AccountName == "" || f.ShareName == "" {
		return EnvStorage{}, cerrors.New(cerrors.InvalidArgument,
			"properties.azureFile.accountName and properties.azureFile.shareName are required")
	}

	if f.AccessMode != "ReadOnly" && f.AccessMode != "ReadWrite" {
		return EnvStorage{}, cerrors.Newf(cerrors.InvalidArgument,
			"properties.azureFile.accessMode %q is not one of ReadOnly, ReadWrite", f.AccessMode)
	}

	clone := EnvStorage{Name: s.Name, AzureFile: copyFile(f)}
	if err := putEnvChild(m, m.storages, sub, rg, env, s.Name, clone); err != nil {
		return EnvStorage{}, err
	}

	return EnvStorage{Name: s.Name, AzureFile: copyFile(f)}, nil
}

// GetEnvStorage returns an environment storage, account key included; the
// wire layer never returns the key.
func (m *Mock) GetEnvStorage(_ context.Context, sub, rg, env, name string) (EnvStorage, error) {
	rec, err := getEnvChild(m, m.storages, sub, rg, env, name, "storage")
	if err != nil {
		return EnvStorage{}, err
	}

	return cloneStorage(rec), nil
}

// DeleteEnvStorage removes an environment storage, reporting whether it existed.
func (m *Mock) DeleteEnvStorage(_ context.Context, sub, rg, env, name string) (bool, error) {
	return deleteEnvChild(m, m.storages, sub, rg, env, name), nil
}

// ListEnvStorages returns the environment's storages by name.
func (m *Mock) ListEnvStorages(_ context.Context, sub, rg, env string) ([]EnvStorage, error) {
	recs, err := listEnvChildren(m, m.storages, sub, rg, env)
	if err != nil {
		return nil, err
	}

	out := make([]EnvStorage, 0, len(recs))
	for _, rec := range recs {
		out = append(out, cloneStorage(rec))
	}

	return out, nil
}

func cloneStorage(rec envChildRecord[EnvStorage]) EnvStorage {
	out := EnvStorage{Name: rec.Name}
	if rec.Spec.AzureFile != nil {
		out.AzureFile = copyFile(rec.Spec.AzureFile)
	}

	return out
}

func copyFile(f *AzureFileStorage) *AzureFileStorage {
	out := *f

	return &out
}

func cloneDapr(c *DaprComponent) DaprComponent {
	out := *c
	out.Metadata = slices.Clone(c.Metadata)
	out.Secrets = slices.Clone(c.Secrets)
	out.Scopes = slices.Clone(c.Scopes)

	return out
}
