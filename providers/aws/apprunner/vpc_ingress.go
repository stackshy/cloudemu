package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

const ingressDomainIDLen = 10

// ingressARN mints the stable ARN of a VPC ingress connection:
// arn:aws:apprunner:<region>:<acct>:vpcingressconnection/<name>/<id>.
func (m *Mock) ingressARN(name, id string) string {
	return idgen.AWSARN(serviceName, m.opts.Region, m.opts.AccountID, "vpcingressconnection/"+name+"/"+id)
}

func copyIngress(c *driver.VpcIngressConnection) driver.VpcIngressConnection {
	out := *c
	out.Tags = copyTags(c.Tags)

	return out
}

// viewIngress copies a connection with its status overlaid by any settle window.
func (m *Mock) viewIngress(c *driver.VpcIngressConnection) driver.VpcIngressConnection {
	out := copyIngress(c)
	out.Status = m.settling.State(c.VpcIngressConnectionArn, m.opts.Clock.Now(), c.Status)

	return out
}

func validateIngressConfig(cfg driver.IngressVpcConfiguration) error {
	if cfg.VpcID == "" || cfg.VpcEndpointID == "" {
		return invalidRequest("IngressVpcConfiguration requires VpcId and VpcEndpointId")
	}

	return nil
}

// CreateVpcIngressConnection connects a private service to a VPC endpoint. The
// service must exist and be private (IsPubliclyAccessible false) and idle; the
// connection name must be unique among the account's connections. It is
// AVAILABLE at once, and PENDING_CREATION first under async settling.
func (m *Mock) CreateVpcIngressConnection(
	_ context.Context, in *driver.CreateVpcIngressConnectionInput,
) (*driver.VpcIngressConnection, error) {
	if err := validateResourceName("VpcIngressConnectionName", in.VpcIngressConnectionName); err != nil {
		return nil, err
	}

	if err := validateIngressConfig(in.IngressVpcConfiguration); err != nil {
		return nil, err
	}

	m.refMu.Lock()
	defer m.refMu.Unlock()

	svc, ok := m.services.Get(in.ServiceArn)
	if !ok {
		return nil, notFound("service %q does not exist", in.ServiceArn)
	}

	if err := m.requireIdle(&svc); err != nil {
		return nil, err
	}

	if n := svc.NetworkConfiguration; n == nil || n.IngressConfiguration == nil ||
		n.IngressConfiguration.IsPubliclyAccessible == nil || *n.IngressConfiguration.IsPubliclyAccessible {
		return nil, invalidRequest("a VPC ingress connection needs a service with private ingress (IsPubliclyAccessible false)")
	}

	if m.ingressNameTaken(in.VpcIngressConnectionName) {
		return nil, invalidRequest("a VPC ingress connection named " + in.VpcIngressConnectionName + " already exists")
	}

	id := newID()
	arn := m.ingressARN(in.VpcIngressConnectionName, id)

	conn := driver.VpcIngressConnection{
		VpcIngressConnectionName: in.VpcIngressConnectionName, VpcIngressConnectionArn: arn, ServiceArn: in.ServiceArn,
		AccountID: m.opts.AccountID, DomainName: id[:ingressDomainIDLen] + "." + m.opts.Region + ".awsapprunner.com",
		Status: driver.IngressStatusAvailable, IngressVpcConfiguration: in.IngressVpcConfiguration,
		CreatedAt: m.now(), Tags: copyTags(in.Tags),
	}

	m.ingress.Set(arn, conn)
	m.settling.Begin(arn, driver.IngressStatusPendingCreation, m.opts.Clock.Now(), m.opts.SettleDuration(settleWindow))

	out := m.viewIngress(&conn)

	return &out, nil
}

func (m *Mock) ingressNameTaken(name string) bool {
	all := m.ingress.SortedValues()
	for i := range all {
		if all[i].VpcIngressConnectionName == name {
			return true
		}
	}

	return false
}

