package mwaa

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/mwaa/driver"
)

// Request-body fields the emulator handles specially and therefore strips from
// the verbatim configuration passthrough: Tags is modeled separately (it is
// also mutated by the tagging operations); Name is the URI label, not body
// state; WorkerReplacementStrategy is an update-only directive, not environment
// state, so it is never echoed back.
const (
	fieldTags                      = "Tags"
	fieldName                      = "Name"
	fieldWorkerReplacementStrategy = "WorkerReplacementStrategy"
	fieldLoggingConfiguration      = "LoggingConfiguration"
	fieldNetworkConfiguration      = "NetworkConfiguration"
)

// CreateEnvironment provisions a new environment directly in the AVAILABLE
// state with stable computed fields (arn, webserverUrl, serviceRoleArn,
// createdAt). The configuration blocks are carried verbatim so a GetEnvironment
// reflects exactly what the caller sent, with the per-module
// CloudWatchLogGroupArn injected into LoggingConfiguration. A name already in
// use yields a ValidationException.
func (m *Mock) CreateEnvironment(_ context.Context, in *driver.CreateEnvironmentInput) (*driver.Environment, error) {
	if in.Name == "" {
		return nil, validation("Name is required")
	}

	if m.envs.Has(in.Name) {
		return nil, validation("An environment with the name %s already exists.", in.Name)
	}

	cfg := copyConfig(in.Config)
	if cfg == nil {
		cfg = map[string]json.RawMessage{}
	}

	delete(cfg, fieldTags)
	delete(cfg, fieldName)
	cfg[fieldLoggingConfiguration] = m.augmentLogging(in.Name, cfg[fieldLoggingConfiguration])

	now := m.now()
	env := driver.Environment{
		Name:           in.Name,
		Arn:            m.environmentARN(in.Name),
		Status:         driver.StatusAvailable,
		WebserverURL:   m.webserverURL(in.Name),
		ServiceRoleArn: m.serviceRoleARN(),
		CreatedAt:      now,
		LastUpdatedAt:  now,
		Tags:           copyTags(in.Tags),
		Config:         cfg,
	}

	m.envs.Set(in.Name, env)

	out := copyEnv(&env)

	return &out, nil
}

// GetEnvironment returns a copy of the environment. The stored arn, status,
// webserverUrl, serviceRoleArn and createdAt are returned unchanged so repeated
// reads never drift.
func (m *Mock) GetEnvironment(_ context.Context, name string) (*driver.Environment, error) {
	e, ok := m.envs.Get(name)
	if !ok {
		return nil, notFound(name)
	}

	out := copyEnv(&e)

	return &out, nil
}

// UpdateEnvironment merges the fields the PATCH request supplied, leaving
// unmentioned fields untouched. The computed arn, status, webserverUrl,
// serviceRoleArn and createdAt are preserved; lastUpdatedAt is bumped.
// LoggingConfiguration is re-normalized; NetworkConfiguration is deep-merged so
// an update that changes only the security groups preserves the immutable
// subnet ids.
func (m *Mock) UpdateEnvironment(_ context.Context, in *driver.UpdateEnvironmentInput) (*driver.Environment, error) {
	var updated driver.Environment

	ok := m.envs.Update(in.Name, func(e driver.Environment) driver.Environment {
		e.Config = copyConfig(e.Config)
		if e.Config == nil {
			e.Config = map[string]json.RawMessage{}
		}

		for k, v := range in.Config {
			switch k {
			case fieldTags, fieldName, fieldWorkerReplacementStrategy:
				continue
			case fieldLoggingConfiguration:
				e.Config[k] = m.augmentLogging(e.Name, v)
			case fieldNetworkConfiguration:
				e.Config[k] = mergeNetworkConfig(e.Config[k], v)
			default:
				e.Config[k] = append(json.RawMessage(nil), v...)
			}
		}

		e.LastUpdatedAt = m.now()
		updated = e

		return e
	})
	if !ok {
		return nil, notFound(in.Name)
	}

	out := copyEnv(&updated)

	return &out, nil
}

// DeleteEnvironment removes an environment.
func (m *Mock) DeleteEnvironment(_ context.Context, name string) error {
	if !m.envs.Delete(name) {
		return notFound(name)
	}

	return nil
}

// ListEnvironments returns a deterministic page of environment names.
func (m *Mock) ListEnvironments(_ context.Context, page driver.Page) (names []string, nextToken string, err error) {
	stored := m.envs.SortedValues()

	all := make([]string, 0, len(stored))
	for i := range stored {
		all = append(all, stored[i].Name)
	}

	start, end, next := paginate(len(all), page)

	return all[start:end], next, nil
}

// mergeNetworkConfig applies the incoming NetworkConfiguration fields onto the
// stored block, preserving stored fields the update omitted (notably the
// immutable SubnetIds, which UpdateEnvironment does not accept).
func mergeNetworkConfig(existing, incoming json.RawMessage) json.RawMessage {
	cur := map[string]json.RawMessage{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &cur)
	}

	inc := map[string]json.RawMessage{}
	if len(incoming) > 0 {
		_ = json.Unmarshal(incoming, &inc)
	}

	for k, v := range inc {
		cur[k] = v
	}

	b, err := json.Marshal(cur)
	if err != nil {
		return incoming
	}

	return b
}
