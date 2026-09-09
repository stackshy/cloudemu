package composer

import (
	"encoding/json"

	composer "google.golang.org/api/composer/v1"

	cdriver "github.com/stackshy/cloudemu/v2/services/composer/driver"
)

// environmentTypeURL is the google.protobuf.Any type URL for a Composer
// Environment, embedded in a completed operation's `response` so an SDK that
// unpacks the Any resolves the environment.
const environmentTypeURL = "type.googleapis.com/google.cloud.orchestration.airflow.service.v1.Environment"

// modeledConfigKeys are the config JSON fields CloudEmu renders itself; every
// other key a caller sends is captured verbatim in EnvironmentConfig.Other so a
// read round-trips exactly.
func modeledConfigKeys() map[string]bool {
	return map[string]bool{
		"nodeCount": true, "environmentSize": true, "resilienceMode": true,
		"softwareConfig": true, "nodeConfig": true,
		// output-only fields CloudEmu computes; never carried as passthrough.
		"gkeCluster": true, "dagGcsPrefix": true, "airflowUri": true, "airflowByoidUri": true,
	}
}

// fromWireConfig maps a decoded wire EnvironmentConfig to the driver config,
// echoing the modeled fields and capturing every unmodeled sub-block verbatim
// from rawConfig so a read round-trips. rawConfig is the raw JSON of the config
// object (nil when the caller sent no config).
func fromWireConfig(cfg *composer.EnvironmentConfig, rawConfig json.RawMessage) cdriver.EnvironmentConfig {
	if cfg == nil {
		return cdriver.EnvironmentConfig{}
	}

	out := cdriver.EnvironmentConfig{
		NodeCount:       cfg.NodeCount,
		EnvironmentSize: cfg.EnvironmentSize,
		ResilienceMode:  cfg.ResilienceMode,
		SoftwareConfig:  fromWireSoftware(cfg.SoftwareConfig),
		NodeConfig:      fromWireNode(cfg.NodeConfig),
		Other:           captureOther(rawConfig),
	}

	return out
}

