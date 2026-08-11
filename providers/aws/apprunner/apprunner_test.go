package apprunner_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/apprunner"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

func newMock(t *testing.T) *apprunner.Mock {
	t.Helper()

	opts := config.NewOptions(
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
		config.WithClock(config.NewFakeClock(time.Unix(1700000000, 0).UTC())),
	)

	return apprunner.New(opts)
}

func createService(t *testing.T, m *apprunner.Mock, name string) *driver.Service {
	t.Helper()

	res, err := m.CreateService(context.Background(), driver.CreateServiceInput{
		ServiceName:         name,
		SourceConfiguration: json.RawMessage(`{"ImageRepository":{"ImageIdentifier":"public.ecr.aws/x/y:latest"}}`),
	})
	if err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	return res.Service
}

func exceptionOf(t *testing.T, err error) string {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *driver.APIError, got %v", err)
	}

	return apiErr.Exception
}

func TestServiceCRUD(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	svc := createService(t, m, "web")
	if svc.Status != driver.ServiceStatusRunning {
		t.Fatalf("status = %s, want RUNNING", svc.Status)
	}

	if svc.ServiceURL == "" || svc.ServiceID == "" {
		t.Fatal("expected synthesized ServiceUrl and ServiceId")
	}

	got, err := m.DescribeService(ctx, svc.ServiceArn)
	if err != nil {
		t.Fatalf("DescribeService: %v", err)
	}

	if got.ServiceName != "web" {
		t.Fatalf("name = %s", got.ServiceName)
	}

	up, err := m.UpdateService(ctx, driver.UpdateServiceInput{
		ServiceArn: svc.ServiceArn, InstanceConfiguration: json.RawMessage(`{"Cpu":"2 vCPU"}`),
	})
	if err != nil {
		t.Fatalf("UpdateService: %v", err)
	}

	if up.OperationID == "" {
		t.Fatal("UpdateService: expected an OperationId")
	}

	del, err := m.DeleteService(ctx, svc.ServiceArn)
	if err != nil {
		t.Fatalf("DeleteService: %v", err)
	}

	if del.Service.Status != driver.ServiceStatusDeleted {
		t.Fatalf("after delete status = %s, want DELETED", del.Service.Status)
	}
}

func TestDescribeMissingService(t *testing.T) {
	m := newMock(t)

	_, err := m.DescribeService(context.Background(), "arn:aws:apprunner:us-east-1:123456789012:service/x/missing")
	if err == nil {
		t.Fatal("expected error for missing service")
	}

	if got := exceptionOf(t, err); got != driver.ExResourceNotFound {
		t.Fatalf("exception = %s, want ResourceNotFoundException", got)
	}
}

func TestPauseIllegalState(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	svc := createService(t, m, "svc")

	if _, err := m.PauseService(ctx, svc.ServiceArn); err != nil {
		t.Fatalf("PauseService (RUNNING->PAUSED): %v", err)
	}

	// Pausing an already-PAUSED service is an illegal transition.
	_, err := m.PauseService(ctx, svc.ServiceArn)
	if err == nil {
		t.Fatal("expected InvalidStateException pausing a PAUSED service")
	}

	if got := exceptionOf(t, err); got != driver.ExInvalidState {
		t.Fatalf("exception = %s, want InvalidStateException", got)
	}

	if _, err := m.ResumeService(ctx, svc.ServiceArn); err != nil {
		t.Fatalf("ResumeService (PAUSED->RUNNING): %v", err)
	}
}

func TestStartDeploymentRequiresRunning(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	svc := createService(t, m, "svc")

	opID, err := m.StartDeployment(ctx, svc.ServiceArn)
	if err != nil || opID == "" {
		t.Fatalf("StartDeployment: id=%q err=%v", opID, err)
	}

	if _, err := m.PauseService(ctx, svc.ServiceArn); err != nil {
		t.Fatalf("PauseService: %v", err)
	}

	if _, err := m.StartDeployment(ctx, svc.ServiceArn); err == nil {
		t.Fatal("expected InvalidStateException deploying a PAUSED service")
	}
}

func TestListServicesPagination(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	for i := 0; i < 3; i++ {
		createService(t, m, "svc")
	}

	page, token, err := m.ListServices(ctx, "", 2)
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}

	if len(page) != 2 || token == "" {
		t.Fatalf("first page = %d items, token=%q; want 2 + token", len(page), token)
	}

	rest, token2, err := m.ListServices(ctx, token, 2)
	if err != nil {
		t.Fatalf("ListServices(page2): %v", err)
	}

	if len(rest) != 1 || token2 != "" {
		t.Fatalf("second page = %d items, token=%q; want 1 + empty", len(rest), token2)
	}
}

