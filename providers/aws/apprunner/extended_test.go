package apprunner_test

import (
	"context"
	"fmt"
	"github.com/stackshy/cloudemu/v2/services/scope"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/apprunner"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatch"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs"
	"github.com/stackshy/cloudemu/v2/providers/aws/vpc"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

var bg = context.Background()

func namedService(name string) *driver.CreateServiceInput {
	in := sampleService()
	in.ServiceName = name

	return in
}

func mustNamed(t *testing.T, m *apprunner.Mock, name string) *driver.Service {
	t.Helper()

	res, err := m.CreateService(bg, namedService(name))
	requireNoError(t, err)

	return res.Service
}

func newAsyncMock() (*apprunner.Mock, *config.FakeClock) {
	clk := config.NewFakeClock(time.Unix(1_700_000_000, 0))

	return apprunner.New(config.NewOptions(config.WithClock(clk), config.WithAsyncSettle())), clk
}

func privateService(name string) *driver.CreateServiceInput {
	in := namedService(name)
	in.NetworkConfiguration = &driver.NetworkConfiguration{IngressConfiguration: &driver.IngressConfiguration{IsPubliclyAccessible: boolPtr(false)}}

	return in
}

func TestServiceInputValidation(t *testing.T) {
	m := newMock()

	cases := map[string]func(*driver.CreateServiceInput){
		"name too short": func(c *driver.CreateServiceInput) { c.ServiceName = "abc" },
		"name too long":  func(c *driver.CreateServiceInput) { c.ServiceName = strings.Repeat("a", 41) },
		"name bad lead":  func(c *driver.CreateServiceInput) { c.ServiceName = "-abcd" },
		"name bad chars": func(c *driver.CreateServiceInput) { c.ServiceName = "my app" },
		"no source":      func(c *driver.CreateServiceInput) { c.SourceConfiguration = &driver.SourceConfiguration{} },
		"nil source":     func(c *driver.CreateServiceInput) { c.SourceConfiguration = nil },
		"both sources": func(c *driver.CreateServiceInput) {
			c.SourceConfiguration.CodeRepository = &driver.CodeRepository{RepositoryURL: "u"}
		},
		"bad image type": func(c *driver.CreateServiceInput) {
			c.SourceConfiguration.ImageRepository.ImageRepositoryType = "DOCKERHUB"
		},
		"empty image id": func(c *driver.CreateServiceInput) { c.SourceConfiguration.ImageRepository.ImageIdentifier = "" },
		"bad cpu":        func(c *driver.CreateServiceInput) { c.InstanceConfiguration.CPU = "3 vCPU" },
		"bad memory":     func(c *driver.CreateServiceInput) { c.InstanceConfiguration.Memory = "5 GB" },
		"bad health protocol": func(c *driver.CreateServiceInput) {
			c.HealthCheckConfiguration = &driver.HealthCheckConfiguration{Protocol: "UDP"}
		},
		"health interval 0": func(c *driver.CreateServiceInput) {
			c.HealthCheckConfiguration = &driver.HealthCheckConfiguration{Interval: int32Ptr(0)}
		},
		"health timeout 21": func(c *driver.CreateServiceInput) {
			c.HealthCheckConfiguration = &driver.HealthCheckConfiguration{Timeout: int32Ptr(21)}
		},
		"bad egress type": func(c *driver.CreateServiceInput) {
			c.NetworkConfiguration = &driver.NetworkConfiguration{EgressConfiguration: &driver.EgressConfiguration{EgressType: "NAT"}}
		},
		"vpc egress no conn": func(c *driver.CreateServiceInput) {
			c.NetworkConfiguration = &driver.NetworkConfiguration{EgressConfiguration: &driver.EgressConfiguration{EgressType: "VPC"}}
		},
		"vpc egress unknown": func(c *driver.CreateServiceInput) {
			c.NetworkConfiguration = &driver.NetworkConfiguration{EgressConfiguration: &driver.EgressConfiguration{EgressType: "VPC", VpcConnectorArn: "arn:aws:apprunner:us-east-1:123456789012:vpcconnector/x/1/y"}}
		},
		"bad ip type": func(c *driver.CreateServiceInput) {
			c.NetworkConfiguration = &driver.NetworkConfiguration{IPAddressType: "IPV6"}
		},
		"unknown autoscaling": func(c *driver.CreateServiceInput) {
			c.AutoScalingConfigurationArn = "arn:aws:apprunner:us-east-1:123456789012:autoscalingconfiguration/nosuch/1/id"
		},
		"unknown observability": func(c *driver.CreateServiceInput) {
			c.ObservabilityConfiguration = &driver.ServiceObservabilityConfiguration{ObservabilityConfigurationArn: "arn:aws:apprunner:us-east-1:123456789012:observabilityconfiguration/nosuch/1/id"}
		},
	}

	for name, mutate := range cases {
		in := sampleService()
		mutate(in)

		_, err := m.CreateService(bg, in)
		if err == nil {
			t.Fatalf("%s: expected InvalidRequestException, got success", name)
		}

		requireInvalidRequest(t, err)
	}

	svcs, _, _ := m.ListServices(bg, driver.Page{})
	if len(svcs) != 0 {
		t.Fatalf("rejected creates stored %d services", len(svcs))
	}
}

func int32Ptr(n int32) *int32 { return &n }

func TestServiceDefaultsArePopulated(t *testing.T) {
	m := newMock()
	in := sampleService()
	in.InstanceConfiguration = nil
	in.HealthCheckConfiguration = nil
	in.ObservabilityConfiguration = nil

	res, err := m.CreateService(bg, in)
	requireNoError(t, err)

	svc := res.Service
	if svc.InstanceConfiguration.CPU != "1024" || svc.InstanceConfiguration.Memory != "2048" {
		t.Fatalf("instance defaults: %+v", svc.InstanceConfiguration)
	}

	h := svc.HealthCheckConfiguration
	if h.Protocol != "TCP" || h.Path != "/" || *h.Interval != 5 || *h.Timeout != 2 || *h.HealthyThreshold != 1 || *h.UnhealthyThreshold != 5 {
		t.Fatalf("health defaults: %+v", h)
	}

	if o := svc.ObservabilityConfiguration; o == nil || o.ObservabilityEnabled == nil || *o.ObservabilityEnabled {
		t.Fatalf("observability default must be enabled=false, got %+v", o)
	}

	// Supplied values are kept, unset ones default.
	in2 := namedService("second-svc")
	in2.InstanceConfiguration = &driver.InstanceConfiguration{CPU: "2 vCPU"}
	in2.HealthCheckConfiguration = &driver.HealthCheckConfiguration{Protocol: "HTTP", Path: "/health", Interval: int32Ptr(10)}

	res2, err := m.CreateService(bg, in2)
	requireNoError(t, err)
	assertStr(t, res2.Service.InstanceConfiguration.CPU, "2 vCPU")
	assertStr(t, res2.Service.InstanceConfiguration.Memory, "2048")
	assertStr(t, res2.Service.HealthCheckConfiguration.Path, "/health")

	if *res2.Service.HealthCheckConfiguration.Interval != 10 || *res2.Service.HealthCheckConfiguration.Timeout != 2 {
		t.Fatalf("partial health block: %+v", res2.Service.HealthCheckConfiguration)
	}
}

func assertStr(t *testing.T, got, want string) {
	t.Helper()

	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDuplicateServiceNameRejectedAndFreedByDelete(t *testing.T) {
	m := newMock()
	svc := mustNamed(t, m, "my-app")

	_, err := m.CreateService(bg, namedService("my-app"))
	requireInvalidRequest(t, err)

	_, err = m.DeleteService(bg, svc.ServiceArn)
	requireNoError(t, err)

	mustNamed(t, m, "my-app")
}

func TestParallelCreatesOfOneServiceNameCreateOne(t *testing.T) {
	m := newMock()

	var wg sync.WaitGroup

	for range 50 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, _ = m.CreateService(bg, namedService("same-name"))
		}()
	}

	wg.Wait()

	svcs, _, _ := m.ListServices(bg, driver.Page{})
	if len(svcs) != 1 {
		t.Fatalf("50 parallel creates of one name made %d services", len(svcs))
	}
}

