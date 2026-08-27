package guardduty_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsgd "github.com/aws/aws-sdk-go-v2/service/guardduty"
	gdtypes "github.com/aws/aws-sdk-go-v2/service/guardduty/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newGDClient(t *testing.T) *awsgd.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{GuardDuty: cloud.GuardDuty})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsgd.NewFromConfig(cfg, func(o *awsgd.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

// newGDClientFullStack registers every AWS handler (not just GuardDuty), so the
// GuardDuty handler competes with the others for shared REST paths — notably
// /tags/{ResourceArn}, which EKS also claims. This is the realistic wiring and
// guards against a handler shadowing GuardDuty's tag operations.
func newGDClientFullStack(t *testing.T) *awsgd.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.DriversFrom(cloud))

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsgd.NewFromConfig(cfg, func(o *awsgd.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

// TestSDKTagsRouteToGuardDutyUnderFullStack reproduces the cross-handler routing
// bug where EKS (which also serves /tags/{ResourceArn}) shadowed GuardDuty's tag
// operations. With the full handler stack registered, tagging a GuardDuty
// detector must reach the GuardDuty handler, not EKS.
func TestSDKTagsRouteToGuardDutyUnderFullStack(t *testing.T) {
	ctx := context.Background()
	c := newGDClientFullStack(t)

	det, err := c.CreateDetector(ctx, &awsgd.CreateDetectorInput{Enable: aws.Bool(true)})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	arn := "arn:aws:guardduty:us-east-1:000000000000:detector/" + aws.ToString(det.DetectorId)

	if _, err := c.TagResource(ctx, &awsgd.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        map[string]string{"env": "test"},
	}); err != nil {
		t.Fatalf("TagResource routed to the wrong handler or failed: %v", err)
	}

	out, err := c.ListTagsForResource(ctx, &awsgd.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if out.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want env=test", out.Tags)
	}
}

func TestSDKDetectorLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)

	create, err := c.CreateDetector(ctx, &awsgd.CreateDetectorInput{
		Enable:                     aws.Bool(true),
		FindingPublishingFrequency: gdtypes.FindingPublishingFrequencyOneHour,
		Tags:                       map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	id := aws.ToString(create.DetectorId)
	if id == "" {
		t.Fatalf("empty detector id")
	}

	get, err := c.GetDetector(ctx, &awsgd.GetDetectorInput{DetectorId: create.DetectorId})
	if err != nil {
		t.Fatalf("GetDetector: %v", err)
	}

	if get.Status != gdtypes.DetectorStatusEnabled {
		t.Fatalf("status = %s, want ENABLED", get.Status)
	}

	if get.FindingPublishingFrequency != gdtypes.FindingPublishingFrequencyOneHour {
		t.Fatalf("frequency = %s", get.FindingPublishingFrequency)
	}

	if get.Tags["env"] != "prod" {
		t.Fatalf("tags not round-tripped: %+v", get.Tags)
	}

	if _, err := c.UpdateDetector(ctx, &awsgd.UpdateDetectorInput{
		DetectorId: create.DetectorId,
		Enable:     aws.Bool(false),
	}); err != nil {
		t.Fatalf("UpdateDetector: %v", err)
	}

	get, _ = c.GetDetector(ctx, &awsgd.GetDetectorInput{DetectorId: create.DetectorId})
	if get.Status != gdtypes.DetectorStatusDisabled {
		t.Fatalf("status after update = %s, want DISABLED", get.Status)
	}

	list, err := c.ListDetectors(ctx, &awsgd.ListDetectorsInput{})
	if err != nil || len(list.DetectorIds) != 1 || list.DetectorIds[0] != id {
		t.Fatalf("ListDetectors: %v ids=%v", err, list.DetectorIds)
	}

	if _, err := c.DeleteDetector(ctx, &awsgd.DeleteDetectorInput{DetectorId: create.DetectorId}); err != nil {
		t.Fatalf("DeleteDetector: %v", err)
	}

	list, _ = c.ListDetectors(ctx, &awsgd.ListDetectorsInput{})
	if len(list.DetectorIds) != 0 {
		t.Fatalf("expected no detectors after delete, got %v", list.DetectorIds)
	}
}

// TestSDKGetDetectorDefaultsDataSourcesAndFeatures verifies a detector created
// without explicit features/data sources still reports a populated DataSources
// block and a non-empty Features list, matching real GuardDuty's GetDetector.
func TestSDKGetDetectorDefaultsDataSourcesAndFeatures(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)

	create, err := c.CreateDetector(ctx, &awsgd.CreateDetectorInput{Enable: aws.Bool(true)})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	get, err := c.GetDetector(ctx, &awsgd.GetDetectorInput{DetectorId: create.DetectorId})
	if err != nil {
		t.Fatalf("GetDetector: %v", err)
	}

	if get.DataSources == nil {
		t.Fatal("DataSources is nil, want a populated data-source configuration")
	}

	if get.DataSources.CloudTrail == nil || get.DataSources.CloudTrail.Status != gdtypes.DataSourceStatusEnabled {
		t.Fatalf("CloudTrail data source not ENABLED: %+v", get.DataSources.CloudTrail)
	}

	if len(get.Features) == 0 {
		t.Fatal("Features is empty, want the default enabled feature set")
	}

	for _, f := range get.Features {
		if f.Name == "" {
			t.Fatalf("feature has empty name: %+v", f)
		}
	}
}

func TestSDKIPSetLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)

	det, err := c.CreateDetector(ctx, &awsgd.CreateDetectorInput{Enable: aws.Bool(true)})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	create, err := c.CreateIPSet(ctx, &awsgd.CreateIPSetInput{
		DetectorId: det.DetectorId,
		Name:       aws.String("trusted"),
		Format:     gdtypes.IpSetFormatTxt,
		Location:   aws.String("s3://bucket/key.txt"),
		Activate:   aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateIPSet: %v", err)
	}

	get, err := c.GetIPSet(ctx, &awsgd.GetIPSetInput{
		DetectorId: det.DetectorId,
		IpSetId:    create.IpSetId,
	})
	if err != nil {
		t.Fatalf("GetIPSet: %v", err)
	}

	if aws.ToString(get.Name) != "trusted" || get.Format != gdtypes.IpSetFormatTxt {
		t.Fatalf("unexpected IPSet: name=%s format=%s", aws.ToString(get.Name), get.Format)
	}

	if get.Status != gdtypes.IpSetStatusActive {
		t.Fatalf("status = %s, want ACTIVE", get.Status)
	}

	if _, err := c.UpdateIPSet(ctx, &awsgd.UpdateIPSetInput{
		DetectorId: det.DetectorId,
		IpSetId:    create.IpSetId,
		Name:       aws.String("renamed"),
	}); err != nil {
		t.Fatalf("UpdateIPSet: %v", err)
	}

	get, _ = c.GetIPSet(ctx, &awsgd.GetIPSetInput{DetectorId: det.DetectorId, IpSetId: create.IpSetId})
	if aws.ToString(get.Name) != "renamed" {
		t.Fatalf("name not updated: %s", aws.ToString(get.Name))
	}

	list, err := c.ListIPSets(ctx, &awsgd.ListIPSetsInput{DetectorId: det.DetectorId})
	if err != nil || len(list.IpSetIds) != 1 {
		t.Fatalf("ListIPSets: %v ids=%v", err, list.IpSetIds)
	}

	if _, err := c.DeleteIPSet(ctx, &awsgd.DeleteIPSetInput{
		DetectorId: det.DetectorId,
		IpSetId:    create.IpSetId,
	}); err != nil {
		t.Fatalf("DeleteIPSet: %v", err)
	}
}

func TestSDKGetDetectorMissingReturnsBadRequest(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)

	_, err := c.GetDetector(ctx, &awsgd.GetDetectorInput{DetectorId: aws.String("nonexistent")})
	if err == nil {
		t.Fatal("expected error for missing detector")
	}

	// Every GuardDuty op models BadRequestException (ResourceNotFoundException is
	// modeled by only 4 malware ops), so an unknown detectorId returns
	// BadRequestException. The SDK models zero typed op-errors, so it surfaces as
	// a generic smithy.APIError whose ErrorCode is the header we emit.
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "BadRequestException" {
		t.Fatalf("want BadRequestException, got %v", err)
	}
}

