package apigateway

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// Compile-time check for the optional capability.
var _ driver.APIKeys = (*Mock)(nil)

const (
	msgNoSuchKey    = "Invalid API Key identifier specified"
	msgKeyDuplicate = "API Key already exists"
	msgKeyLength    = "API Key value must be between 20 and 128 characters"

	minAPIKeyValueLen = 20
	maxAPIKeyValueLen = 128
	generatedKeyLen   = 40
)

// CreateAPIKey creates an API key. A supplied Value must be 20-128 characters
// and unique among the account's keys; otherwise one is generated.
func (m *Mock) CreateAPIKey(_ context.Context, in *driver.CreateAPIKeyInput) (*driver.APIKey, error) {
	value := in.Value
	if value != "" && (len(value) < minAPIKeyValueLen || len(value) > maxAPIKeyValueLen) {
		return nil, cerrors.New(cerrors.InvalidArgument, msgKeyLength)
	}

	stages, err := m.checkStageKeys(in.StageKeys)
	if err != nil {
		return nil, err
	}

	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if value == "" {
		value = randomID(generatedKeyLen)
	}

	for _, k := range m.keys {
		if k.Value == value {
			return nil, cerrors.New(cerrors.AlreadyExists, msgKeyDuplicate)
		}
	}

	now := m.now()
	key := &driver.APIKey{
		ID: genID(), Value: value, Name: in.Name, CustomerID: in.CustomerID, Description: in.Description,
		Enabled: in.Enabled, CreatedDate: now, LastUpdatedDate: now, StageKeys: stages, Tags: copyStrMap(in.Tags),
	}
	m.keys[key.ID] = key

	out := copyAPIKey(key, true)

	return &out, nil
}

// checkStageKeys validates that each stage key names an existing API and stage.
func (m *Mock) checkStageKeys(in []driver.StageKey) ([]string, error) {
	out := make([]string, 0, len(in))

	for _, sk := range in {
		ad, err := m.getAPI(sk.RestAPIID)
		if err != nil {
			return nil, err
		}

		ad.mu.RLock()
		_, ok := ad.stages[sk.StageName]
		ad.mu.RUnlock()

		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "Invalid stage identifier specified %s", sk.StageName)
		}

		out = append(out, sk.RestAPIID+"/"+sk.StageName)
	}

	return out, nil
}

// GetAPIKey returns one key; its value is included only when asked for.
func (m *Mock) GetAPIKey(_ context.Context, id string, includeValue bool) (*driver.APIKey, error) {
	m.regionMu.RLock()
	defer m.regionMu.RUnlock()

	key, ok := m.keys[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgNoSuchKey)
	}

	out := copyAPIKey(key, includeValue)

	return &out, nil
}

// GetAPIKeys lists keys ordered by creation then id, filtered by name prefix
// and customer id. Values are omitted unless IncludeValues is set.
func (m *Mock) GetAPIKeys(_ context.Context, in *driver.GetAPIKeysInput) (*driver.APIKeyPage, error) {
	m.regionMu.RLock()

	all := make([]driver.APIKey, 0, len(m.keys))

	for _, k := range m.keys {
		if in.NameQuery != "" && !strings.HasPrefix(k.Name, in.NameQuery) {
			continue
		}

		if in.CustomerID != "" && k.CustomerID != in.CustomerID {
			continue
		}

		all = append(all, copyAPIKey(k, in.IncludeValues))
	}

	m.regionMu.RUnlock()

	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedDate != all[j].CreatedDate {
			return all[i].CreatedDate < all[j].CreatedDate
		}

		return all[i].ID < all[j].ID
	})

	items, next, err := pageOf(all, in.PageInput)
	if err != nil {
		return nil, err
	}

	return &driver.APIKeyPage{Items: items, Position: next}, nil
}

// UpdateAPIKey applies a patchOperations document: /name, /description,
// /enabled, /customerId and /stages (add or remove "restApiId/stage").
func (m *Mock) UpdateAPIKey(_ context.Context, id string, ops []driver.PatchOperation) (*driver.APIKey, error) {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	key, ok := m.keys[id]
	if !ok {
		return nil, cerrors.New(cerrors.NotFound, msgNoSuchKey)
	}

	upd := *key
	upd.StageKeys = copyStrSlice(key.StageKeys)

	for _, op := range ops {
		if err := m.applyAPIKeyPatch(&upd, op); err != nil {
			return nil, err
		}
	}

	upd.LastUpdatedDate = m.now()
	*key = upd

	out := copyAPIKey(key, false)

	return &out, nil
}

func (m *Mock) applyAPIKeyPatch(k *driver.APIKey, op driver.PatchOperation) error {
	switch op.Path {
	case pathName:
		k.Name = op.Value
	case pathDescription:
		k.Description = op.Value
	case "/enabled":
		k.Enabled = parseBool(op.Value)
	case "/customerId":
		k.CustomerID = op.Value
	case "/stages":
		if op.Op == opAdd {
			apiID, stage, _ := strings.Cut(op.Value, "/")
			if _, err := m.checkStageKeys([]driver.StageKey{{RestAPIID: apiID, StageName: stage}}); err != nil {
				return err
			}
		}

		k.StageKeys = patchStringSlice(k.StageKeys, op.Op, op.Value)
	default:
		return invalidPatchPath(op, pathName, pathDescription, "/enabled", "/customerId", "/stages")
	}

	return nil
}

// DeleteAPIKey removes a key and detaches it from every usage plan.
func (m *Mock) DeleteAPIKey(_ context.Context, id string) error {
	m.regionMu.Lock()
	defer m.regionMu.Unlock()

	if _, ok := m.keys[id]; !ok {
		return cerrors.New(cerrors.NotFound, msgNoSuchKey)
	}

	delete(m.keys, id)

	m.usageMu.Lock()
	defer m.usageMu.Unlock()

	for plan, keys := range m.planKeys {
		delete(keys, id)
		delete(m.usage, plan+"|"+id)
		delete(m.buckets, plan+"|"+id)
	}

	return nil
}

// copyAPIKey deep-copies a key, dropping its secret value unless includeValue.
func copyAPIKey(k *driver.APIKey, includeValue bool) driver.APIKey {
	out := *k
	out.StageKeys = copyStrSlice(k.StageKeys)
	out.Tags = copyStrMap(k.Tags)

	if !includeValue {
		out.Value = ""
	}

	return out
}
