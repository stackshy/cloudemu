package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// PutConfigurationAggregator creates or updates an aggregator (upsert). Exactly
// one of AccountSources / OrganizationSource must be supplied.
//
//nolint:gocritic // agg taken by value to match the driver API.
func (m *Mock) PutConfigurationAggregator(
	_ context.Context, agg driver.ConfigurationAggregator,
) (driver.ConfigurationAggregator, error) {
	if agg.Name == "" {
		return driver.ConfigurationAggregator{}, invalidParameter("ConfigurationAggregatorName is required")
	}

	if (len(agg.AccountSources) == 0) == (agg.OrganizationSource == nil) {
		return driver.ConfigurationAggregator{},
			invalidParameter("exactly one of AccountAggregationSources or OrganizationAggregationSource is required")
	}

	now := m.now()
	agg.LastUpdatedTime = now
	agg.Tags = copyTags(agg.Tags)

	if existing, ok := m.aggregators.Get(agg.Name); ok {
		existing.mu.Lock()
		agg.Arn = existing.agg.Arn
		agg.CreationTime = existing.agg.CreationTime
		existing.agg = agg
		out := copyAggregator(&existing.agg)
		existing.mu.Unlock()

		return out, nil
	}

	agg.Arn = m.arn("config-aggregator/" + agg.Name)
	agg.CreationTime = now

	if !m.aggregators.SetIfAbsent(agg.Name, &aggData{agg: agg}) {
		return driver.ConfigurationAggregator{}, resourceInUse("aggregator %q already exists", agg.Name)
	}

	return copyAggregator(&agg), nil
}

func copyAggregator(a *driver.ConfigurationAggregator) driver.ConfigurationAggregator {
	out := *a
	out.Tags = copyTags(a.Tags)

	out.AccountSources = make([]driver.AccountAggregationSource, len(a.AccountSources))

	for i, s := range a.AccountSources {
		cp := s
		cp.AccountIDs = copyStrings(s.AccountIDs)
		cp.AwsRegions = copyStrings(s.AwsRegions)
		out.AccountSources[i] = cp
	}

	if a.OrganizationSource != nil {
		os := *a.OrganizationSource
		os.AwsRegions = copyStrings(a.OrganizationSource.AwsRegions)
		out.OrganizationSource = &os
	}

	return out
}

func (m *Mock) allAggregators() []driver.ConfigurationAggregator {
	keys := sortedKeys(m.aggregators.Keys())
	out := make([]driver.ConfigurationAggregator, 0, len(keys))

	for _, k := range keys {
		ad, ok := m.aggregators.Get(k)
		if !ok {
			continue
		}

		ad.mu.RLock()
		out = append(out, copyAggregator(&ad.agg))
		ad.mu.RUnlock()
	}

	return out
}

// DescribeConfigurationAggregators returns the named aggregators (all if empty).
func (m *Mock) DescribeConfigurationAggregators(
	_ context.Context, names []string, page driver.Page,
) ([]driver.ConfigurationAggregator, string, error) {
	for _, n := range names {
		if !m.aggregators.Has(n) {
			return nil, "", noSuchAggregator(n)
		}
	}

	filtered := filterByNames(m.allAggregators(),
		func(a driver.ConfigurationAggregator) string { return a.Name }, names)

	return paginate(filtered, page)
}

// DeleteConfigurationAggregator removes an aggregator.
func (m *Mock) DeleteConfigurationAggregator(_ context.Context, name string) error {
	if !m.aggregators.Delete(name) {
		return noSuchAggregator(name)
	}

	return nil
}

// DescribeConfigurationAggregatorSourcesStatus returns source sync status.
func (m *Mock) DescribeConfigurationAggregatorSourcesStatus(
	_ context.Context, name string, page driver.Page,
) ([]driver.ConfigurationAggregator, string, error) {
	if !m.aggregators.Has(name) {
		return nil, "", noSuchAggregator(name)
	}

	ad, _ := m.aggregators.Get(name)
	ad.mu.RLock()
	out := copyAggregator(&ad.agg)
	ad.mu.RUnlock()

	return paginate([]driver.ConfigurationAggregator{out}, page)
}

// PutAggregationAuthorization authorizes another account/region to aggregate
// this account. Idempotent on (account, region).
func (m *Mock) PutAggregationAuthorization(
	_ context.Context, authAccountID, authRegion string, tags map[string]string,
) (driver.AggregationAuthorization, error) {
	if authAccountID == "" || authRegion == "" {
		return driver.AggregationAuthorization{},
			invalidParameter("AuthorizedAccountId and AuthorizedAwsRegion are required")
	}

	m.authMu.Lock()
	defer m.authMu.Unlock()

	for i := range m.authorizations {
		a := &m.authorizations[i]
		if a.AuthorizedAccountID == authAccountID && a.AuthorizedAwsRegion == authRegion {
			a.Tags = copyTags(tags)

			return copyAuth(a), nil
		}
	}

	auth := driver.AggregationAuthorization{
		Arn:                 m.arn("aggregation-authorization/" + authAccountID + "/" + authRegion),
		AuthorizedAccountID: authAccountID,
		AuthorizedAwsRegion: authRegion,
		CreationTime:        m.now(),
		Tags:                copyTags(tags),
	}
	m.authorizations = append(m.authorizations, auth)

	return copyAuth(&auth), nil
}

func copyAuth(a *driver.AggregationAuthorization) driver.AggregationAuthorization {
	out := *a
	out.Tags = copyTags(a.Tags)

	return out
}

// DescribeAggregationAuthorizations lists all authorizations, paginated.
func (m *Mock) DescribeAggregationAuthorizations(
	_ context.Context, page driver.Page,
) (auths []driver.AggregationAuthorization, nextToken string, err error) {
	m.authMu.RLock()
	out := make([]driver.AggregationAuthorization, len(m.authorizations))

	for i := range m.authorizations {
		out[i] = copyAuth(&m.authorizations[i])
	}
	m.authMu.RUnlock()

	return paginate(out, page)
}

// DeleteAggregationAuthorization removes an authorization.
func (m *Mock) DeleteAggregationAuthorization(_ context.Context, authAccountID, authRegion string) error {
	m.authMu.Lock()
	defer m.authMu.Unlock()

	for i := range m.authorizations {
		if m.authorizations[i].AuthorizedAccountID == authAccountID &&
			m.authorizations[i].AuthorizedAwsRegion == authRegion {
			m.authorizations = append(m.authorizations[:i], m.authorizations[i+1:]...)

			return nil
		}
	}

	// Real Config treats deleting a non-existent authorization as a success.
	return nil
}

// DescribePendingAggregationRequests lists unaccepted authorization requests.
// Synthesized empty (no cross-account requests originate in the emulator).
func (*Mock) DescribePendingAggregationRequests(
	_ context.Context, page driver.Page,
) ([]driver.PendingAggregationRequest, string, error) {
	return paginate([]driver.PendingAggregationRequest{}, page)
}

// DeletePendingAggregationRequest removes a pending request (no-op success).
func (*Mock) DeletePendingAggregationRequest(_ context.Context, requesterAccountID, requesterRegion string) error {
	if requesterAccountID == "" || requesterRegion == "" {
		return invalidParameter("RequesterAccountId and RequesterAwsRegion are required")
	}

	return nil
}
