package apigateway

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// ARN resource paths the v1 tagging API addresses.
const (
	arnRestAPIs           = "/restapis/"
	arnClientCertificates = "/clientcertificates/"
)

// Resource kinds of the account-level tag targets (the first ARN path segment).
const (
	kindAPIKeys    = "apikeys"
	kindUsagePlans = "usageplans"
	kindDomains    = "domainnames"
	kindVpcLinks   = "vpclinks"
)

// arnScheme and arnService are the first and service fields of an API
// Gateway ARN.
const (
	arnScheme  = "arn"
	arnService = "apigateway"
)

// arnFields is the field count of arn:partition:apigateway:region::/path.
const arnFields = 6

// tagTarget is the tag map a tagging call reads or mutates, resolved from an
// ARN. apply runs fn on the live map under the owning lock.
type tagTarget struct {
	apply func(fn func(tags map[string]string) map[string]string) error
}

// resolveTagTarget maps an API Gateway ARN to the resource whose tags it
// names: a REST API or a client certificate.
func (m *Mock) resolveTagTarget(arn string) (tagTarget, error) {
	parts := strings.SplitN(arn, ":", arnFields)
	if len(parts) != arnFields || parts[0] != arnScheme || parts[2] != arnService {
		return tagTarget{}, cerrors.Newf(cerrors.InvalidArgument, "Invalid ARN specified in the request: %s", arn)
	}

	resource := parts[5]

	switch {
	case strings.HasPrefix(resource, arnRestAPIs):
		return m.restAPIOrStageTagTarget(strings.TrimPrefix(resource, arnRestAPIs), arn)
	case strings.HasPrefix(resource, arnClientCertificates):
		return m.certTagTarget(strings.TrimPrefix(resource, arnClientCertificates))
	}

	if t, ok := m.regionTagTarget(resource); ok {
		return t, nil
	}

	return tagTarget{}, cerrors.Newf(cerrors.NotFound, "Invalid resource identifier specified in the ARN: %s", arn)
}

// restAPIOrStageTagTarget resolves "{apiId}" or "{apiId}/stages/{stage}".
func (m *Mock) restAPIOrStageTagTarget(path, arn string) (tagTarget, error) {
	apiID, rest, hasRest := strings.Cut(path, "/")
	if !hasRest {
		return m.restAPITagTarget(apiID)
	}

	stage, ok := strings.CutPrefix(rest, "stages/")
	if !ok || stage == "" || strings.Contains(stage, "/") {
		return tagTarget{}, cerrors.Newf(cerrors.NotFound, "Invalid resource identifier specified in the ARN: %s", arn)
	}

	ad, err := m.getAPI(apiID)
	if err != nil {
		return tagTarget{}, err
	}

	return tagTarget{apply: func(fn func(map[string]string) map[string]string) error {
		ad.mu.Lock()
		defer ad.mu.Unlock()

		st, found := ad.stages[stage]
		if !found {
			return cerrors.Newf(cerrors.NotFound, "Invalid stage identifier specified %s", stage)
		}

		st.Tags = fn(st.Tags)

		return nil
	}}, nil
}

// regionTagTarget resolves the account-level resources that carry tags: API
// keys, usage plans, custom domain names and VPC links.
func (m *Mock) regionTagTarget(resource string) (tagTarget, bool) {
	kind, id, ok := strings.Cut(strings.TrimPrefix(resource, "/"), "/")
	if !ok || id == "" || strings.Contains(id, "/") {
		return tagTarget{}, false
	}

	switch kind {
	case kindAPIKeys, kindUsagePlans, kindDomains, kindVpcLinks:
	default:
		return tagTarget{}, false
	}

	return tagTarget{apply: func(fn func(map[string]string) map[string]string) error {
		m.regionMu.Lock()
		defer m.regionMu.Unlock()

		if !m.applyRegionTags(kind, id, fn) {
			return cerrors.Newf(cerrors.NotFound, "Invalid resource identifier specified: %s", resource)
		}

		return nil
	}}, true
}

// applyRegionTags runs fn on the tag map of the named resource and stores the
// result, reporting whether the resource exists. regionMu is held.
func (m *Mock) applyRegionTags(kind, id string, fn func(map[string]string) map[string]string) bool {
	switch kind {
	case kindAPIKeys:
		if r := m.keys[id]; r != nil {
			r.Tags = fn(r.Tags)

			return true
		}
	case kindUsagePlans:
		if r := m.plans[id]; r != nil {
			r.Tags = fn(r.Tags)

			return true
		}
	case kindDomains:
		if r := m.domains[id]; r != nil {
			r.Tags = fn(r.Tags)

			return true
		}
	case kindVpcLinks:
		if r := m.vpcLinks[id]; r != nil {
			r.Tags = fn(r.Tags)

			return true
		}
	}

	return false
}

func (m *Mock) restAPITagTarget(id string) (tagTarget, error) {
	ad, err := m.getAPI(id)
	if err != nil {
		return tagTarget{}, err
	}

	return tagTarget{apply: func(fn func(map[string]string) map[string]string) error {
		ad.mu.Lock()
		defer ad.mu.Unlock()

		ad.api.Tags = fn(ad.api.Tags)

		return nil
	}}, nil
}

func (m *Mock) certTagTarget(id string) (tagTarget, error) {
	return tagTarget{apply: func(fn func(map[string]string) map[string]string) error {
		m.regionMu.Lock()
		defer m.regionMu.Unlock()

		cc, ok := m.certs[id]
		if !ok {
			return cerrors.New(cerrors.NotFound, msgCertNotFound)
		}

		cc.Tags = fn(cc.Tags)

		return nil
	}}, nil
}

// TagResource adds or overwrites tags on the resource the ARN names.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	target, err := m.resolveTagTarget(arn)
	if err != nil {
		return err
	}

	return target.apply(func(cur map[string]string) map[string]string {
		if cur == nil {
			cur = make(map[string]string, len(tags))
		}

		for k, v := range tags {
			cur[k] = v
		}

		return cur
	})
}

// UntagResource removes tag keys from the resource the ARN names.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	target, err := m.resolveTagTarget(arn)
	if err != nil {
		return err
	}

	return target.apply(func(cur map[string]string) map[string]string {
		for _, k := range keys {
			delete(cur, k)
		}

		return cur
	})
}

// GetTags returns the tags of the resource the ARN names.
func (m *Mock) GetTags(_ context.Context, arn string) (map[string]string, error) {
	target, err := m.resolveTagTarget(arn)
	if err != nil {
		return nil, err
	}

	var out map[string]string

	err = target.apply(func(cur map[string]string) map[string]string {
		out = copyStrMap(cur)

		return cur
	})
	if out == nil && err == nil {
		out = map[string]string{}
	}

	return out, err
}
