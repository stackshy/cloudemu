package apprunner

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// CreateVpcIngressConnection registers a VPC ingress connection. It comes up
// AVAILABLE immediately with a synthesized DomainName and is keyed by its
// server-minted ARN (SetIfAbsent enforces uniqueness). The service it targets
// must exist.
func (m *Mock) CreateVpcIngressConnection(
	_ context.Context, name, serviceArn string, ingressVpcConfig json.RawMessage, tags map[string]string,
) (*driver.VpcIngressConnection, error) {
	if name == "" {
		return nil, invalidRequest("VpcIngressConnectionName is required")
	}

	// CreateVpcIngressConnection does not model ResourceNotFoundException, so a
	// missing target service is an InvalidRequestException (not RNF).
	if _, ok := m.services.Get(serviceArn); !ok {
		return nil, invalidRequest("no App Runner service found for ARN %q", serviceArn)
	}

	arn := m.vpcIngressArn(name)
	vic := driver.VpcIngressConnection{
		Arn: arn, Name: name, Status: driver.VpcIngressStatusAvailable,
		AccountID:  m.opts.AccountID,
		DomainName: name + "." + m.opts.Region + ".vpc.awsapprunner.com",
		ServiceArn: serviceArn, IngressVpcConfiguration: copyRaw(ingressVpcConfig),
		CreatedAt: m.now(), Tags: copyTags(tags),
	}

	if !m.vpcIngress.SetIfAbsent(arn, &vpcIngressData{vic: vic}) {
		return nil, invalidRequest("VPC ingress connection ARN collision for %q", arn)
	}

	return copyVpcIngress(&vic), nil
}

func (m *Mock) DescribeVpcIngressConnection(_ context.Context, arn string) (*driver.VpcIngressConnection, error) {
	vd, ok := m.vpcIngress.Get(arn)
	if !ok {
		return nil, notFound("no VPC ingress connection found for ARN %q", arn)
	}

	vd.mu.RLock()
	defer vd.mu.RUnlock()

	return copyVpcIngress(&vd.vic), nil
}

// DeleteVpcIngressConnection marks a connection DELETED and stamps its deletion
// time.
func (m *Mock) DeleteVpcIngressConnection(_ context.Context, arn string) (*driver.VpcIngressConnection, error) {
	vd, ok := m.vpcIngress.Get(arn)
	if !ok {
		return nil, notFound("no VPC ingress connection found for ARN %q", arn)
	}

	vd.mu.Lock()
	defer vd.mu.Unlock()

	vd.vic.Status = driver.VpcIngressStatusDeleted
	if vd.vic.DeletedAt.IsZero() {
		vd.vic.DeletedAt = m.now()
	}

	return copyVpcIngress(&vd.vic), nil
}

// ListVpcIngressConnections lists connections, optionally filtered by owning
// service ARN and/or VPC endpoint id, paginated by ARN.
func (m *Mock) ListVpcIngressConnections(
	_ context.Context, serviceArn, vpcEndpointID, nextToken string, maxResults int32,
) ([]driver.VpcIngressConnection, string, error) {
	all := m.vpcIngress.SortedValues()
	out := make([]driver.VpcIngressConnection, 0, len(all))

	for _, vd := range all {
		vd.mu.RLock()
		if matchVpcIngress(&vd.vic, serviceArn, vpcEndpointID) {
			out = append(out, *copyVpcIngress(&vd.vic))
		}
		vd.mu.RUnlock()
	}

	return paginate(out, nextToken, maxResults, func(v driver.VpcIngressConnection) string { return v.Arn })
}

// matchVpcIngress reports whether a connection passes the optional service-ARN
// and VPC-endpoint-id filters.
func matchVpcIngress(vic *driver.VpcIngressConnection, serviceArn, vpcEndpointID string) bool {
	if serviceArn != "" && vic.ServiceArn != serviceArn {
		return false
	}

	if vpcEndpointID != "" && !ingressHasEndpoint(vic, vpcEndpointID) {
		return false
	}

	return true
}

// ingressHasEndpoint reports whether the stored ingress config names the given
// VPC endpoint id.
func ingressHasEndpoint(vic *driver.VpcIngressConnection, vpcEndpointID string) bool {
	var cfg struct {
		VpcEndpointID string `json:"VpcEndpointId"`
	}

	if err := json.Unmarshal(vic.IngressVpcConfiguration, &cfg); err != nil {
		return false
	}

	return cfg.VpcEndpointID == vpcEndpointID
}

// UpdateVpcIngressConnection replaces the stored ingress VPC configuration.
func (m *Mock) UpdateVpcIngressConnection(
	_ context.Context, arn string, ingressVpcConfig json.RawMessage,
) (*driver.VpcIngressConnection, error) {
	vd, ok := m.vpcIngress.Get(arn)
	if !ok {
		return nil, notFound("no VPC ingress connection found for ARN %q", arn)
	}

	vd.mu.Lock()
	defer vd.mu.Unlock()

	if vd.vic.Status == driver.VpcIngressStatusDeleted {
		return nil, invalidState("VPC ingress connection %q is deleted", arn)
	}

	if len(ingressVpcConfig) > 0 {
		vd.vic.IngressVpcConfiguration = copyRaw(ingressVpcConfig)
	}

	return copyVpcIngress(&vd.vic), nil
}

// copyVpcIngress deep-copies a connection, including its ingress config bytes.
func copyVpcIngress(v *driver.VpcIngressConnection) *driver.VpcIngressConnection {
	out := *v
	out.IngressVpcConfiguration = copyRaw(v.IngressVpcConfiguration)
	out.Tags = copyTags(v.Tags)

	return &out
}