func TestSDKDuplicateFilterReturnsBadRequest(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)

	det, err := c.CreateDetector(ctx, &awsgd.CreateDetectorInput{Enable: aws.Bool(true)})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	in := &awsgd.CreateFilterInput{
		DetectorId: det.DetectorId,
		Name:       aws.String("dup"),
		Action:     gdtypes.FilterActionNoop,
		FindingCriteria: &gdtypes.FindingCriteria{
			Criterion: map[string]gdtypes.Condition{
				"severity": {Equals: []string{"8"}},
			},
		},
	}

	if _, err := c.CreateFilter(ctx, in); err != nil {
		t.Fatalf("first CreateFilter: %v", err)
	}

	_, err = c.CreateFilter(ctx, in)
	if err == nil {
		t.Fatal("expected error on duplicate filter")
	}

	// CreateFilter models only BadRequestException, so a duplicate name returns
	// that (surfaced as a generic smithy.APIError with our header code).
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "BadRequestException" {
		t.Fatalf("want BadRequestException, got %v", err)
	}
}

func TestSDKMembersLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)

	det, err := c.CreateDetector(ctx, &awsgd.CreateDetectorInput{Enable: aws.Bool(true)})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	create, err := c.CreateMembers(ctx, &awsgd.CreateMembersInput{
		DetectorId: det.DetectorId,
		AccountDetails: []gdtypes.AccountDetail{
			{AccountId: aws.String("111111111111"), Email: aws.String("a@x.com")},
		},
	})
	if err != nil {
		t.Fatalf("CreateMembers: %v", err)
	}

	if len(create.UnprocessedAccounts) != 0 {
		t.Fatalf("unexpected unprocessed: %v", create.UnprocessedAccounts)
	}

	if _, err = c.StartMonitoringMembers(ctx, &awsgd.StartMonitoringMembersInput{
		DetectorId: det.DetectorId,
		AccountIds: []string{"111111111111"},
	}); err != nil {
		t.Fatalf("StartMonitoringMembers: %v", err)
	}

	get, err := c.GetMembers(ctx, &awsgd.GetMembersInput{
		DetectorId: det.DetectorId,
		AccountIds: []string{"111111111111", "999999999999"},
	})
	if err != nil {
		t.Fatalf("GetMembers: %v", err)
	}

	if len(get.Members) != 1 || aws.ToString(get.Members[0].RelationshipStatus) != "ENABLED" {
		t.Fatalf("unexpected members: %+v", get.Members)
	}

	if len(get.UnprocessedAccounts) != 1 {
		t.Fatalf("expected 1 unprocessed account, got %v", get.UnprocessedAccounts)
	}

	list, err := c.ListMembers(ctx, &awsgd.ListMembersInput{DetectorId: det.DetectorId})
	if err != nil || len(list.Members) != 1 {
		t.Fatalf("ListMembers: %v members=%+v", err, list.Members)
	}
}

func TestSDKOrganizationAdminAccounts(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)

	if _, err := c.EnableOrganizationAdminAccount(ctx, &awsgd.EnableOrganizationAdminAccountInput{
		AdminAccountId: aws.String("777777777777"),
	}); err != nil {
		t.Fatalf("EnableOrganizationAdminAccount: %v", err)
	}

	list, err := c.ListOrganizationAdminAccounts(ctx, &awsgd.ListOrganizationAdminAccountsInput{})
	if err != nil || len(list.AdminAccounts) != 1 {
		t.Fatalf("ListOrganizationAdminAccounts: %v accounts=%+v", err, list.AdminAccounts)
	}

	if aws.ToString(list.AdminAccounts[0].AdminAccountId) != "777777777777" ||
		list.AdminAccounts[0].AdminStatus != gdtypes.AdminStatusEnabled {
		t.Fatalf("unexpected admin account: %+v", list.AdminAccounts[0])
	}

	if _, err = c.DisableOrganizationAdminAccount(ctx, &awsgd.DisableOrganizationAdminAccountInput{
		AdminAccountId: aws.String("777777777777"),
	}); err != nil {
		t.Fatalf("DisableOrganizationAdminAccount: %v", err)
	}
}