func TestAutoScalingValidationAndPartialArns(t *testing.T) {
	m := newMock()

	bad := []driver.CreateAutoScalingConfigurationInput{
		{AutoScalingConfigurationName: "abc"},
		{AutoScalingConfigurationName: strings.Repeat("a", 33)},
		{AutoScalingConfigurationName: "-bad-name"},
		{AutoScalingConfigurationName: "good-name", MaxConcurrency: int32Ptr(0)},
		{AutoScalingConfigurationName: "good-name", MaxConcurrency: int32Ptr(-5)},
		{AutoScalingConfigurationName: "good-name", MaxConcurrency: int32Ptr(201)},
		{AutoScalingConfigurationName: "good-name", MinSize: int32Ptr(0)},
		{AutoScalingConfigurationName: "good-name", MinSize: int32Ptr(26)},
		{AutoScalingConfigurationName: "good-name", MaxSize: int32Ptr(0)},
		{AutoScalingConfigurationName: "good-name", MinSize: int32Ptr(10), MaxSize: int32Ptr(5)},
	}

	for i := range bad {
		_, err := m.CreateAutoScalingConfiguration(bg, &bad[i])
		requireInvalidRequest(t, err)
	}

	r1, err := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{
		AutoScalingConfigurationName: "good-name", MaxConcurrency: int32Ptr(200), MinSize: int32Ptr(5), MaxSize: int32Ptr(5),
	})
	requireNoError(t, err)

	r2, err := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "good-name"})
	requireNoError(t, err)

	base := strings.Join(strings.Split(r2.AutoScalingConfigurationArn, "/")[:2], "/") // .../autoscalingconfiguration/good-name

	// A name-only ARN means the latest revision; name/revision pins one.
	in := namedService("by-name")
	in.AutoScalingConfigurationArn = base
	res, err := m.CreateService(bg, in)
	requireNoError(t, err)
	assertStr(t, res.Service.AutoScalingConfigurationSummary.AutoScalingConfigurationArn, r2.AutoScalingConfigurationArn)

	in = namedService("by-revision")
	in.AutoScalingConfigurationArn = base + "/1"
	res, err = m.CreateService(bg, in)
	requireNoError(t, err)
	assertStr(t, res.Service.AutoScalingConfigurationSummary.AutoScalingConfigurationArn, r1.AutoScalingConfigurationArn)

	got, err := m.DescribeAutoScalingConfiguration(bg, base)
	requireNoError(t, err)
	assertStr(t, got.AutoScalingConfigurationArn, r2.AutoScalingConfigurationArn)

	svcs, _, err := m.ListServicesForAutoScalingConfiguration(bg, r1.AutoScalingConfigurationArn, driver.Page{})
	requireNoError(t, err)

	if len(svcs) != 1 || svcs[0] != res.Service.ServiceArn {
		t.Fatalf("ListServicesForAutoScalingConfiguration = %v", svcs)
	}

	_, _, err = m.ListServicesForAutoScalingConfiguration(bg, "arn:aws:apprunner:us-east-1:123456789012:autoscalingconfiguration/nosuch/1/x", driver.Page{})
	assertException(t, err, driver.ExResourceNotFound)
}