// DescribeVpcIngressConnection returns the connection by ARN.
func (m *Mock) DescribeVpcIngressConnection(_ context.Context, arn string) (*driver.VpcIngressConnection, error) {
	c, ok := m.ingress.Get(arn)
	if !ok {
		return nil, notFound("VPC ingress connection %q does not exist", arn)
	}

	out := m.viewIngress(&c)

	return &out, nil
}

// UpdateVpcIngressConnection changes the VPC and endpoint of an AVAILABLE
// connection (PENDING_UPDATE first under async settling).
func (m *Mock) UpdateVpcIngressConnection(
	_ context.Context, arn string, cfg driver.IngressVpcConfiguration,
) (*driver.VpcIngressConnection, error) {
	if err := validateIngressConfig(cfg); err != nil {
		return nil, err
	}

	m.refMu.Lock()
	defer m.refMu.Unlock()

	c, ok := m.ingress.Get(arn)
	if !ok {
		return nil, notFound("VPC ingress connection %q does not exist", arn)
	}

	if status := m.settling.State(arn, m.opts.Clock.Now(), c.Status); status != driver.IngressStatusAvailable {
		return nil, invalidState("VPC ingress connection %q is %s and cannot be updated", arn, status)
	}

	c.IngressVpcConfiguration = cfg
	m.ingress.Set(arn, c)
	m.settling.Begin(arn, driver.IngressStatusPendingUpdate, m.opts.Clock.Now(), m.opts.SettleDuration(settleWindow))

	out := m.viewIngress(&c)

	return &out, nil
}

// DeleteVpcIngressConnection removes a connection and returns it with a DELETED
// status, so a subsequent describe 404s.
func (m *Mock) DeleteVpcIngressConnection(_ context.Context, arn string) (*driver.VpcIngressConnection, error) {
	m.refMu.Lock()
	defer m.refMu.Unlock()

	c, ok := m.ingress.Get(arn)
	if !ok {
		return nil, notFound("VPC ingress connection %q does not exist", arn)
	}

	if status := m.settling.State(arn, m.opts.Clock.Now(), c.Status); status != driver.IngressStatusAvailable {
		return nil, invalidState("VPC ingress connection %q is %s and cannot be deleted", arn, status)
	}

	m.ingress.Delete(arn)
	m.settling.Clear(arn)

	out := copyIngress(&c)
	out.Status = driver.IngressStatusDeleted
	out.DeletedAt = m.now()

	return &out, nil
}

// ListVpcIngressConnections returns a deterministic page of connection summaries
// ordered by ARN, optionally narrowed to a service and a VPC endpoint.
func (m *Mock) ListVpcIngressConnections(
	_ context.Context, filter driver.ListVpcIngressConnectionsFilter, page driver.Page,
) (items []driver.VpcIngressConnectionSummary, nextToken string, err error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}

	matched := []driver.VpcIngressConnectionSummary{}

	all := m.ingress.SortedValues()
	for i := range all {
		c := &all[i]
		if filter.ServiceArn != "" && c.ServiceArn != filter.ServiceArn {
			continue
		}

		if filter.VpcEndpointID != "" && c.IngressVpcConfiguration.VpcEndpointID != filter.VpcEndpointID {
			continue
		}

		matched = append(matched, driver.VpcIngressConnectionSummary{
			VpcIngressConnectionArn: c.VpcIngressConnectionArn, ServiceArn: c.ServiceArn,
		})
	}

	start, end, next := paginate(len(matched), page)

	return matched[start:end], next, nil
}

// ingressesOf returns the connections attached to a service.
func (m *Mock) ingressesOf(serviceArn string) []driver.VpcIngressConnection {
	out := []driver.VpcIngressConnection{}

	all := m.ingress.SortedValues()
	for i := range all {
		if all[i].ServiceArn == serviceArn {
			out = append(out, all[i])
		}
	}

	return out
}