func TestSDKOrganizationConfiguration(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)

	det, err := c.CreateDetector(ctx, &awsgd.CreateDetectorInput{Enable: aws.Bool(true)})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	if _, err = c.UpdateOrganizationConfiguration(ctx, &awsgd.UpdateOrganizationConfigurationInput{
		DetectorId:                    det.DetectorId,
		AutoEnable:                    aws.Bool(true),
		AutoEnableOrganizationMembers: gdtypes.AutoEnableMembersAll,
	}); err != nil {
		t.Fatalf("UpdateOrganizationConfiguration: %v", err)
	}

	desc, err := c.DescribeOrganizationConfiguration(ctx, &awsgd.DescribeOrganizationConfigurationInput{
		DetectorId: det.DetectorId,
	})
	if err != nil {
		t.Fatalf("DescribeOrganizationConfiguration: %v", err)
	}

	if !aws.ToBool(desc.AutoEnable) || desc.AutoEnableOrganizationMembers != gdtypes.AutoEnableMembersAll {
		t.Fatalf("unexpected org config: autoEnable=%v members=%s",
			aws.ToBool(desc.AutoEnable), desc.AutoEnableOrganizationMembers)
	}
}

func TestSDKPublishingDestinationLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)

	det, err := c.CreateDetector(ctx, &awsgd.CreateDetectorInput{Enable: aws.Bool(true)})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	create, err := c.CreatePublishingDestination(ctx, &awsgd.CreatePublishingDestinationInput{
		DetectorId:      det.DetectorId,
		DestinationType: gdtypes.DestinationTypeS3,
		DestinationProperties: &gdtypes.DestinationProperties{
			DestinationArn: aws.String("arn:aws:s3:::mybucket"),
			KmsKeyArn:      aws.String("arn:aws:kms:us-east-1:0:key/k"),
		},
	})
	if err != nil {
		t.Fatalf("CreatePublishingDestination: %v", err)
	}

	if aws.ToString(create.DestinationId) == "" {
		t.Fatal("empty destinationId")
	}

	desc, err := c.DescribePublishingDestination(ctx, &awsgd.DescribePublishingDestinationInput{
		DetectorId:    det.DetectorId,
		DestinationId: create.DestinationId,
	})
	if err != nil {
		t.Fatalf("DescribePublishingDestination: %v", err)
	}

	if desc.Status != gdtypes.PublishingStatusPublishing {
		t.Fatalf("status = %s, want PUBLISHING", desc.Status)
	}

	if aws.ToString(desc.DestinationProperties.DestinationArn) != "arn:aws:s3:::mybucket" {
		t.Fatalf("arn not round-tripped: %+v", desc.DestinationProperties)
	}

	list, err := c.ListPublishingDestinations(ctx, &awsgd.ListPublishingDestinationsInput{
		DetectorId: det.DetectorId,
	})
	if err != nil || len(list.Destinations) != 1 {
		t.Fatalf("ListPublishingDestinations: %v dests=%+v", err, list.Destinations)
	}

	if _, err = c.DeletePublishingDestination(ctx, &awsgd.DeletePublishingDestinationInput{
		DetectorId:    det.DetectorId,
		DestinationId: create.DestinationId,
	}); err != nil {
		t.Fatalf("DeletePublishingDestination: %v", err)
	}

	_, err = c.DescribePublishingDestination(ctx, &awsgd.DescribePublishingDestinationInput{
		DetectorId:    det.DetectorId,
		DestinationId: create.DestinationId,
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "BadRequestException" {
		t.Fatalf("want BadRequestException after delete, got %v", err)
	}
}

// mustGDDetector creates an enabled detector via the SDK client and returns its id.
func mustGDDetector(ctx context.Context, t *testing.T, c *awsgd.Client) *string {
	t.Helper()

	det, err := c.CreateDetector(ctx, &awsgd.CreateDetectorInput{Enable: aws.Bool(true)})
	if err != nil {
		t.Fatalf("CreateDetector: %v", err)
	}

	return det.DetectorId
}

// TestSDKFindingsLifecycle drives create-sample -> list -> get -> archive ->
// list(archived=false excludes) -> get(still returns) -> unarchive end to end.
func TestSDKFindingsLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)
	id := mustGDDetector(ctx, t, c)

	if _, err := c.CreateSampleFindings(ctx, &awsgd.CreateSampleFindingsInput{DetectorId: id}); err != nil {
		t.Fatalf("CreateSampleFindings: %v", err)
	}

	list, err := c.ListFindings(ctx, &awsgd.ListFindingsInput{DetectorId: id})
	if err != nil || len(list.FindingIds) != 3 {
		t.Fatalf("ListFindings: %v ids=%v", err, list.FindingIds)
	}

	first := list.FindingIds[0]

	get, err := c.GetFindings(ctx, &awsgd.GetFindingsInput{DetectorId: id, FindingIds: []string{first}})
	if err != nil || len(get.Findings) != 1 {
		t.Fatalf("GetFindings: %v findings=%d", err, len(get.Findings))
	}

	if aws.ToString(get.Findings[0].Id) != first || get.Findings[0].Service == nil {
		t.Fatalf("finding not round-tripped: %+v", get.Findings[0])
	}

	if _, err := c.ArchiveFindings(ctx, &awsgd.ArchiveFindingsInput{
		DetectorId: id, FindingIds: []string{first},
	}); err != nil {
		t.Fatalf("ArchiveFindings: %v", err)
	}

	// service.archived=false must now exclude the archived finding.
	nf, err := c.ListFindings(ctx, &awsgd.ListFindingsInput{
		DetectorId: id,
		FindingCriteria: &gdtypes.FindingCriteria{
			Criterion: map[string]gdtypes.Condition{
				"service.archived": {Equals: []string{"false"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ListFindings(archived=false): %v", err)
	}

	for _, fid := range nf.FindingIds {
		if fid == first {
			t.Fatalf("archived finding %s still listed with archived=false", first)
		}
	}

	if len(nf.FindingIds) != 2 {
		t.Fatalf("archived=false list = %d, want 2", len(nf.FindingIds))
	}

	// GetFindings still returns the archived finding by id.
	get, _ = c.GetFindings(ctx, &awsgd.GetFindingsInput{DetectorId: id, FindingIds: []string{first}})
	if len(get.Findings) != 1 || !aws.ToBool(get.Findings[0].Service.Archived) {
		t.Fatalf("archived finding not returned by GetFindings: %+v", get.Findings)
	}

	if _, err := c.UnarchiveFindings(ctx, &awsgd.UnarchiveFindingsInput{
		DetectorId: id, FindingIds: []string{first},
	}); err != nil {
		t.Fatalf("UnarchiveFindings: %v", err)
	}
}

// TestSDKFindingsCriteriaAndStatistics verifies a numeric severity criterion and
// the COUNT_BY_SEVERITY statistics both round-trip through the SDK.
func TestSDKFindingsCriteriaAndStatistics(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)
	id := mustGDDetector(ctx, t, c)

	if _, err := c.CreateSampleFindings(ctx, &awsgd.CreateSampleFindingsInput{
		DetectorId:   id,
		FindingTypes: []string{"Trojan:EC2/DNSDataExfiltration", "Recon:EC2/PortProbeUnprotectedPort"},
	}); err != nil {
		t.Fatalf("CreateSampleFindings: %v", err)
	}

	hi, err := c.ListFindings(ctx, &awsgd.ListFindingsInput{
		DetectorId: id,
		FindingCriteria: &gdtypes.FindingCriteria{
			Criterion: map[string]gdtypes.Condition{
				"severity": {GreaterThanOrEqual: aws.Int64(8)},
			},
		},
	})
	if err != nil || len(hi.FindingIds) != 1 {
		t.Fatalf("severity>=8 list: %v ids=%v", err, hi.FindingIds)
	}

	stats, err := c.GetFindingsStatistics(ctx, &awsgd.GetFindingsStatisticsInput{
		DetectorId:            id,
		FindingStatisticTypes: []gdtypes.FindingStatisticType{gdtypes.FindingStatisticTypeCountBySeverity},
	})
	if err != nil || stats.FindingStatistics == nil {
		t.Fatalf("GetFindingsStatistics: %v", err)
	}

	total := int32(0)
	for _, n := range stats.FindingStatistics.CountBySeverity {
		total += n
	}

	if total != 2 {
		t.Fatalf("countBySeverity total = %d, want 2", total)
	}
}

// TestSDKCoverage verifies ListCoverage (with a filter) and GetCoverageStatistics.
func TestSDKCoverage(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)
	id := mustGDDetector(ctx, t, c)

	list, err := c.ListCoverage(ctx, &awsgd.ListCoverageInput{DetectorId: id})
	if err != nil || len(list.Resources) != 3 {
		t.Fatalf("ListCoverage: %v resources=%d", err, len(list.Resources))
	}

	eks, err := c.ListCoverage(ctx, &awsgd.ListCoverageInput{
		DetectorId: id,
		FilterCriteria: &gdtypes.CoverageFilterCriteria{
			FilterCriterion: []gdtypes.CoverageFilterCriterion{{
				CriterionKey:    gdtypes.CoverageFilterCriterionKeyResourceType,
				FilterCondition: &gdtypes.CoverageFilterCondition{Equals: []string{"EKS"}},
			}},
		},
	})
	if err != nil || len(eks.Resources) != 1 {
		t.Fatalf("EKS-filtered ListCoverage: %v resources=%d", err, len(eks.Resources))
	}

	stats, err := c.GetCoverageStatistics(ctx, &awsgd.GetCoverageStatisticsInput{
		DetectorId: id,
		StatisticsType: []gdtypes.CoverageStatisticsType{
			gdtypes.CoverageStatisticsTypeCountByCoverageStatus,
		},
	})
	if err != nil || stats.CoverageStatistics == nil {
		t.Fatalf("GetCoverageStatistics: %v", err)
	}

	if stats.CoverageStatistics.CountByCoverageStatus[string(gdtypes.CoverageStatusHealthy)] != 3 {
		t.Fatalf("HEALTHY count = %v, want 3", stats.CoverageStatistics.CountByCoverageStatus)
	}
}

// TestSDKUsageAndFreeTrial verifies GetUsageStatistics and GetRemainingFreeTrialDays.
func TestSDKUsageAndFreeTrial(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)
	id := mustGDDetector(ctx, t, c)

	usage, err := c.GetUsageStatistics(ctx, &awsgd.GetUsageStatisticsInput{
		DetectorId:         id,
		UsageStatisticType: gdtypes.UsageStatisticTypeSumByFeatures,
		UsageCriteria:      &gdtypes.UsageCriteria{},
	})
	if err != nil || usage.UsageStatistics == nil || len(usage.UsageStatistics.SumByFeature) == 0 {
		t.Fatalf("GetUsageStatistics: %v stats=%+v", err, usage.UsageStatistics)
	}

	if aws.ToString(usage.UsageStatistics.SumByFeature[0].Total.Unit) != "USD" {
		t.Fatalf("usage unit not USD: %+v", usage.UsageStatistics.SumByFeature[0].Total)
	}

	ft, err := c.GetRemainingFreeTrialDays(ctx, &awsgd.GetRemainingFreeTrialDaysInput{
		DetectorId: id,
		AccountIds: []string{"111111111111"},
	})
	if err != nil || len(ft.Accounts) != 1 {
		t.Fatalf("GetRemainingFreeTrialDays: %v accounts=%d", err, len(ft.Accounts))
	}

	if len(ft.Accounts[0].Features) == 0 ||
		aws.ToInt32(ft.Accounts[0].Features[0].FreeTrialDaysRemaining) != 30 {
		t.Fatalf("free-trial features not round-tripped: %+v", ft.Accounts[0])
	}
}

// TestSDKMalwareProtectionPlanLifecycle drives create -> get -> update -> list ->
// delete -> get(not found) end to end through the SDK.
func TestSDKMalwareProtectionPlanLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)

	create, err := c.CreateMalwareProtectionPlan(ctx, &awsgd.CreateMalwareProtectionPlanInput{
		Role: aws.String("arn:aws:iam::123456789012:role/gd"),
		ProtectedResource: &gdtypes.CreateProtectedResource{
			S3Bucket: &gdtypes.CreateS3BucketResource{BucketName: aws.String("mybucket")},
		},
		Tags: map[string]string{"env": "test"},
	})
	if err != nil || aws.ToString(create.MalwareProtectionPlanId) == "" {
		t.Fatalf("CreateMalwareProtectionPlan: %v id=%q", err, aws.ToString(create.MalwareProtectionPlanId))
	}

	planID := create.MalwareProtectionPlanId

	get, err := c.GetMalwareProtectionPlan(ctx, &awsgd.GetMalwareProtectionPlanInput{
		MalwareProtectionPlanId: planID,
	})
	if err != nil {
		t.Fatalf("GetMalwareProtectionPlan: %v", err)
	}

	if aws.ToString(get.Role) != "arn:aws:iam::123456789012:role/gd" {
		t.Fatalf("role not round-tripped: %q", aws.ToString(get.Role))
	}

	if get.ProtectedResource == nil || get.ProtectedResource.S3Bucket == nil ||
		aws.ToString(get.ProtectedResource.S3Bucket.BucketName) != "mybucket" {
		t.Fatalf("protectedResource not round-tripped: %+v", get.ProtectedResource)
	}

	if get.Tags["env"] != "test" {
		t.Fatalf("tags not round-tripped: %+v", get.Tags)
	}

	if _, err = c.UpdateMalwareProtectionPlan(ctx, &awsgd.UpdateMalwareProtectionPlanInput{
		MalwareProtectionPlanId: planID,
		Role:                    aws.String("arn:aws:iam::123456789012:role/new"),
	}); err != nil {
		t.Fatalf("UpdateMalwareProtectionPlan: %v", err)
	}

	get, _ = c.GetMalwareProtectionPlan(ctx, &awsgd.GetMalwareProtectionPlanInput{MalwareProtectionPlanId: planID})
	if aws.ToString(get.Role) != "arn:aws:iam::123456789012:role/new" {
		t.Fatalf("update not applied: %q", aws.ToString(get.Role))
	}

	list, err := c.ListMalwareProtectionPlans(ctx, &awsgd.ListMalwareProtectionPlansInput{})
	if err != nil || len(list.MalwareProtectionPlans) != 1 {
		t.Fatalf("ListMalwareProtectionPlans: %v plans=%d", err, len(list.MalwareProtectionPlans))
	}

	if _, err = c.DeleteMalwareProtectionPlan(ctx, &awsgd.DeleteMalwareProtectionPlanInput{
		MalwareProtectionPlanId: planID,
	}); err != nil {
		t.Fatalf("DeleteMalwareProtectionPlan: %v", err)
	}

	_, err = c.GetMalwareProtectionPlan(ctx, &awsgd.GetMalwareProtectionPlanInput{MalwareProtectionPlanId: planID})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ResourceNotFoundException" {
		t.Fatalf("want ResourceNotFoundException after delete, got %v", err)
	}
}