func TestUpdateDefaultAutoScalingConfiguration(t *testing.T) {
	m := newMock()

	cfg, err := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "custom-cfg"})
	requireNoError(t, err)

	seeded := mustNamed(t, m, "before-move").AutoScalingConfigurationSummary.AutoScalingConfigurationArn

	moved, err := m.UpdateDefaultAutoScalingConfiguration(bg, cfg.AutoScalingConfigurationArn)
	requireNoError(t, err)

	if !moved.IsDefault || moved.AutoScalingConfigurationArn != cfg.AutoScalingConfigurationArn {
		t.Fatalf("unexpected result: %+v", moved)
	}

	// New services without an explicit configuration use the new default.
	after := mustNamed(t, m, "after-move")
	assertStr(t, after.AutoScalingConfigurationSummary.AutoScalingConfigurationArn, cfg.AutoScalingConfigurationArn)

	old, err := m.DescribeAutoScalingConfiguration(bg, seeded)
	requireNoError(t, err)

	if old.IsDefault {
		t.Fatalf("the previous default must no longer be the default")
	}

	// The new default cannot be deleted; the old one can once nothing uses it.
	_, err = m.DeleteAutoScalingConfiguration(bg, cfg.AutoScalingConfigurationArn)
	requireInvalidRequest(t, err)

	_, err = m.DeleteAutoScalingConfiguration(bg, seeded)
	requireInvalidRequest(t, err) // "before-move" still uses it

	// Partial ARN (name) moves the default back to the latest revision.
	_, err = m.UpdateDefaultAutoScalingConfiguration(bg, strings.Join(strings.Split(cfg.AutoScalingConfigurationArn, "/")[:2], "/"))
	requireNoError(t, err)

	_, err = m.UpdateDefaultAutoScalingConfiguration(bg, "arn:aws:apprunner:us-east-1:123456789012:autoscalingconfiguration/nosuch/1/x")
	assertException(t, err, driver.ExResourceNotFound)

	_, err = m.UpdateDefaultAutoScalingConfiguration(bg, "")
	requireInvalidRequest(t, err)
}

