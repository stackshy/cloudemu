package composer

import (
	"encoding/json"

	cdriver "github.com/stackshy/cloudemu/v2/services/composer/driver"
)

// cloneEnvironment returns a deep copy of e so a stored environment is never
// aliased by a value handed back to a caller (which the wire layer would
// otherwise be free to mutate). Every nested pointer, slice, and map is copied.
func cloneEnvironment(e *cdriver.Environment) cdriver.Environment {
	out := *e
	out.Labels = copyLabels(e.Labels)
	out.Config = cloneConfig(&e.Config)

	return out
}

func cloneConfig(cfg *cdriver.EnvironmentConfig) cdriver.EnvironmentConfig {
	out := *cfg
	out.SoftwareConfig = cloneSoftware(cfg.SoftwareConfig)
	out.NodeConfig = cloneNode(cfg.NodeConfig)
	out.Other = cloneRawMap(cfg.Other)

	return out
}

func cloneSoftware(s *cdriver.SoftwareConfig) *cdriver.SoftwareConfig {
	if s == nil {
		return nil
	}

	out := *s
	out.AirflowConfigOverrides = copyLabels(s.AirflowConfigOverrides)
	out.PypiPackages = copyLabels(s.PypiPackages)
	out.EnvVariables = copyLabels(s.EnvVariables)

	return &out
}

func cloneNode(n *cdriver.NodeConfig) *cdriver.NodeConfig {
	if n == nil {
		return nil
	}

	out := *n
	out.Tags = append([]string(nil), n.Tags...)
	out.OauthScopes = append([]string(nil), n.OauthScopes...)

	return &out
}

// cloneRawMap deep-copies an opaque config passthrough map, copying each
// json.RawMessage's bytes so a mutation of one never aliases the stored copy.
func cloneRawMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}

	return out
}