func TestASCRevisionIncrements(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	c1, err := m.CreateAutoScalingConfiguration(ctx, "hi-traffic", 100, 25, 1)
	if err != nil {
		t.Fatalf("Create ASC r1: %v", err)
	}

	if c1.Revision != 1 || !c1.Latest {
		t.Fatalf("first revision = %d latest=%v, want 1/true", c1.Revision, c1.Latest)
	}

	c2, err := m.CreateAutoScalingConfiguration(ctx, "hi-traffic", 200, 25, 1)
	if err != nil {
		t.Fatalf("Create ASC r2: %v", err)
	}

	if c2.Revision != 2 || !c2.Latest {
		t.Fatalf("second revision = %d latest=%v, want 2/true", c2.Revision, c2.Latest)
	}

	// The prior revision must no longer be Latest.
	got1, err := m.DescribeAutoScalingConfiguration(ctx, c1.Arn)
	if err != nil {
		t.Fatalf("Describe ASC r1: %v", err)
	}

	if got1.Latest {
		t.Fatal("revision 1 should no longer be Latest after revision 2")
	}

	// A different name starts back at revision 1.
	c3, err := m.CreateAutoScalingConfiguration(ctx, "lo-traffic", 50, 10, 1)
	if err != nil {
		t.Fatalf("Create ASC other name: %v", err)
	}

	if c3.Revision != 1 {
		t.Fatalf("new-name revision = %d, want 1", c3.Revision)
	}
}

func TestUpdateDefaultASC(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	c1, _ := m.CreateAutoScalingConfiguration(ctx, "a", 100, 25, 1)
	c2, _ := m.CreateAutoScalingConfiguration(ctx, "b", 100, 25, 1)

	if _, err := m.UpdateDefaultAutoScalingConfiguration(ctx, c1.Arn); err != nil {
		t.Fatalf("UpdateDefault c1: %v", err)
	}

	if _, err := m.UpdateDefaultAutoScalingConfiguration(ctx, c2.Arn); err != nil {
		t.Fatalf("UpdateDefault c2: %v", err)
	}

	got1, _ := m.DescribeAutoScalingConfiguration(ctx, c1.Arn)
	if got1.IsDefault {
		t.Fatal("c1 should no longer be default after c2 was set")
	}

	got2, _ := m.DescribeAutoScalingConfiguration(ctx, c2.Arn)
	if !got2.IsDefault {
		t.Fatal("c2 should be default")
	}
}

func TestConnectionCRUD(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	conn, err := m.CreateConnection(ctx, "gh", driver.ProviderTypeGitHub, nil)
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	if conn.Status != driver.ConnectionStatusPendingHandshake {
		t.Fatalf("status = %s, want PENDING_HANDSHAKE", conn.Status)
	}

	list, _, err := m.ListConnections(ctx, "", "", 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListConnections: n=%d err=%v", len(list), err)
	}

	if _, err := m.DeleteConnection(ctx, conn.Arn); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}

	list, _, _ = m.ListConnections(ctx, "", "", 0)
	if len(list) != 0 {
		t.Fatalf("after delete want 0 connections, got %d", len(list))
	}
}

func TestConnectionInvalidProvider(t *testing.T) {
	m := newMock(t)

	_, err := m.CreateConnection(context.Background(), "x", "GITLAB", nil)
	if err == nil {
		t.Fatal("expected InvalidRequestException for unknown provider")
	}

	if got := exceptionOf(t, err); got != driver.ExInvalidRequest {
		t.Fatalf("exception = %s, want InvalidRequestException", got)
	}
}

func TestObservabilityRevisions(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	trace := json.RawMessage(`{"Vendor":"AWSXRAY"}`)

	c1, err := m.CreateObservabilityConfiguration(ctx, "obs", trace, nil)
	if err != nil || c1.Revision != 1 || !c1.Latest || c1.Status != driver.StatusActive {
		t.Fatalf("Create obs r1: %+v err=%v", c1, err)
	}

	c2, err := m.CreateObservabilityConfiguration(ctx, "obs", trace, nil)
	if err != nil || c2.Revision != 2 {
		t.Fatalf("Create obs r2: %+v err=%v", c2, err)
	}

	got1, _ := m.DescribeObservabilityConfiguration(ctx, c1.Arn)
	if got1.Latest {
		t.Fatal("obs r1 should no longer be Latest")
	}

	del, err := m.DeleteObservabilityConfiguration(ctx, c1.Arn)
	if err != nil || del.Status != driver.StatusInactive {
		t.Fatalf("Delete obs: %+v err=%v", del, err)
	}

	list, _, err := m.ListObservabilityConfigurations(ctx, "obs", true, "", 0)
	if err != nil {
		t.Fatalf("List obs: %v", err)
	}

	if len(list) != 1 || list[0].Revision != 2 {
		t.Fatalf("latestOnly list = %d items rev=%v", len(list), list)
	}
}