func TestListPaginationRules(t *testing.T) {
	m := newMock()

	for i := range 30 {
		_, err := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: fmt.Sprintf("cfg-%02d", i)})
		requireNoError(t, err)
	}

	// No MaxResults: everything in one page (the default configuration is also listed).
	all, next, err := m.ListAutoScalingConfigurations(bg, "", true, driver.Page{})
	requireNoError(t, err)

	if len(all) != 31 || next != "" {
		t.Fatalf("no MaxResults must return all results in one response, got %d next=%q", len(all), next)
	}

	// With MaxResults the pages walk the list.
	seen, token := 0, ""

	for {
		page, n, perr := m.ListAutoScalingConfigurations(bg, "", true, driver.Page{MaxResults: 7, NextToken: token})
		requireNoError(t, perr)

		seen += len(page)

		if n == "" {
			break
		}

		token = n
	}

	if seen != 31 {
		t.Fatalf("paged walk saw %d results, want 31", seen)
	}

	for _, bad := range []int32{-1, 101} {
		_, _, err = m.ListAutoScalingConfigurations(bg, "", true, driver.Page{MaxResults: bad})
		requireInvalidRequest(t, err)

		_, _, err = m.ListServices(bg, driver.Page{MaxResults: bad})
		requireInvalidRequest(t, err)
	}
}

func TestAsyncSettlingServiceStatusAndInvalidState(t *testing.T) {
	m, clk := newAsyncMock()

	res, err := m.CreateService(bg, namedService("async-app"))
	requireNoError(t, err)
	assertStr(t, res.Service.Status, driver.StatusOperationInProgress)

	arn := res.Service.ServiceArn

	got, _ := m.DescribeService(bg, arn)
	assertStr(t, got.Status, driver.StatusOperationInProgress)

	ops, _, _ := m.ListOperations(bg, arn, driver.Page{})
	assertStr(t, ops[0].Status, driver.OpStatusInProgress)

	// While applying, every other operation is an InvalidStateException.
	_, err = m.PauseService(bg, arn)
	assertException(t, err, driver.ExInvalidState)

	_, err = m.UpdateService(bg, &driver.UpdateServiceInput{ServiceArn: arn})
	assertException(t, err, driver.ExInvalidState)

	_, err = m.StartDeployment(bg, arn)
	assertException(t, err, driver.ExInvalidState)

	_, err = m.DeleteService(bg, arn)
	assertException(t, err, driver.ExInvalidState)

	clk.Advance(time.Minute)

	got, _ = m.DescribeService(bg, arn)
	assertStr(t, got.Status, driver.StatusRunning)

	ops, _, _ = m.ListOperations(bg, arn, driver.Page{})
	assertStr(t, ops[0].Status, driver.OpStatusSucceeded)

	up, err := m.UpdateService(bg, &driver.UpdateServiceInput{ServiceArn: arn, InstanceConfiguration: &driver.InstanceConfiguration{CPU: "2048"}})
	requireNoError(t, err)
	assertStr(t, up.Service.Status, driver.StatusOperationInProgress)
	assertStr(t, up.Service.InstanceConfiguration.CPU, "2048")
	assertStr(t, up.Service.InstanceConfiguration.Memory, "2048")

	clk.Advance(time.Minute)

	_, err = m.PauseService(bg, arn)
	requireNoError(t, err)
	clk.Advance(time.Minute)

	paused, _ := m.DescribeService(bg, arn)
	assertStr(t, paused.Status, driver.StatusPaused)
}

func TestUpdateServiceValidation(t *testing.T) {
	m := newMock()
	svc := mustNamed(t, m, "update-me")

	bad := []driver.UpdateServiceInput{
		{SourceConfiguration: &driver.SourceConfiguration{}},
		{InstanceConfiguration: &driver.InstanceConfiguration{Memory: "7 GB"}},
		{HealthCheckConfiguration: &driver.HealthCheckConfiguration{Protocol: "ICMP"}},
		{NetworkConfiguration: &driver.NetworkConfiguration{IPAddressType: "IPV9"}},
		{AutoScalingConfigurationArn: "arn:aws:apprunner:us-east-1:123456789012:autoscalingconfiguration/ghost/1/x"},
	}

	for i := range bad {
		bad[i].ServiceArn = svc.ServiceArn

		_, err := m.UpdateService(bg, &bad[i])
		requireInvalidRequest(t, err)
	}

	got, _ := m.DescribeService(bg, svc.ServiceArn)
	assertStr(t, got.InstanceConfiguration.Memory, "2048")
}

