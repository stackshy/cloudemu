package kafka

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// kafkaVersion is one modeled Apache Kafka version with its lifecycle status.
type kafkaVersion struct {
	Version string
	Status  string
}

// modeledKafkaVersions returns the fixed set of Apache Kafka versions the
// emulator reports, oldest first. Deterministic and stable so paging and
// compatibility lookups are reproducible.
func modeledKafkaVersions() []kafkaVersion {
	return []kafkaVersion{
		{"2.8.1", "DEPRECATED"},
		{"3.4.0", "ACTIVE"},
		{"3.5.1", "ACTIVE"},
		{"3.6.0", "ACTIVE"},
		{"3.7.x", "ACTIVE"},
	}
}

// ListKafkaVersions returns the modeled Kafka-version set, paginated.
func (m *Mock) ListKafkaVersions(
	_ context.Context, page driver.Page,
) (versions []json.RawMessage, next string, err error) {
	versionSet := modeledKafkaVersions()

	all := make([]json.RawMessage, 0, len(versionSet))

	for _, v := range versionSet {
		b, mErr := json.Marshal(map[string]any{"version": v.Version, "status": v.Status})
		if mErr != nil {
			continue
		}

		all = append(all, b)
	}

	start, end, nextTok, err := m.paginate(len(all), page)
	if err != nil {
		return nil, "", err
	}

	return all[start:end], nextTok, nil
}

// GetCompatibleKafkaVersions returns, for the cluster's current Kafka version
// (or every modeled version when no cluster ARN is given), the set of newer
// modeled versions it can upgrade to. A given ARN must resolve to a cluster.
func (m *Mock) GetCompatibleKafkaVersions(
	_ context.Context, clusterARN string,
) ([]json.RawMessage, error) {
	sources := m.compatibilitySources(clusterARN)
	if clusterARN != "" && len(sources) == 0 {
		return nil, notFound("cluster not found: %s", clusterARN)
	}

	out := make([]json.RawMessage, 0, len(sources))

	for _, src := range sources {
		targets := compatibleTargets(src)

		b, err := json.Marshal(map[string]any{"sourceVersion": src, "targetVersions": targets})
		if err != nil {
			continue
		}

		out = append(out, b)
	}

	return out, nil
}

// compatibilitySources returns the source version(s) to report compatibility
// for: the cluster's version when an ARN is given, else every modeled version.
func (m *Mock) compatibilitySources(clusterARN string) []string {
	if clusterARN == "" {
		versionSet := modeledKafkaVersions()

		all := make([]string, 0, len(versionSet))
		for _, v := range versionSet {
			all = append(all, v.Version)
		}

		return all
	}

	cd, ok := m.clusters.Get(clusterARN)
	if !ok {
		return nil
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	return []string{cd.cluster.KafkaVersion}
}

// compatibleTargets returns every modeled version strictly newer than src, by
// the modeled ordering (oldest first).
func compatibleTargets(src string) []string {
	versionSet := modeledKafkaVersions()
	idx := -1

	for i, v := range versionSet {
		if v.Version == src {
			idx = i

			break
		}
	}

	if idx < 0 {
		return nil
	}

	targets := make([]string, 0, len(versionSet)-idx-1)
	for _, v := range versionSet[idx+1:] {
		targets = append(targets, v.Version)
	}

	return targets
}