func TestObservabilityReservedName(t *testing.T) {
	m := newMock(t)

	_, err := m.CreateObservabilityConfiguration(context.Background(), "DefaultConfiguration", nil, nil)
	if err == nil || exceptionOf(t, err) != driver.ExInvalidRequest {
		t.Fatalf("expected InvalidRequestException for reserved name, got %v", err)
	}
}

func TestVpcConnectorCRUD(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	vc, err := m.CreateVpcConnector(ctx, "vc", []string{"subnet-1", "subnet-2"}, []string{"sg-1"}, nil)
	if err != nil || vc.Status != driver.StatusActive || vc.Revision != 1 {
		t.Fatalf("CreateVpcConnector: %+v err=%v", vc, err)
	}

	got, err := m.DescribeVpcConnector(ctx, vc.Arn)
	if err != nil || len(got.Subnets) != 2 {
		t.Fatalf("DescribeVpcConnector: %+v err=%v", got, err)
	}

	// Mutating the returned slice must not affect stored state.
	got.Subnets[0] = "MUTATED"

	again, _ := m.DescribeVpcConnector(ctx, vc.Arn)
	if again.Subnets[0] == "MUTATED" {
		t.Fatal("VpcConnector Subnets slice was aliased")
	}

	del, err := m.DeleteVpcConnector(ctx, vc.Arn)
	if err != nil || del.Status != driver.StatusInactive {
		t.Fatalf("DeleteVpcConnector: %+v err=%v", del, err)
	}

	list, _, _ := m.ListVpcConnectors(ctx, "", 0)
	if len(list) != 1 {
		t.Fatalf("ListVpcConnectors = %d, want 1", len(list))
	}
}

func TestVpcConnectorMissing(t *testing.T) {
	m := newMock(t)

	_, err := m.DescribeVpcConnector(context.Background(), "arn:aws:apprunner:us-east-1:123456789012:vpcconnector/x/1/y")
	if err == nil || exceptionOf(t, err) != driver.ExResourceNotFound {
		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}
}

func TestVpcIngressCRUDAndFilter(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	svc := createService(t, m, "ingress-svc")

	cfg := json.RawMessage(`{"VpcId":"vpc-1","VpcEndpointId":"vpce-1"}`)

	vic, err := m.CreateVpcIngressConnection(ctx, "vic", svc.ServiceArn, cfg, nil)
	if err != nil || vic.Status != driver.VpcIngressStatusAvailable || vic.DomainName == "" {
		t.Fatalf("CreateVpcIngressConnection: %+v err=%v", vic, err)
	}

	// Filter by service ARN.
	byService, _, _ := m.ListVpcIngressConnections(ctx, svc.ServiceArn, "", "", 0)
	if len(byService) != 1 {
		t.Fatalf("filter by service = %d, want 1", len(byService))
	}

	// Filter by VPC endpoint id.
	byEndpoint, _, _ := m.ListVpcIngressConnections(ctx, "", "vpce-1", "", 0)
	if len(byEndpoint) != 1 {
		t.Fatalf("filter by endpoint = %d, want 1", len(byEndpoint))
	}

	miss, _, _ := m.ListVpcIngressConnections(ctx, "", "vpce-none", "", 0)
	if len(miss) != 0 {
		t.Fatalf("filter by missing endpoint = %d, want 0", len(miss))
	}

	up, err := m.UpdateVpcIngressConnection(ctx, vic.Arn, json.RawMessage(`{"VpcId":"vpc-2","VpcEndpointId":"vpce-2"}`))
	if err != nil {
		t.Fatalf("UpdateVpcIngressConnection: %v", err)
	}

	if string(up.IngressVpcConfiguration) != `{"VpcId":"vpc-2","VpcEndpointId":"vpce-2"}` {
		t.Fatalf("update did not mutate config: %s", up.IngressVpcConfiguration)
	}

	del, err := m.DeleteVpcIngressConnection(ctx, vic.Arn)
	if err != nil || del.Status != driver.VpcIngressStatusDeleted {
		t.Fatalf("DeleteVpcIngressConnection: %+v err=%v", del, err)
	}
}