func TestVpcIngressConnectionLifecycle(t *testing.T) {
	m := newMock()
	public := mustNamed(t, m, "public-app")

	res, err := m.CreateService(bg, privateService("private-app"))
	requireNoError(t, err)

	cfg := driver.IngressVpcConfiguration{VpcID: "vpc-1a2b3c4d", VpcEndpointID: "vpce-1a2b3c4d"}

	// A public service cannot take a VPC ingress connection.
	_, err = m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-1", ServiceArn: public.ServiceArn, IngressVpcConfiguration: cfg,
	})
	requireInvalidRequest(t, err)

	_, err = m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-1", ServiceArn: "arn:aws:apprunner:us-east-1:123456789012:service/ghost/abc", IngressVpcConfiguration: cfg,
	})
	assertException(t, err, driver.ExResourceNotFound)

	for _, in := range []driver.CreateVpcIngressConnectionInput{
		{VpcIngressConnectionName: "abc", ServiceArn: res.Service.ServiceArn, IngressVpcConfiguration: cfg},
		{VpcIngressConnectionName: "ingress-1", ServiceArn: res.Service.ServiceArn},
	} {
		_, err = m.CreateVpcIngressConnection(bg, &in)
		requireInvalidRequest(t, err)
	}

	conn, err := m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-1", ServiceArn: res.Service.ServiceArn, IngressVpcConfiguration: cfg,
		Tags: []driver.Tag{{Key: "env", Value: "dev"}},
	})
	requireNoError(t, err)
	assertStr(t, conn.Status, driver.IngressStatusAvailable)

	if !strings.Contains(conn.VpcIngressConnectionArn, ":vpcingressconnection/ingress-1/") || conn.DomainName == "" || conn.AccountID == "" {
		t.Fatalf("unexpected connection: %+v", conn)
	}

	_, err = m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-1", ServiceArn: res.Service.ServiceArn, IngressVpcConfiguration: cfg,
	})
	requireInvalidRequest(t, err)

	got, err := m.DescribeVpcIngressConnection(bg, conn.VpcIngressConnectionArn)
	requireNoError(t, err)
	assertStr(t, got.IngressVpcConfiguration.VpcEndpointID, "vpce-1a2b3c4d")

	tags, err := m.ListTagsForResource(bg, conn.VpcIngressConnectionArn)
	requireNoError(t, err)

	if len(tags) != 1 {
		t.Fatalf("ingress connection must be taggable, got %v", tags)
	}

	updated, err := m.UpdateVpcIngressConnection(bg, conn.VpcIngressConnectionArn, driver.IngressVpcConfiguration{VpcID: "vpc-9", VpcEndpointID: "vpce-9"})
	requireNoError(t, err)
	assertStr(t, updated.IngressVpcConfiguration.VpcID, "vpc-9")

	list, _, err := m.ListVpcIngressConnections(bg, driver.ListVpcIngressConnectionsFilter{ServiceArn: res.Service.ServiceArn}, driver.Page{})
	requireNoError(t, err)

	if len(list) != 1 || list[0].VpcIngressConnectionArn != conn.VpcIngressConnectionArn {
		t.Fatalf("unexpected list: %+v", list)
	}

	none, _, _ := m.ListVpcIngressConnections(bg, driver.ListVpcIngressConnectionsFilter{VpcEndpointID: "vpce-1a2b3c4d"}, driver.Page{})
	if len(none) != 0 {
		t.Fatalf("filter by the old endpoint must find nothing after the update")
	}

	del, err := m.DeleteVpcIngressConnection(bg, conn.VpcIngressConnectionArn)
	requireNoError(t, err)
	assertStr(t, del.Status, driver.IngressStatusDeleted)

	_, err = m.DescribeVpcIngressConnection(bg, conn.VpcIngressConnectionArn)
	assertException(t, err, driver.ExResourceNotFound)

	// Deleting the service removes its connections.
	conn2, err := m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-2", ServiceArn: res.Service.ServiceArn, IngressVpcConfiguration: cfg,
	})
	requireNoError(t, err)

	_, err = m.DeleteService(bg, res.Service.ServiceArn)
	requireNoError(t, err)

	_, err = m.DescribeVpcIngressConnection(bg, conn2.VpcIngressConnectionArn)
	assertException(t, err, driver.ExResourceNotFound)
}

