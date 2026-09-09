package appsync

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

//nolint:gochecknoglobals // immutable validation set, read-only after init.
var validAuthTypes = map[string]bool{
	driver.AuthAPIKey:        true,
	driver.AuthAWSIAM:        true,
	driver.AuthCognito:       true,
	driver.AuthOpenIDConnect: true,
	driver.AuthLambda:        true,
}

// CreateGraphqlAPI provisions a new GraphQL API with a stable apiId, the two
// endpoint URIs, and an ARN. The owner is the account id and visibility/apiType
// default to GLOBAL/GRAPHQL when omitted.
func (m *Mock) CreateGraphqlAPI(_ context.Context, in *driver.CreateGraphqlAPIInput) (*driver.GraphqlAPI, error) {
	if in.Name == "" {
		return nil, badRequest("name is required")
	}

	if !validAuthTypes[in.AuthenticationType] {
		return nil, badRequest("authenticationType %q is not valid", in.AuthenticationType)
	}

	apiID := newAPIID()

	api := driver.GraphqlAPI{
		APIID:              apiID,
		Name:               in.Name,
		AuthenticationType: in.AuthenticationType,
		ARN:                m.apiARN(apiID),
		URIs:               m.urisFor(apiID),
		Owner:              m.opts.AccountID,
		Visibility:         defaultString(in.Visibility, driver.VisibilityGlobal),
		APIType:            defaultString(in.APIType, driver.APITypeGraphQL),
		XrayEnabled:        in.XrayEnabled != nil && *in.XrayEnabled,
		Tags:               copyTags(in.Tags),
		Extra:              copyExtra(in.Extra),
	}

	m.apis.Set(apiID, &apiData{
		api:      api,
		dataSrcs: map[string]driver.DataSource{},
		apiKeys:  map[string]driver.APIKey{},
	})

	return m.snapshotAPI(apiID), nil
}

// GetGraphqlAPI returns a copy of the API. The stored apiId, arn, uris, and
// owner are returned unchanged so repeated reads never drift.
func (m *Mock) GetGraphqlAPI(_ context.Context, apiID string) (*driver.GraphqlAPI, error) {
	ad, err := m.getAPI(apiID)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := copyAPI(&ad.api)

	return &out, nil
}

// UpdateGraphqlAPI replaces the mutable fields of an API while preserving the
// computed apiId, arn, uris, owner, visibility, and apiType.
func (m *Mock) UpdateGraphqlAPI(_ context.Context, in *driver.UpdateGraphqlAPIInput) (*driver.GraphqlAPI, error) {
	if in.Name == "" {
		return nil, badRequest("name is required")
	}

	if !validAuthTypes[in.AuthenticationType] {
		return nil, badRequest("authenticationType %q is not valid", in.AuthenticationType)
	}

	ad, err := m.getAPI(in.APIID)
	if err != nil {
		return nil, err
	}

	ad.mu.Lock()
	defer ad.mu.Unlock()

	ad.api.Name = in.Name
	ad.api.AuthenticationType = in.AuthenticationType
	ad.api.Extra = copyExtra(in.Extra)

	if in.XrayEnabled != nil {
		ad.api.XrayEnabled = *in.XrayEnabled
	}

	out := copyAPI(&ad.api)

	return &out, nil
}

// DeleteGraphqlAPI removes an API and all of its nested data sources and keys.
func (m *Mock) DeleteGraphqlAPI(_ context.Context, apiID string) error {
	if !m.apis.Delete(apiID) {
		return notFound("GraphQL API %s not found", apiID)
	}

	return nil
}

// ListGraphqlAPIs returns a deterministic, deep-copied page of the APIs.
func (m *Mock) ListGraphqlAPIs(_ context.Context, page driver.Page) ([]driver.GraphqlAPI, string, error) {
	ads := m.apis.SortedValues()

	all := make([]driver.GraphqlAPI, 0, len(ads))

	for _, ad := range ads {
		ad.mu.RLock()
		all = append(all, copyAPI(&ad.api))
		ad.mu.RUnlock()
	}

	start, end, next := paginate(len(all), page)

	return all[start:end], next, nil
}

// snapshotAPI returns a standalone copy of the stored API.
func (m *Mock) snapshotAPI(apiID string) *driver.GraphqlAPI {
	ad, ok := m.apis.Get(apiID)
	if !ok {
		return nil
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := copyAPI(&ad.api)

	return &out
}

// copyAPI returns an alias-free copy of an API so callers cannot mutate stored
// state through the result.
func copyAPI(a *driver.GraphqlAPI) driver.GraphqlAPI {
	out := *a
	out.Tags = copyTags(a.Tags)
	out.Extra = copyExtra(a.Extra)
	out.URIs = copyStringMap(a.URIs)

	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func defaultString(v, def string) string {
	if v == "" {
		return def
	}

	return v
}