func TestVpcIngressMissingService(t *testing.T) {
	m := newMock(t)

	// CreateVpcIngressConnection does not model ResourceNotFoundException, so a
	// missing target service is an InvalidRequestException.
	_, err := m.CreateVpcIngressConnection(context.Background(), "vic", "arn:missing", nil, nil)
	if err == nil || exceptionOf(t, err) != driver.ExInvalidRequest {
		t.Fatalf("expected InvalidRequestException, got %v", err)
	}
}

func TestCustomDomainLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	svc := createService(t, m, "domain-svc")

	cd, dnsTarget, err := m.AssociateCustomDomain(ctx, svc.ServiceArn, "example.com", true)
	if err != nil || cd.Status != driver.CustomDomainStatusActive || dnsTarget == "" {
		t.Fatalf("AssociateCustomDomain: %+v dns=%q err=%v", cd, dnsTarget, err)
	}

	if len(cd.CertificateValidationRecords) == 0 {
		t.Fatal("expected synthesized certificate validation records")
	}

	// Duplicate association is rejected.
	if _, _, err := m.AssociateCustomDomain(ctx, svc.ServiceArn, "example.com", true); err == nil {
		t.Fatal("expected error associating a duplicate domain")
	}

	domains, target, _, err := m.DescribeCustomDomains(ctx, svc.ServiceArn, "", 0)
	if err != nil || len(domains) != 1 || target == "" {
		t.Fatalf("DescribeCustomDomains: n=%d target=%q err=%v", len(domains), target, err)
	}

	if _, _, err := m.DisassociateCustomDomain(ctx, svc.ServiceArn, "example.com"); err != nil {
		t.Fatalf("DisassociateCustomDomain: %v", err)
	}

	domains, _, _, _ = m.DescribeCustomDomains(ctx, svc.ServiceArn, "", 0)
	if len(domains) != 0 {
		t.Fatalf("after disassociate want 0 domains, got %d", len(domains))
	}
}

func TestDescribeCustomDomainsMissingService(t *testing.T) {
	m := newMock(t)

	_, _, _, err := m.DescribeCustomDomains(context.Background(), "arn:missing", "", 0)
	if err == nil || exceptionOf(t, err) != driver.ExResourceNotFound {
		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}
}

func TestListOperationsRecordsMutations(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	svc := createService(t, m, "ops-svc")

	if _, err := m.PauseService(ctx, svc.ServiceArn); err != nil {
		t.Fatalf("PauseService: %v", err)
	}

	if _, err := m.ResumeService(ctx, svc.ServiceArn); err != nil {
		t.Fatalf("ResumeService: %v", err)
	}

	ops, _, err := m.ListOperations(ctx, svc.ServiceArn, "", 0)
	if err != nil {
		t.Fatalf("ListOperations: %v", err)
	}

	// CREATE_SERVICE + PAUSE_SERVICE + RESUME_SERVICE.
	if len(ops) != 3 {
		t.Fatalf("ListOperations = %d ops, want 3", len(ops))
	}

	for _, o := range ops {
		if o.ID == "" || o.Status != driver.OperationStatusSucceeded {
			t.Fatalf("bad operation: %+v", o)
		}
	}
}

func TestListOperationsMissingService(t *testing.T) {
	m := newMock(t)

	_, _, err := m.ListOperations(context.Background(), "arn:missing", "", 0)
	if err == nil || exceptionOf(t, err) != driver.ExResourceNotFound {
		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}
}

func TestTagsAcrossResourceKinds(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	svc := createService(t, m, "tagged")
	asc, _ := m.CreateAutoScalingConfiguration(ctx, "tagged-asc", 100, 25, 1)
	obs, _ := m.CreateObservabilityConfiguration(ctx, "tagged-obs", nil, nil)
	vc, _ := m.CreateVpcConnector(ctx, "tagged-vc", []string{"subnet-1"}, nil, nil)
	conn, _ := m.CreateConnection(ctx, "tagged-conn", driver.ProviderTypeGitHub, nil)
	vic, _ := m.CreateVpcIngressConnection(ctx, "tagged-vic", svc.ServiceArn, nil, nil)

	arns := []string{svc.ServiceArn, asc.Arn, obs.Arn, vc.Arn, conn.Arn, vic.Arn}

	for _, arn := range arns {
		if err := m.TagResource(ctx, arn, map[string]string{"env": "prod", "team": "core"}); err != nil {
			t.Fatalf("TagResource(%s): %v", arn, err)
		}

		got, err := m.ListTagsForResource(ctx, arn)
		if err != nil || got["env"] != "prod" || got["team"] != "core" {
			t.Fatalf("ListTagsForResource(%s) = %v err=%v", arn, got, err)
		}

		if err := m.UntagResource(ctx, arn, []string{"team"}); err != nil {
			t.Fatalf("UntagResource(%s): %v", arn, err)
		}

		got, _ = m.ListTagsForResource(ctx, arn)
		if _, ok := got["team"]; ok || got["env"] != "prod" {
			t.Fatalf("after untag(%s) = %v", arn, got)
		}
	}
}

