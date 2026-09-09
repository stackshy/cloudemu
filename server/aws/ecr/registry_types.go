package ecr

import (
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// replicationConfigJSON models ECR's replicationConfiguration wire object,
// mapped to and from the AWS-specific driver.ReplicationConfiguration.
type replicationConfigJSON struct {
	Rules []replicationRuleJSON `json:"rules"`
}

type replicationRuleJSON struct {
	Destinations      []replicationDestJSON   `json:"destinations"`
	RepositoryFilters []replicationFilterJSON `json:"repositoryFilters,omitempty"`
}

type replicationDestJSON struct {
	Region     string `json:"region"`
	RegistryID string `json:"registryId"`
}

type replicationFilterJSON struct {
	Filter     string `json:"filter"`
	FilterType string `json:"filterType"`
}

// scanningConfigJSON models ECR's registryScanningConfiguration wire object.
type scanningConfigJSON struct {
	ScanType string             `json:"scanType"`
	Rules    []scanningRuleJSON `json:"rules"`
}

type scanningRuleJSON struct {
	ScanFrequency     string               `json:"scanFrequency"`
	RepositoryFilters []scanningFilterJSON `json:"repositoryFilters"`
}

type scanningFilterJSON struct {
	Filter     string `json:"filter"`
	FilterType string `json:"filterType"`
}

// pullThroughRuleJSON models ECR's pullThroughCacheRule wire object.
type pullThroughRuleJSON struct {
	ECRRepositoryPrefix string  `json:"ecrRepositoryPrefix"`
	UpstreamRegistryURL string  `json:"upstreamRegistryUrl,omitempty"`
	UpstreamRegistry    string  `json:"upstreamRegistry,omitempty"`
	CredentialARN       string  `json:"credentialArn,omitempty"`
	RegistryID          string  `json:"registryId,omitempty"`
	CreatedAt           float64 `json:"createdAt,omitempty"`
	UpdatedAt           float64 `json:"updatedAt,omitempty"`
}

func replicationToDriver(in replicationConfigJSON) crdriver.ReplicationConfiguration {
	out := crdriver.ReplicationConfiguration{Rules: make([]crdriver.ReplicationRule, 0, len(in.Rules))}

	for i := range in.Rules {
		src := &in.Rules[i]
		rule := crdriver.ReplicationRule{
			Destinations:      make([]crdriver.ReplicationDestination, 0, len(src.Destinations)),
			RepositoryFilters: make([]crdriver.ReplicationFilter, 0, len(src.RepositoryFilters)),
		}

		for _, d := range src.Destinations {
			rule.Destinations = append(rule.Destinations, crdriver.ReplicationDestination{Region: d.Region, RegistryID: d.RegistryID})
		}

		for _, f := range src.RepositoryFilters {
			rule.RepositoryFilters = append(rule.RepositoryFilters, crdriver.ReplicationFilter{Filter: f.Filter, FilterType: f.FilterType})
		}

		out.Rules = append(out.Rules, rule)
	}

	return out
}

func replicationToJSON(in crdriver.ReplicationConfiguration) replicationConfigJSON {
	out := replicationConfigJSON{Rules: make([]replicationRuleJSON, 0, len(in.Rules))}

	for i := range in.Rules {
		src := &in.Rules[i]
		rule := replicationRuleJSON{
			Destinations:      make([]replicationDestJSON, 0, len(src.Destinations)),
			RepositoryFilters: make([]replicationFilterJSON, 0, len(src.RepositoryFilters)),
		}

		for _, d := range src.Destinations {
			rule.Destinations = append(rule.Destinations, replicationDestJSON{Region: d.Region, RegistryID: d.RegistryID})
		}

		for _, f := range src.RepositoryFilters {
			rule.RepositoryFilters = append(rule.RepositoryFilters, replicationFilterJSON{Filter: f.Filter, FilterType: f.FilterType})
		}

		out.Rules = append(out.Rules, rule)
	}

	return out
}

func scanningToDriver(in scanningConfigJSON) crdriver.RegistryScanningConfiguration {
	out := crdriver.RegistryScanningConfiguration{
		ScanType: in.ScanType,
		Rules:    make([]crdriver.RegistryScanningRule, 0, len(in.Rules)),
	}

	for i := range in.Rules {
		src := &in.Rules[i]
		rule := crdriver.RegistryScanningRule{
			ScanFrequency:     src.ScanFrequency,
			RepositoryFilters: make([]crdriver.ScanningRepositoryFilter, 0, len(src.RepositoryFilters)),
		}

		for _, f := range src.RepositoryFilters {
			rule.RepositoryFilters = append(rule.RepositoryFilters, crdriver.ScanningRepositoryFilter{Filter: f.Filter, FilterType: f.FilterType})
		}

		out.Rules = append(out.Rules, rule)
	}

	return out
}

func scanningToJSON(in crdriver.RegistryScanningConfiguration) scanningConfigJSON {
	out := scanningConfigJSON{ScanType: in.ScanType, Rules: make([]scanningRuleJSON, 0, len(in.Rules))}

	for i := range in.Rules {
		src := &in.Rules[i]
		rule := scanningRuleJSON{
			ScanFrequency:     src.ScanFrequency,
			RepositoryFilters: make([]scanningFilterJSON, 0, len(src.RepositoryFilters)),
		}

		for _, f := range src.RepositoryFilters {
			rule.RepositoryFilters = append(rule.RepositoryFilters, scanningFilterJSON{Filter: f.Filter, FilterType: f.FilterType})
		}

		out.Rules = append(out.Rules, rule)
	}

	return out
}

// pullThroughToJSON renders a driver rule as its wire object, converting the
// stored RFC3339 timestamps to the epoch-seconds shape ECR uses on the wire.
func pullThroughToJSON(in *crdriver.PullThroughCacheRule) pullThroughRuleJSON {
	return pullThroughRuleJSON{
		ECRRepositoryPrefix: in.ECRRepositoryPrefix,
		UpstreamRegistryURL: in.UpstreamRegistryURL,
		UpstreamRegistry:    in.UpstreamRegistry,
		CredentialARN:       in.CredentialARN,
		RegistryID:          in.RegistryID,
		CreatedAt:           epochSeconds(in.CreatedAt),
		UpdatedAt:           epochSeconds(in.UpdatedAt),
	}
}
