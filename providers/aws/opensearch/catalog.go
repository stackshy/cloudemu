package opensearch

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// supportedVersions is the synthesized catalog ListVersions/GetCompatibleVersions
// draw from. It is ordered newest-first, matching real AWS.
func supportedVersions() []string {
	return []string{
		"OpenSearch_2.17", "OpenSearch_2.15", "OpenSearch_2.13", "OpenSearch_2.11",
		"OpenSearch_2.9", "OpenSearch_2.7", "OpenSearch_2.5", "OpenSearch_2.3",
		"OpenSearch_1.3", "OpenSearch_1.2", "OpenSearch_1.1", "OpenSearch_1.0",
		"Elasticsearch_7.10", "Elasticsearch_7.9", "Elasticsearch_7.8",
		"Elasticsearch_7.7", "Elasticsearch_7.4", "Elasticsearch_7.1",
		"Elasticsearch_6.8", "Elasticsearch_6.7", "Elasticsearch_6.5",
		"Elasticsearch_6.4", "Elasticsearch_6.3", "Elasticsearch_6.2",
		"Elasticsearch_6.0", "Elasticsearch_5.6", "Elasticsearch_5.5",
		"Elasticsearch_5.3", "Elasticsearch_5.1",
	}
}

// ListVersions returns the paginated catalog of supported engine versions.
func (*Mock) ListVersions(_ context.Context, page driver.Page) (versions []string, next string, err error) {
	all := supportedVersions()
	start, end, next := paginate(len(all), page)

	return append([]string(nil), all[start:end]...), next, nil
}

// GetCompatibleVersions returns, for a source version (or every version when no
// domain is named), the versions it can upgrade to (all newer versions).
func (m *Mock) GetCompatibleVersions(_ context.Context, domainName string) (map[string][]string, error) {
	all := supportedVersions()

	if domainName != "" {
		dd, err := m.getDomain(domainName)
		if err != nil {
			return nil, err
		}

		dd.mu.RLock()
		src := dd.status.EngineVersion
		dd.mu.RUnlock()

		return map[string][]string{src: newerThan(all, src)}, nil
	}

	out := make(map[string][]string, len(all))
	for _, v := range all {
		out[v] = newerThan(all, v)
	}

	return out, nil
}

// newerThan returns the versions listed before src in the newest-first catalog
// (i.e. upgrade targets). If src is absent, returns nil.
func newerThan(all []string, src string) []string {
	for i, v := range all {
		if v == src {
			return append([]string(nil), all[:i]...)
		}
	}

	return nil
}

// DescribeInstanceTypeLimits returns synthesized limits for an instance type.
func (m *Mock) DescribeInstanceTypeLimits(_ context.Context,
	engineVersion, instanceType, domainName string,
) (map[string]json.RawMessage, error) {
	if domainName != "" {
		if _, err := m.getDomain(domainName); err != nil {
			return nil, err
		}
	}

	if engineVersion == "" || instanceType == "" {
		return nil, validation("EngineVersion and InstanceType are required")
	}

	limits := json.RawMessage(`{
		"data": {
			"InstanceLimits": {"InstanceCountLimits": {"MinimumInstanceCount": 1, "MaximumInstanceCount": 80}},
			"StorageTypes": [{"StorageTypeName": "ebs", "StorageSubTypeName": "gp3", "StorageTypeLimits": []}],
			"AdditionalLimits": []
		},
		"master": {
			"InstanceLimits": {"InstanceCountLimits": {"MinimumInstanceCount": 3, "MaximumInstanceCount": 5}},
			"AdditionalLimits": []
		}
	}`)

	return map[string]json.RawMessage{"LimitsByRole": limits}, nil
}

// ListInstanceTypeDetails returns a synthesized instance-type catalog.
func (*Mock) ListInstanceTypeDetails(_ context.Context, engineVersion string,
	page driver.Page,
) (details []map[string]json.RawMessage, next string, err error) {
	if engineVersion == "" {
		return nil, "", validation("EngineVersion is required")
	}

	all := instanceTypeCatalog()
	start, end, next := paginate(len(all), page)

	return append([]map[string]json.RawMessage(nil), all[start:end]...), next, nil
}

// instanceTypeCatalog returns the synthesized instance-type detail list.
func instanceTypeCatalog() []map[string]json.RawMessage {
	types := []string{
		"t3.small.search", "t3.medium.search", "m6g.large.search",
		"m6g.xlarge.search", "c6g.large.search", "r6g.large.search",
		"r6g.xlarge.search", "i3.large.search", "or1.medium.search",
	}

	out := make([]map[string]json.RawMessage, 0, len(types))
	for _, t := range types {
		out = append(out, map[string]json.RawMessage{
			"InstanceType":            rawString(t),
			"EncryptionEnabled":       json.RawMessage("true"),
			"CognitoEnabled":          json.RawMessage("true"),
			"AppLogsEnabled":          json.RawMessage("true"),
			"AdvancedSecurityEnabled": json.RawMessage("true"),
			"WarmEnabled":             json.RawMessage("true"),
			"InstanceRole":            json.RawMessage(`["data","master"]`),
			"AvailabilityZones":       json.RawMessage(`["` + "" + `"]`),
		})
	}

	return out
}