func TestVpcIngressConnectionAsyncStates(t *testing.T) {
	m, clk := newAsyncMock()

	res, err := m.CreateService(bg, privateService("async-private"))
	requireNoError(t, err)
	clk.Advance(time.Minute)

	conn, err := m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-a", ServiceArn: res.Service.ServiceArn,
		IngressVpcConfiguration: driver.IngressVpcConfiguration{VpcID: "vpc-1", VpcEndpointID: "vpce-1"},
	})
	requireNoError(t, err)
	assertStr(t, conn.Status, driver.IngressStatusPendingCreation)

	cfg := driver.IngressVpcConfiguration{VpcID: "vpc-2", VpcEndpointID: "vpce-2"}

	_, err = m.UpdateVpcIngressConnection(bg, conn.VpcIngressConnectionArn, cfg)
	assertException(t, err, driver.ExInvalidState)

	_, err = m.DeleteVpcIngressConnection(bg, conn.VpcIngressConnectionArn)
	assertException(t, err, driver.ExInvalidState)

	clk.Advance(time.Minute)

	up, err := m.UpdateVpcIngressConnection(bg, conn.VpcIngressConnectionArn, cfg)
	requireNoError(t, err)
	assertStr(t, up.Status, driver.IngressStatusPendingUpdate)
}

func TestCustomDomains(t *testing.T) {
	m := newMock()
	svc := mustNamed(t, m, "domain-app")
	other := mustNamed(t, m, "other-app")

	res, err := m.AssociateCustomDomain(bg, svc.ServiceArn, "example.com", nil)
	requireNoError(t, err)

	if !res.CustomDomain.EnableWWWSubdomain || res.CustomDomain.Status != driver.DomainStatusPendingCertificateDNSValidate ||
		res.DNSTarget != svc.ServiceURL || len(res.CustomDomain.CertificateValidationRecords) != 2 {
		t.Fatalf("unexpected association: %+v %+v", res, res.CustomDomain)
	}

	for _, r := range res.CustomDomain.CertificateValidationRecords {
		if r.Type != "CNAME" || r.Status != "PENDING_VALIDATION" || !strings.HasSuffix(r.Value, ".acm-validations.aws.") {
			t.Fatalf("unexpected validation record %+v", r)
		}
	}

	// The same domain cannot go to the same or another service.
	_, err = m.AssociateCustomDomain(bg, svc.ServiceArn, "example.com", nil)
	requireInvalidRequest(t, err)

	_, err = m.AssociateCustomDomain(bg, other.ServiceArn, "example.com", nil)
	requireInvalidRequest(t, err)

	for _, bad := range []string{"", "bad domain", "under_score.com", strings.Repeat("a", 256)} {
		_, err = m.AssociateCustomDomain(bg, svc.ServiceArn, bad, nil)
		requireInvalidRequest(t, err)
	}

	_, err = m.AssociateCustomDomain(bg, "arn:aws:apprunner:us-east-1:123456789012:service/ghost/abc", "x.com", nil)
	assertException(t, err, driver.ExResourceNotFound)

	noWWW := false
	res, err = m.AssociateCustomDomain(bg, svc.ServiceArn, "*.example.org", &noWWW)
	requireNoError(t, err)

	if res.CustomDomain.EnableWWWSubdomain || len(res.CustomDomain.CertificateValidationRecords) != 1 {
		t.Fatalf("EnableWWWSubdomain=false must not add a www record: %+v", res.CustomDomain)
	}

	desc, next, err := m.DescribeCustomDomains(bg, svc.ServiceArn, driver.Page{})
	requireNoError(t, err)

	if len(desc.CustomDomains) != 2 || next != "" || desc.CustomDomains[0].DomainName != "*.example.org" {
		t.Fatalf("unexpected describe: %+v", desc.CustomDomains)
	}

	page, next, _ := m.DescribeCustomDomains(bg, svc.ServiceArn, driver.Page{MaxResults: 1})
	if len(page.CustomDomains) != 1 || next == "" {
		t.Fatalf("paging: %+v next=%q", page.CustomDomains, next)
	}

	gone, err := m.DisassociateCustomDomain(bg, svc.ServiceArn, "example.com")
	requireNoError(t, err)
	assertStr(t, gone.CustomDomain.Status, driver.DomainStatusDeleting)

	_, err = m.DisassociateCustomDomain(bg, svc.ServiceArn, "example.com")
	requireInvalidRequest(t, err)

	// The freed domain can move to the other service; deleting a service frees its domains.
	_, err = m.AssociateCustomDomain(bg, other.ServiceArn, "example.com", nil)
	requireNoError(t, err)

	_, err = m.DeleteService(bg, other.ServiceArn)
	requireNoError(t, err)

	_, err = m.AssociateCustomDomain(bg, svc.ServiceArn, "example.com", nil)
	requireNoError(t, err)
}

