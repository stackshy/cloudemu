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
	case strings.HasPrefix(resource, arnRestAPIs) && !strings.Contains(strings.TrimPrefix(resource, arnRestAPIs), "/"):
		return m.restAPITagTarget(strings.TrimPrefix(resource, arnRestAPIs))
	case strings.HasPrefix(resource, arnClientCertificates):
		return m.certTagTarget(strings.TrimPrefix(resource, arnClientCertificates))
	default:
		return tagTarget{}, cerrors.Newf(cerrors.NotFound, "Invalid resource identifier specified in the ARN: %s", arn)
	}
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
