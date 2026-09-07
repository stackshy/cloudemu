package apprunner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/apprunner"
	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

func newMock() *apprunner.Mock {
	return apprunner.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func boolPtr(b bool) *bool { return &b }

func sampleService() *driver.CreateServiceInput {
	return &driver.CreateServiceInput{
		ServiceName: "my-app",
		SourceConfiguration: &driver.SourceConfiguration{
			ImageRepository: &driver.ImageRepository{
				ImageIdentifier:     "123456789012.dkr.ecr.us-east-1.amazonaws.com/app:latest",
				ImageRepositoryType: "ECR",
				ImageConfiguration:  &driver.ImageConfiguration{Port: "8080"},
			},
			AutoDeploymentsEnabled: boolPtr(false),
		},
		InstanceConfiguration: &driver.InstanceConfiguration{CPU: "1024", Memory: "2048"},
		Tags:                  []driver.Tag{{Key: "env", Value: "prod"}},
	}
}

func mustService(t *testing.T, m *apprunner.Mock) *driver.Service {
	t.Helper()

	res, err := m.CreateService(context.Background(), sampleService())
	requireNoError(t, err)

	return res.Service
}

func TestCreateServiceComputedFields(t *testing.T) {
	m := newMock()
	res, err := m.CreateService(context.Background(), sampleService())
	requireNoError(t, err)

	svc := res.Service
	if svc.Status != driver.StatusRunning {
		t.Fatalf("status = %q, want RUNNING (synchronous terminal)", svc.Status)
	}

	wantArn := "arn:aws:apprunner:us-east-1:123456789012:service/my-app/" + svc.ServiceID
	if svc.ServiceArn != wantArn {
		t.Fatalf("arn = %q, want %q", svc.ServiceArn, wantArn)
	}

	wantURL := svc.ServiceID + ".us-east-1.awsapprunner.com"
	if svc.ServiceURL != wantURL {
		t.Fatalf("url = %q, want %q", svc.ServiceURL, wantURL)
	}

	if len(svc.ServiceID) != 32 {
		t.Fatalf("service id %q must be 32 hex chars", svc.ServiceID)
	}

	if res.OperationID == "" {
		t.Fatalf("expected a create OperationId")
	}
}

func TestComputedFieldsByteStableAcrossReads(t *testing.T) {
	m := newMock()
	created := mustService(t, m)

	d1, err := m.DescribeService(context.Background(), created.ServiceArn)
	requireNoError(t, err)

	d2, err := m.DescribeService(context.Background(), created.ServiceArn)
	requireNoError(t, err)

	if d1.ServiceArn != d2.ServiceArn || d1.ServiceURL != d2.ServiceURL ||
		d1.ServiceID != d2.ServiceID || !d1.CreatedAt.Equal(d2.CreatedAt) {
		t.Fatalf("computed fields drifted across reads: %+v vs %+v", d1, d2)
	}

	if !d1.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("createdAt drifted from create: %v vs %v", d1.CreatedAt, created.CreatedAt)
	}
}

func TestSourceConfigRoundTripsVerbatim(t *testing.T) {
	m := newMock()
	created := mustService(t, m)

	got, err := m.DescribeService(context.Background(), created.ServiceArn)
	requireNoError(t, err)

	img := got.SourceConfiguration.ImageRepository
	if img == nil || img.ImageIdentifier != "123456789012.dkr.ecr.us-east-1.amazonaws.com/app:latest" ||
		img.ImageConfiguration == nil || img.ImageConfiguration.Port != "8080" {
		t.Fatalf("image repository did not round-trip: %+v", img)
	}

	if got.SourceConfiguration.AutoDeploymentsEnabled == nil || *got.SourceConfiguration.AutoDeploymentsEnabled {
		t.Fatalf("AutoDeploymentsEnabled=false did not round-trip via pointer")
	}
}

func TestPauseResumeStateMachine(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	svc := mustService(t, m)

	paused, err := m.PauseService(ctx, svc.ServiceArn)
	requireNoError(t, err)

	if paused.Service.Status != driver.StatusPaused || paused.OperationID == "" {
		t.Fatalf("pause: status=%q op=%q, want PAUSED + op", paused.Service.Status, paused.OperationID)
	}

	resumed, err := m.ResumeService(ctx, svc.ServiceArn)
	requireNoError(t, err)

	if resumed.Service.Status != driver.StatusRunning {
		t.Fatalf("resume: status=%q, want RUNNING", resumed.Service.Status)
	}
}

