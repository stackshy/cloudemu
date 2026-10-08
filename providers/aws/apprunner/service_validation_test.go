package apprunner_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/apprunner"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
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
	in2.InstanceConfiguration = &driver.InstanceConfiguration{CPU: "1 vCPU"}
	in2.HealthCheckConfiguration = &driver.HealthCheckConfiguration{Protocol: "HTTP", Path: "/health", Interval: int32Ptr(10)}

	res2, err := m.CreateService(bg, in2)
	requireNoError(t, err)
	assertStr(t, res2.Service.InstanceConfiguration.CPU, "1 vCPU")
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
	_, err = m.DeleteAutoScalingConfiguration(bg, cfg.AutoScalingConfigurationArn, false)
	requireInvalidRequest(t, err)

	_, err = m.DeleteAutoScalingConfiguration(bg, seeded, false)
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

	up, err := m.UpdateService(bg, &driver.UpdateServiceInput{ServiceArn: arn, InstanceConfiguration: &driver.InstanceConfiguration{CPU: "1024", Memory: "3072"}})
	requireNoError(t, err)
	assertStr(t, up.Service.Status, driver.StatusOperationInProgress)
	assertStr(t, up.Service.InstanceConfiguration.CPU, "1024")
	assertStr(t, up.Service.InstanceConfiguration.Memory, "3072")

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