func TestTagsUnknownArn(t *testing.T) {
	m := newMock(t)

	err := m.TagResource(context.Background(), "arn:aws:apprunner:us-east-1:123456789012:unknownkind/x", nil)
	if err == nil || exceptionOf(t, err) != driver.ExResourceNotFound {
		t.Fatalf("expected ResourceNotFoundException, got %v", err)
	}
}

// TestConcurrentTagNoLostUpdate proves the tag read-modify-write is atomic under
// one lock: N goroutines each adding a distinct key must all survive. Run under
// -race.
func TestConcurrentTagNoLostUpdate(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	svc := createService(t, m, "concurrent")

	const n = 50

	var wg sync.WaitGroup

	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()

			key := "k" + itoaTest(i)
			if err := m.TagResource(ctx, svc.ServiceArn, map[string]string{key: "v"}); err != nil {
				t.Errorf("TagResource: %v", err)
			}
		}(i)
	}

	wg.Wait()

	got, err := m.ListTagsForResource(ctx, svc.ServiceArn)
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(got) != n {
		t.Fatalf("concurrent tags = %d, want %d (lost updates)", len(got), n)
	}
}

func itoaTest(v int) string {
	return strconv.Itoa(v)
}

// TestNoAliasOnRead proves Describe/List return deep copies: mutating a returned
// value's slices/maps must not affect stored state. Run under -race.
func TestNoAliasOnRead(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)
	svc := createService(t, m, "alias")

	got, err := m.DescribeService(ctx, svc.ServiceArn)
	if err != nil {
		t.Fatalf("DescribeService: %v", err)
	}

	// Mutate the returned raw config bytes; the store must not alias them.
	if len(got.SourceConfiguration) > 0 {
		got.SourceConfiguration[0] = 'X'
	}

	again, err := m.DescribeService(ctx, svc.ServiceArn)
	if err != nil {
		t.Fatalf("DescribeService(2): %v", err)
	}

	if len(again.SourceConfiguration) > 0 && again.SourceConfiguration[0] == 'X' {
		t.Fatal("stored SourceConfiguration was aliased and mutated by the caller")
	}
}

// TestNoAliasTags asserts that the Tags map returned by auto-scaling-config and
// connection reads is a copy, so a caller mutating it cannot corrupt the store.
func TestNoAliasTags(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	asc, err := m.CreateAutoScalingConfiguration(ctx, "tags-asc", 100, 25, 1)
	if err != nil {
		t.Fatalf("CreateAutoScalingConfiguration: %v", err)
	}

	if err := m.TagResource(ctx, asc.Arn, map[string]string{"env": "test"}); err != nil {
		t.Fatalf("TagResource(asc): %v", err)
	}

	got, err := m.DescribeAutoScalingConfiguration(ctx, asc.Arn)
	if err != nil {
		t.Fatalf("DescribeAutoScalingConfiguration: %v", err)
	}

	got.Tags["env"] = "mutated"
	got.Tags["injected"] = "x"

	again, _ := m.DescribeAutoScalingConfiguration(ctx, asc.Arn)
	if again.Tags["env"] != "test" || again.Tags["injected"] != "" {
		t.Fatalf("ASC Tags aliased: %v", again.Tags)
	}

	conn, err := m.CreateConnection(ctx, "tags-conn", driver.ProviderTypeGitHub, map[string]string{"team": "data"})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	list, _, err := m.ListConnections(ctx, "", "", 0)
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}

	for i := range list {
		if list[i].Arn == conn.Arn {
			list[i].Tags["team"] = "mutated"
		}
	}

	tags, err := m.ListTagsForResource(ctx, conn.Arn)
	if err != nil || tags["team"] != "data" {
		t.Fatalf("connection Tags aliased via ListConnections: %v %v", tags, err)
	}
}