// TestSDKMalwareScanLifecycle drives start -> get -> list (filter+sort) ->
// describe (per detector) and asserts the stored terminal status is reported.
func TestSDKMalwareScanLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)
	id := mustGDDetector(ctx, t, c)

	start, err := c.StartMalwareScan(ctx, &awsgd.StartMalwareScanInput{
		ResourceArn: aws.String("arn:aws:s3:::bucket"),
	})
	if err != nil || aws.ToString(start.ScanId) == "" {
		t.Fatalf("StartMalwareScan: %v id=%q", err, aws.ToString(start.ScanId))
	}

	get, err := c.GetMalwareScan(ctx, &awsgd.GetMalwareScanInput{ScanId: start.ScanId})
	if err != nil {
		t.Fatalf("GetMalwareScan: %v", err)
	}

	if get.ScanStatus != gdtypes.MalwareProtectionScanStatusCompleted {
		t.Fatalf("scan status = %s, want COMPLETED", get.ScanStatus)
	}

	list, err := c.ListMalwareScans(ctx, &awsgd.ListMalwareScansInput{
		FilterCriteria: &gdtypes.ListMalwareScansFilterCriteria{
			ListMalwareScansFilterCriterion: []gdtypes.ListMalwareScansFilterCriterion{{
				ListMalwareScansCriterionKey: gdtypes.ListMalwareScansCriterionKeyScanStatus,
				FilterCondition:              &gdtypes.FilterCondition{EqualsValue: aws.String("COMPLETED")},
			}},
		},
		SortCriteria: &gdtypes.SortCriteria{AttributeName: aws.String("scanStartTime"), OrderBy: gdtypes.OrderByDesc},
	})
	if err != nil || len(list.Scans) != 1 {
		t.Fatalf("ListMalwareScans: %v scans=%d", err, len(list.Scans))
	}

	desc, err := c.DescribeMalwareScans(ctx, &awsgd.DescribeMalwareScansInput{DetectorId: id})
	if err != nil || len(desc.Scans) != 1 {
		t.Fatalf("DescribeMalwareScans: %v scans=%d", err, len(desc.Scans))
	}

	if _, err = c.GetMalwareScan(ctx, &awsgd.GetMalwareScanInput{ScanId: aws.String("nope")}); err == nil {
		t.Fatalf("GetMalwareScan(unknown) want error, got nil")
	}
}