// captureOther extracts the config sub-blocks CloudEmu does not model from the
// raw config JSON, keyed by their canonical JSON field name.
func captureOther(rawConfig json.RawMessage) map[string]json.RawMessage {
	if len(rawConfig) == 0 {
		return nil
	}

	var all map[string]json.RawMessage
	if err := json.Unmarshal(rawConfig, &all); err != nil {
		return nil
	}

	modeled := modeledConfigKeys()
	out := make(map[string]json.RawMessage)

	for k, v := range all {
		if !modeled[k] {
			out[k] = v
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func fromWireSoftware(s *composer.SoftwareConfig) *cdriver.SoftwareConfig {
	if s == nil {
		return nil
	}

	return &cdriver.SoftwareConfig{
		ImageVersion:           s.ImageVersion,
		AirflowConfigOverrides: s.AirflowConfigOverrides,
		PypiPackages:           s.PypiPackages,
		EnvVariables:           s.EnvVariables,
		PythonVersion:          s.PythonVersion,
		SchedulerCount:         s.SchedulerCount,
	}
}

func fromWireNode(n *composer.NodeConfig) *cdriver.NodeConfig {
	if n == nil {
		return nil
	}

	return &cdriver.NodeConfig{
		Location:       n.Location,
		MachineType:    n.MachineType,
		Network:        n.Network,
		Subnetwork:     n.Subnetwork,
		DiskSizeGb:     n.DiskSizeGb,
		ServiceAccount: n.ServiceAccount,
		Tags:           n.Tags,
		OauthScopes:    n.OauthScopes,
	}
}

// toWireSoftware maps a driver SoftwareConfig to the wire shape.
func toWireSoftware(s *cdriver.SoftwareConfig) *composer.SoftwareConfig {
	if s == nil {
		return nil
	}

	return &composer.SoftwareConfig{
		ImageVersion:           s.ImageVersion,
		AirflowConfigOverrides: s.AirflowConfigOverrides,
		PypiPackages:           s.PypiPackages,
		EnvVariables:           s.EnvVariables,
		PythonVersion:          s.PythonVersion,
		SchedulerCount:         s.SchedulerCount,
	}
}

func toWireNode(n *cdriver.NodeConfig) *composer.NodeConfig {
	if n == nil {
		return nil
	}

	return &composer.NodeConfig{
		Location:       n.Location,
		MachineType:    n.MachineType,
		Network:        n.Network,
		Subnetwork:     n.Subnetwork,
		DiskSizeGb:     n.DiskSizeGb,
		ServiceAccount: n.ServiceAccount,
		Tags:           n.Tags,
		OauthScopes:    n.OauthScopes,
	}
}

// toEnvironmentJSON renders a driver environment as the composer/v1 Environment
// wire JSON, merging the verbatim passthrough config sub-blocks back in. The
// modeled + computed fields are marshaled from typed values and the opaque
// Other blocks are injected into the config object under their original keys.
func toEnvironmentJSON(e *cdriver.Environment) (json.RawMessage, error) {
	cfg := &composer.EnvironmentConfig{
		NodeCount:       e.Config.NodeCount,
		EnvironmentSize: e.Config.EnvironmentSize,
		ResilienceMode:  e.Config.ResilienceMode,
		SoftwareConfig:  toWireSoftware(e.Config.SoftwareConfig),
		NodeConfig:      toWireNode(e.Config.NodeConfig),
		GkeCluster:      e.Config.GkeCluster,
		DagGcsPrefix:    e.Config.DagGcsPrefix,
		AirflowUri:      e.Config.AirflowURI,
		AirflowByoidUri: e.Config.AirflowByoidURI,
	}

	cfgMap, err := structToMap(cfg)
	if err != nil {
		return nil, err
	}

	for k, v := range e.Config.Other {
		if _, taken := cfgMap[k]; !taken {
			cfgMap[k] = v
		}
	}

	envMap := map[string]json.RawMessage{}

	if err := putJSON(envMap, "name", environmentResourceName(e.Project, e.Location, e.EnvironmentID)); err != nil {
		return nil, err
	}

	if err := putScalars(envMap, e); err != nil {
		return nil, err
	}

	if err := putJSON(envMap, "config", cfgMap); err != nil {
		return nil, err
	}

	return json.Marshal(envMap)
}

// putScalars adds the environment's top-level scalar and map fields.
func putScalars(envMap map[string]json.RawMessage, e *cdriver.Environment) error {
	fields := []struct {
		key string
		val any
	}{
		{"uuid", e.UUID},
		{"state", e.State},
		{"createTime", formatTime(e.CreateTime)},
		{"updateTime", formatTime(e.UpdateTime)},
	}

	for _, f := range fields {
		if s, ok := f.val.(string); ok && s == "" {
			continue
		}

		if err := putJSON(envMap, f.key, f.val); err != nil {
			return err
		}
	}

	if len(e.Labels) > 0 {
		if err := putJSON(envMap, "labels", e.Labels); err != nil {
			return err
		}
	}

	if e.StorageBucket != "" {
		if err := putJSON(envMap, "storageConfig", composer.StorageConfig{Bucket: e.StorageBucket}); err != nil {
			return err
		}
	}

	return nil
}

// structToMap marshals v and unmarshals it into a flat key→raw map, dropping the
// empty-valued fields json omitempty already elided.
func structToMap(v any) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	out := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}

	return out, nil
}

// putJSON marshals val and stores it under key in m.
func putJSON(m map[string]json.RawMessage, key string, val any) error {
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}

	m[key] = raw

	return nil
}

// environmentResponseAny wraps an environment JSON object as a
// google.protobuf.Any (adding the "@type" discriminator), the shape a completed
// operation's `response` carries.
func environmentResponseAny(envJSON json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(envJSON, &fields) != nil {
		return nil
	}

	fields["@type"] = json.RawMessage(`"` + environmentTypeURL + `"`)

	out, err := json.Marshal(fields)
	if err != nil {
		return nil
	}

	return out
}