func TestCustomDomainReportsVpcDNSTargets(t *testing.T) {
	m := newMock()

	res, err := m.CreateService(bg, privateService("dns-private"))
	requireNoError(t, err)

	conn, err := m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-dns", ServiceArn: res.Service.ServiceArn,
		IngressVpcConfiguration: driver.IngressVpcConfiguration{VpcID: "vpc-7", VpcEndpointID: "vpce-7"},
	})
	requireNoError(t, err)

	assoc, err := m.AssociateCustomDomain(bg, res.Service.ServiceArn, "private.example.com", nil)
	requireNoError(t, err)

	if len(assoc.VpcDNSTargets) != 1 || assoc.VpcDNSTargets[0].VpcID != "vpc-7" || assoc.VpcDNSTargets[0].VpcIngressConnectionArn != conn.VpcIngressConnectionArn {
		t.Fatalf("unexpected VPC DNS targets: %+v", assoc.VpcDNSTargets)
	}
}

func TestCustomDomainAsyncStatus(t *testing.T) {
	m, clk := newAsyncMock()

	res, err := m.CreateService(bg, namedService("async-domain"))
	requireNoError(t, err)
	clk.Advance(time.Minute)

	assoc, err := m.AssociateCustomDomain(bg, res.Service.ServiceArn, "async.example.com", nil)
	requireNoError(t, err)
	assertStr(t, assoc.CustomDomain.Status, driver.DomainStatusCreating)

	clk.Advance(time.Minute)

	desc, _, _ := m.DescribeCustomDomains(bg, res.Service.ServiceArn, driver.Page{})
	assertStr(t, desc.CustomDomains[0].Status, driver.DomainStatusPendingCertificateDNSValidate)
}

func TestVpcConnectorNetworkIsCrossChecked(t *testing.T) {
	opts := config.NewOptions()
	m := apprunner.New(opts)
	net := vpc.New(opts)
	m.SetNetworkResolver(net)

	mk := func(cidr string) (vpcID, subnetID string) {
		v, err := net.CreateVPC(bg, netdriver.VPCConfig{CIDRBlock: cidr})
		requireNoError(t, err)

		s, err := net.CreateSubnet(bg, netdriver.SubnetConfig{VPCID: v.ID, CIDRBlock: strings.Replace(cidr, "/16", "/24", 1), AvailabilityZone: "us-east-1a"})
		requireNoError(t, err)

		return v.ID, s.ID
	}

	vpcA, subA := mk("10.0.0.0/16")
	vpcB, subB := mk("10.1.0.0/16")

	sgA, err := net.CreateSecurityGroup(bg, netdriver.SecurityGroupConfig{Name: "sg-a", VPCID: vpcA})
	requireNoError(t, err)

	sgB, err := net.CreateSecurityGroup(bg, netdriver.SecurityGroupConfig{Name: "sg-b", VPCID: vpcB})
	requireNoError(t, err)

	for name, in := range map[string]driver.CreateVpcConnectorInput{
		"unknown subnet":                {VpcConnectorName: "conn-1", Subnets: []string{"subnet-nope"}},
		"subnets in two VPCs":           {VpcConnectorName: "conn-1", Subnets: []string{subA, subB}},
		"unknown security group":        {VpcConnectorName: "conn-1", Subnets: []string{subA}, SecurityGroups: []string{"sg-nope"}},
		"security group in another VPC": {VpcConnectorName: "conn-1", Subnets: []string{subA}, SecurityGroups: []string{sgB.ID}},
		"bad name":                      {VpcConnectorName: "abc", Subnets: []string{subA}},
	} {
		in := in

		if _, err = m.CreateVpcConnector(bg, &in); err == nil {
			t.Fatalf("%s: expected InvalidRequestException", name)
		}

		requireInvalidRequest(t, err)
	}

	conn, err := m.CreateVpcConnector(bg, &driver.CreateVpcConnectorInput{VpcConnectorName: "conn-1", Subnets: []string{subA}, SecurityGroups: []string{sgA.ID}})
	requireNoError(t, err)
	assertStr(t, conn.Status, driver.ResourceStatusActive)

	// Without a resolver the ids are accepted as given.
	plain := newMock()

	_, err = plain.CreateVpcConnector(bg, &driver.CreateVpcConnectorInput{VpcConnectorName: "conn-1", Subnets: []string{"subnet-1"}})
	requireNoError(t, err)
}