// TestSDKMalwareScanSettings drives update -> get and asserts the criteria and
// snapshot-preservation setting round-trip through the detector store.
func TestSDKMalwareScanSettings(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)
	id := mustGDDetector(ctx, t, c)

	if _, err := c.UpdateMalwareScanSettings(ctx, &awsgd.UpdateMalwareScanSettingsInput{
		DetectorId:              id,
		EbsSnapshotPreservation: gdtypes.EbsSnapshotPreservationRetentionWithFinding,
		ScanResourceCriteria: &gdtypes.ScanResourceCriteria{
			Include: map[string]gdtypes.ScanCondition{
				string(gdtypes.ScanCriterionKeyEc2InstanceTag): {
					MapEquals: []gdtypes.ScanConditionPair{{Key: aws.String("team"), Value: aws.String("sec")}},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateMalwareScanSettings: %v", err)
	}

	get, err := c.GetMalwareScanSettings(ctx, &awsgd.GetMalwareScanSettingsInput{DetectorId: id})
	if err != nil {
		t.Fatalf("GetMalwareScanSettings: %v", err)
	}

	if get.EbsSnapshotPreservation != gdtypes.EbsSnapshotPreservationRetentionWithFinding {
		t.Fatalf("EbsSnapshotPreservation = %s, want RETENTION_WITH_FINDING", get.EbsSnapshotPreservation)
	}

	if get.ScanResourceCriteria == nil || len(get.ScanResourceCriteria.Include) != 1 {
		t.Fatalf("scanResourceCriteria not round-tripped: %+v", get.ScanResourceCriteria)
	}
}

// TestSDKTagsLifecycle drives tag -> list -> untag on a detector ARN through the
// SDK, plus an unknown-resource-type ARN returning BadRequestException.
func TestSDKTagsLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newGDClient(t)
	id := mustGDDetector(ctx, t, c)

	arn := aws.String("arn:aws:guardduty:us-east-1:123456789012:detector/" + aws.ToString(id))

	if _, err := c.TagResource(ctx, &awsgd.TagResourceInput{
		ResourceArn: arn,
		Tags:        map[string]string{"a": "1", "b": "2"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	list, err := c.ListTagsForResource(ctx, &awsgd.ListTagsForResourceInput{ResourceArn: arn})
	if err != nil || list.Tags["a"] != "1" || list.Tags["b"] != "2" {
		t.Fatalf("ListTagsForResource: %v tags=%+v", err, list.Tags)
	}

	if _, err = c.UntagResource(ctx, &awsgd.UntagResourceInput{
		ResourceArn: arn,
		TagKeys:     []string{"a"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	list, _ = c.ListTagsForResource(ctx, &awsgd.ListTagsForResourceInput{ResourceArn: arn})
	if _, ok := list.Tags["a"]; ok || list.Tags["b"] != "2" {
		t.Fatalf("untag not applied: %+v", list.Tags)
	}

	_, err = c.TagResource(ctx, &awsgd.TagResourceInput{
		ResourceArn: aws.String("arn:aws:guardduty:us-east-1:123456789012:widget/1"),
		Tags:        map[string]string{"x": "y"},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "BadRequestException" {
		t.Fatalf("want BadRequestException for unknown resource type, got %v", err)
	}
}
