package apprunner

import (
	"context"
	"strings"
)

// App Runner ARN resource-kind segments. The kind is the first path element of
// the ARN's resource part (arn:aws:apprunner:region:account:<kind>/...).
const (
	kindService     = "service"
	kindASC         = "autoscalingconfiguration"
	kindObserver    = "observabilityconfiguration"
	kindVpcConn     = "vpcconnector"
	kindVpcIngress  = "vpcingressconnection"
	kindConnection  = "connection"
	arnResourceSep  = ":"
	arnPathSegments = 6
)

// TagResource merges tags into the resource owning arn. The read-merge-write is
// performed atomically under the owning resource's single lock so concurrent
// tag updates can't lose each other.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	return m.withTags(arn, func(existing map[string]string) map[string]string {
		out := copyTags(existing)
		if out == nil {
			out = make(map[string]string, len(tags))
		}

		for k, v := range tags {
			out[k] = v
		}

		return out
	})
}

// UntagResource removes the given keys from the resource owning arn, atomically.
func (m *Mock) UntagResource(_ context.Context, arn string, tagKeys []string) error {
	return m.withTags(arn, func(existing map[string]string) map[string]string {
		out := copyTags(existing)
		for _, k := range tagKeys {
			delete(out, k)
		}

		return out
	})
}

// ListTagsForResource returns a deep copy of the resource's tags.
func (m *Mock) ListTagsForResource(_ context.Context, arn string) (map[string]string, error) {
	var result map[string]string

	err := m.withTags(arn, func(existing map[string]string) map[string]string {
		result = copyTags(existing)

		return existing
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// withTags resolves arn to its owning resource and applies fn to that resource's
// tag map under a SINGLE hold of the resource's lock (read-modify-write), so
// concurrent tag mutations don't lose updates. Unknown ARNs are
// ResourceNotFoundException (the exception the tag ops model).
func (m *Mock) withTags(arn string, fn func(map[string]string) map[string]string) error {
	kind, err := arnKind(arn)
	if err != nil {
		return err
	}

	switch kind {
	case kindService:
		return m.tagService(arn, fn)
	case kindConnection:
		return m.tagConnection(arn, fn)
	case kindVpcIngress:
		return m.tagVpcIngress(arn, fn)
	case kindASC:
		return m.tagASC(arn, fn)
	case kindObserver:
		return m.tagObs(arn, fn)
	case kindVpcConn:
		return m.tagVpcConnector(arn, fn)
	default:
		return notFound("unsupported App Runner resource ARN %q", arn)
	}
}

// arnKind extracts the resource-kind segment from an App Runner ARN.
func arnKind(arn string) (string, error) {
	parts := strings.SplitN(arn, arnResourceSep, arnPathSegments)
	if len(parts) < arnPathSegments || parts[2] != serviceSegment {
		return "", notFound("not an App Runner resource ARN: %q", arn)
	}

	resource := parts[arnPathSegments-1]

	kind, _, ok := strings.Cut(resource, "/")
	if !ok {
		return "", notFound("malformed App Runner resource ARN: %q", arn)
	}

	return kind, nil
}

func (m *Mock) tagService(arn string, fn func(map[string]string) map[string]string) error {
	sd, ok := m.services.Get(arn)
	if !ok {
		return notFound("no App Runner service found for ARN %q", arn)
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.svc.Tags = fn(sd.svc.Tags)

	return nil
}

func (m *Mock) tagConnection(arn string, fn func(map[string]string) map[string]string) error {
	cd, ok := m.connections.Get(arn)
	if !ok {
		return notFound("no connection found for ARN %q", arn)
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	cd.conn.Tags = fn(cd.conn.Tags)

	return nil
}

func (m *Mock) tagVpcIngress(arn string, fn func(map[string]string) map[string]string) error {
	vd, ok := m.vpcIngress.Get(arn)
	if !ok {
		return notFound("no VPC ingress connection found for ARN %q", arn)
	}

	vd.mu.Lock()
	defer vd.mu.Unlock()

	vd.vic.Tags = fn(vd.vic.Tags)

	return nil
}

func (m *Mock) tagVpcConnector(arn string, fn func(map[string]string) map[string]string) error {
	vd, ok := m.vpcConnectors.Get(arn)
	if !ok {
		return notFound("no VPC connector found for ARN %q", arn)
	}

	vd.mu.Lock()
	defer vd.mu.Unlock()

	vd.vc.Tags = fn(vd.vc.Tags)

	return nil
}

func (m *Mock) tagASC(arn string, fn func(map[string]string) map[string]string) error {
	m.ascMu.Lock()
	defer m.ascMu.Unlock()

	cfg, ok := m.ascByArn[arn]
	if !ok {
		return notFound("no auto scaling configuration found for ARN %q", arn)
	}

	cfg.Tags = fn(cfg.Tags)

	return nil
}

func (m *Mock) tagObs(arn string, fn func(map[string]string) map[string]string) error {
	m.obsMu.Lock()
	defer m.obsMu.Unlock()

	cfg, ok := m.obsByArn[arn]
	if !ok {
		return notFound("no observability configuration found for ARN %q", arn)
	}

	cfg.Tags = fn(cfg.Tags)

	return nil
}