func TestServiceLogGroupsAndMetrics(t *testing.T) {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk))
	m := apprunner.New(opts)
	logs := cloudwatchlogs.New(opts)
	cw := cloudwatch.New(opts)
	m.SetLogSink(logs)
	m.SetMonitoring(cw)

	svc := mustNamed(t, m, "logged-app")
	base := "/aws/apprunner/logged-app/" + svc.ServiceID

	groups, err := logs.ListLogGroups(bg, scope.Scope{})
	requireNoError(t, err)

	names := map[string]bool{}
	for _, g := range groups {
		names[g.Name] = true
	}

	if !names[base+"/service"] || !names[base+"/application"] {
		t.Fatalf("service and application log groups must be created, got %v", names)
	}

	res, err := cw.GetMetricData(bg, mondriver.GetMetricInput{
		Namespace: "AWS/AppRunner", MetricName: "ActiveInstances",
		Dimensions: map[string]string{"ServiceName": "logged-app", "ServiceID": svc.ServiceID},
		StartTime:  clk.Now().Add(-time.Hour), EndTime: clk.Now().Add(time.Hour), Period: 7200, Stat: "Maximum",
	})
	requireNoError(t, err)

	if len(res.Values) == 0 || res.Unit != "Count" {
		t.Fatalf("ActiveInstances must be published (Count), got %+v", res)
	}

	_, err = m.DeleteService(bg, svc.ServiceArn)
	requireNoError(t, err)

	groups, _ = logs.ListLogGroups(bg, scope.Scope{})
	for _, g := range groups {
		if strings.HasPrefix(g.Name, "/aws/apprunner/logged-app/") {
			t.Fatalf("log group %s survived the service delete", g.Name)
		}
	}
}

func TestIngressAndDomainStateSurviveSnapshotRestore(t *testing.T) {
	m := newMock()

	res, err := m.CreateService(bg, privateService("snap-private"))
	requireNoError(t, err)

	conn, err := m.CreateVpcIngressConnection(bg, &driver.CreateVpcIngressConnectionInput{
		VpcIngressConnectionName: "ingress-snap", ServiceArn: res.Service.ServiceArn,
		IngressVpcConfiguration: driver.IngressVpcConfiguration{VpcID: "vpc-1", VpcEndpointID: "vpce-1"},
	})
	requireNoError(t, err)

	_, err = m.AssociateCustomDomain(bg, res.Service.ServiceArn, "snap.example.com", nil)
	requireNoError(t, err)

	cfg, err := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "moved-def"})
	requireNoError(t, err)
	_, err = m.UpdateDefaultAutoScalingConfiguration(bg, cfg.AutoScalingConfigurationArn)
	requireNoError(t, err)

	data, err := m.Snapshot(bg, false)
	requireNoError(t, err)

	r := newMock()
	requireNoError(t, r.Restore(bg, data))

	got, err := r.DescribeVpcIngressConnection(bg, conn.VpcIngressConnectionArn)
	requireNoError(t, err)
	assertStr(t, got.IngressVpcConfiguration.VpcID, "vpc-1")

	desc, _, err := r.DescribeCustomDomains(bg, res.Service.ServiceArn, driver.Page{})
	requireNoError(t, err)

	if len(desc.CustomDomains) != 1 || len(desc.VpcDNSTargets) != 1 {
		t.Fatalf("restore lost the domain or ingress connection: %+v", desc)
	}

	next := mustNamed(t, r, "post-restore")
	assertStr(t, next.AutoScalingConfigurationSummary.AutoScalingConfigurationArn, cfg.AutoScalingConfigurationArn)
}

func TestPartialArnDeleteAndInvalidRevisionNotInDriver(t *testing.T) {
	m := newMock()

	cfg, err := m.CreateAutoScalingConfiguration(bg, &driver.CreateAutoScalingConfigurationInput{AutoScalingConfigurationName: "delete-me"})
	requireNoError(t, err)

	base := strings.Join(strings.Split(cfg.AutoScalingConfigurationArn, "/")[:2], "/")

	del, err := m.DeleteAutoScalingConfiguration(bg, base)
	requireNoError(t, err)
	assertStr(t, del.AutoScalingConfigurationArn, cfg.AutoScalingConfigurationArn)

	_, err = m.DescribeAutoScalingConfiguration(bg, cfg.AutoScalingConfigurationArn)
	assertException(t, err, driver.ExResourceNotFound)
}