func TestIllegalTransitionsRejected(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	svc := mustService(t, m)

	// Pausing a RUNNING service is legal; a second pause on PAUSED is not.
	_, err := m.PauseService(ctx, svc.ServiceArn)
	requireNoError(t, err)

	_, err = m.PauseService(ctx, svc.ServiceArn)
	assertException(t, err, driver.ExInvalidState)

	// Resuming a service that is not PAUSED is illegal.
	_, err = m.ResumeService(ctx, svc.ServiceArn)
	requireNoError(t, err)

	_, err = m.ResumeService(ctx, svc.ServiceArn)
	assertException(t, err, driver.ExInvalidState)

	// Operating on an unknown ARN is ResourceNotFoundException.
	_, err = m.PauseService(ctx, "arn:aws:apprunner:us-east-1:123456789012:service/ghost/deadbeef")
	assertException(t, err, driver.ExResourceNotFound)
}

func TestStartDeploymentAndListOperations(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	svc := mustService(t, m)

	opID, err := m.StartDeployment(ctx, svc.ServiceArn)
	requireNoError(t, err)

	if opID == "" {
		t.Fatalf("expected a deployment OperationId")
	}

	ops, _, err := m.ListOperations(ctx, svc.ServiceArn, driver.Page{})
	requireNoError(t, err)

	// CREATE_SERVICE + START_DEPLOYMENT, newest first.
	if len(ops) != 2 || ops[0].Type != driver.OpStartDeployment || ops[0].ID != opID {
		t.Fatalf("operations = %+v, want START_DEPLOYMENT newest with id %q", ops, opID)
	}

	if ops[0].Status != driver.OpStatusSucceeded {
		t.Fatalf("operation status = %q, want SUCCEEDED", ops[0].Status)
	}
}

func TestDeleteServiceThenDescribe404(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	svc := mustService(t, m)

	del, err := m.DeleteService(ctx, svc.ServiceArn)
	requireNoError(t, err)

	if del.Service.Status != driver.StatusDeleted {
		t.Fatalf("delete status = %q, want DELETED", del.Service.Status)
	}

	_, err = m.DescribeService(ctx, svc.ServiceArn)
	assertException(t, err, driver.ExResourceNotFound)

	// Pausing a removed (deleted) service is rejected.
	_, err = m.PauseService(ctx, svc.ServiceArn)
	assertException(t, err, driver.ExResourceNotFound)
}

func TestUpdateServiceInPlace(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	svc := mustService(t, m)

	res, err := m.UpdateService(ctx, &driver.UpdateServiceInput{
		ServiceArn:            svc.ServiceArn,
		InstanceConfiguration: &driver.InstanceConfiguration{CPU: "2048", Memory: "4096"},
	})
	requireNoError(t, err)

	if res.Service.InstanceConfiguration.CPU != "2048" || res.Service.Status != driver.StatusRunning {
		t.Fatalf("update did not apply in place: %+v", res.Service.InstanceConfiguration)
	}

	if res.Service.ServiceArn != svc.ServiceArn || res.Service.ServiceID != svc.ServiceID {
		t.Fatalf("identity fields changed on update")
	}
}

