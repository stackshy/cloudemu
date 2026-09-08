package ecr

import (
	"sort"

	"github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
)

// copyReplication deep-copies a replication configuration so stored state and
// returned reads never share backing slices with the caller.
func copyReplication(src driver.ReplicationConfiguration) driver.ReplicationConfiguration {
	out := driver.ReplicationConfiguration{Rules: make([]driver.ReplicationRule, 0, len(src.Rules))}

	for i := range src.Rules {
		rule := &src.Rules[i]
		dst := driver.ReplicationRule{
			Destinations:      make([]driver.ReplicationDestination, len(rule.Destinations)),
			RepositoryFilters: make([]driver.ReplicationFilter, len(rule.RepositoryFilters)),
		}
		copy(dst.Destinations, rule.Destinations)
		copy(dst.RepositoryFilters, rule.RepositoryFilters)
		out.Rules = append(out.Rules, dst)
	}

	return out
}

// copyScanConfig deep-copies a registry scanning configuration.
func copyScanConfig(src driver.RegistryScanningConfiguration) driver.RegistryScanningConfiguration {
	out := driver.RegistryScanningConfiguration{
		ScanType: src.ScanType,
		Rules:    make([]driver.RegistryScanningRule, 0, len(src.Rules)),
	}

	for i := range src.Rules {
		rule := &src.Rules[i]
		dst := driver.RegistryScanningRule{
			ScanFrequency:     rule.ScanFrequency,
			RepositoryFilters: make([]driver.ScanningRepositoryFilter, len(rule.RepositoryFilters)),
		}
		copy(dst.RepositoryFilters, rule.RepositoryFilters)
		out.Rules = append(out.Rules, dst)
	}

	return out
}

// sortPullThrough orders rules by repository prefix for a stable read order.
func sortPullThrough(rules []driver.PullThroughCacheRule) {
	sort.Slice(rules, func(i, j int) bool { return rules[i].ECRRepositoryPrefix < rules[j].ECRRepositoryPrefix })
}