func TestAutoScalingConfigurationRevisions(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	c1, err := m.CreateAutoScalingConfiguration(ctx, &driver.CreateAutoScalingConfigurationInput{
		AutoScalingConfigurationName: "high-availability",
	})
	requireNoError(t, err)

	if c1.AutoScalingConfigurationRevision != 1 || !c1.Latest || c1.MaxConcurrency != 100 {
		t.Fatalf("first revision wrong: %+v", c1)
	}

	c2, err := m.CreateAutoScalingConfiguration(ctx, &driver.CreateAutoScalingConfigurationInput{
		AutoScalingConfigurationName: "high-availability",
	})
	requireNoError(t, err)

	if c2.AutoScalingConfigurationRevision != 2 || !c2.Latest {
		t.Fatalf("second revision should be 2 and latest: %+v", c2)
	}

	latest, _, err := m.ListAutoScalingConfigurations(ctx, "high-availability", true, driver.Page{})
	requireNoError(t, err)

	if len(latest) != 1 || latest[0].AutoScalingConfigurationRevision != 2 {
		t.Fatalf("latestOnly should return only revision 2: %+v", latest)
	}

	all, _, err := m.ListAutoScalingConfigurationRevisions(ctx, "high-availability", driver.Page{})
	requireNoError(t, err)

	if len(all) != 2 {
		t.Fatalf("revisions should list 2, got %d", len(all))
	}

	del, err := m.DeleteAutoScalingConfiguration(ctx, c2.AutoScalingConfigurationArn)
	requireNoError(t, err)

	if del.Status != driver.AutoScalingStatusInactive {
		t.Fatalf("deleted config status = %q, want inactive", del.Status)
	}

	_, err = m.DescribeAutoScalingConfiguration(ctx, c2.AutoScalingConfigurationArn)
	assertException(t, err, driver.ExResourceNotFound)
}

func TestConnectionVpcConnectorObservabilityCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	conn, err := m.CreateConnection(ctx, &driver.CreateConnectionInput{
		ConnectionName: "gh", ProviderType: "GITHUB",
	})
	requireNoError(t, err)

	if conn.Status != driver.ConnectionStatusPendingHandshake {
		t.Fatalf("connection status = %q, want PENDING_HANDSHAKE", conn.Status)
	}

	vpc, err := m.CreateVpcConnector(ctx, &driver.CreateVpcConnectorInput{
		VpcConnectorName: "vpc-1", Subnets: []string{"subnet-1"},
	})
	requireNoError(t, err)

	if vpc.VpcConnectorRevision != 1 || vpc.Status != driver.ResourceStatusActive {
		t.Fatalf("vpc connector wrong: %+v", vpc)
	}

	got, err := m.DescribeVpcConnector(ctx, vpc.VpcConnectorArn)
	requireNoError(t, err)

	if got.Subnets[0] != "subnet-1" {
		t.Fatalf("vpc connector subnets did not round-trip: %+v", got.Subnets)
	}

	obs, err := m.CreateObservabilityConfiguration(ctx, &driver.CreateObservabilityConfigurationInput{
		ObservabilityConfigurationName: "obs-1",
		TraceConfiguration:             &driver.TraceConfiguration{Vendor: "AWSXRAY"},
	})
	requireNoError(t, err)

	if obs.TraceConfiguration == nil || obs.TraceConfiguration.Vendor != "AWSXRAY" {
		t.Fatalf("observability trace config did not round-trip: %+v", obs)
	}
}

func TestTagResourceMergeAndUntag(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	svc := mustService(t, m)

	requireNoError(t, m.TagResource(ctx, svc.ServiceArn, []driver.Tag{{Key: "team", Value: "data"}}))

	tags, err := m.ListTagsForResource(ctx, svc.ServiceArn)
	requireNoError(t, err)

	if len(tags) != 2 {
		t.Fatalf("expected env+team tags, got %+v", tags)
	}

	requireNoError(t, m.UntagResource(ctx, svc.ServiceArn, []string{"env"}))

	after, err := m.ListTagsForResource(ctx, svc.ServiceArn)
	requireNoError(t, err)

	if len(after) != 1 || after[0].Key != "team" {
		t.Fatalf("untag did not leave only team: %+v", after)
	}

	_, err = m.ListTagsForResource(ctx, "arn:aws:apprunner:us-east-1:123456789012:service/ghost/dead")
	assertException(t, err, driver.ExResourceNotFound)
}

func TestValidationErrors(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateService(ctx, &driver.CreateServiceInput{SourceConfiguration: &driver.SourceConfiguration{}})
	assertException(t, err, driver.ExInvalidRequest)

	_, err = m.CreateService(ctx, &driver.CreateServiceInput{ServiceName: "x"})
	assertException(t, err, driver.ExInvalidRequest)
}

// assertException asserts err is a driver.APIError with the given exception name.
func assertException(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with exception %s, got nil", want)
	}

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not a driver.APIError", err)
	}

	if apiErr.Exception != want {
		t.Fatalf("exception = %q, want %q", apiErr.Exception, want)
	}

	_ = strings.TrimSpace(apiErr.Error())
}
