package configservice_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cfgsdk "github.com/aws/aws-sdk-go-v2/service/configservice"
	cfgtypes "github.com/aws/aws-sdk-go-v2/service/configservice/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	cfgprovider "github.com/stackshy/cloudemu/v2/providers/aws/configservice"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func nowUTC() time.Time { return time.Now().UTC() }

func newConfigClient(t *testing.T) *cfgsdk.Client {
	t.Helper()

	c, _ := newConfigClientWithDriver(t)

	return c
}

// newConfigClientWithDriver returns the SDK client plus the backing Config mock,
// so tests that need the opaque PutEvaluations result token (never surfaced over
// the wire) can obtain it directly.
func newConfigClientWithDriver(t *testing.T) (*cfgsdk.Client, *cfgprovider.Mock) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Config: cloud.Config, AccountID: "123456789012", Region: "us-east-1"})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := cfgsdk.NewFromConfig(cfg, func(o *cfgsdk.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})

	return client, cloud.Config
}

func TestSDKRecorderAndChannelLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutConfigurationRecorder(ctx, &cfgsdk.PutConfigurationRecorderInput{
		ConfigurationRecorder: &cfgtypes.ConfigurationRecorder{
			Name:    aws.String("default"),
			RoleARN: aws.String("arn:aws:iam::123456789012:role/config"),
			RecordingGroup: &cfgtypes.RecordingGroup{
				AllSupported: true, IncludeGlobalResourceTypes: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("PutConfigurationRecorder: %v", err)
	}

	_, err = c.PutDeliveryChannel(ctx, &cfgsdk.PutDeliveryChannelInput{
		DeliveryChannel: &cfgtypes.DeliveryChannel{
			Name: aws.String("default"), S3BucketName: aws.String("my-config-bucket"),
		},
	})
	if err != nil {
		t.Fatalf("PutDeliveryChannel: %v", err)
	}

	if _, err = c.StartConfigurationRecorder(ctx, &cfgsdk.StartConfigurationRecorderInput{
		ConfigurationRecorderName: aws.String("default"),
	}); err != nil {
		t.Fatalf("StartConfigurationRecorder: %v", err)
	}

	status, err := c.DescribeConfigurationRecorderStatus(ctx, &cfgsdk.DescribeConfigurationRecorderStatusInput{})
	if err != nil {
		t.Fatalf("DescribeConfigurationRecorderStatus: %v", err)
	}

	if len(status.ConfigurationRecordersStatus) != 1 || !status.ConfigurationRecordersStatus[0].Recording {
		t.Fatalf("expected recording=true, got %+v", status.ConfigurationRecordersStatus)
	}

	recs, err := c.DescribeConfigurationRecorders(ctx, &cfgsdk.DescribeConfigurationRecordersInput{})
	if err != nil {
		t.Fatalf("DescribeConfigurationRecorders: %v", err)
	}

	if len(recs.ConfigurationRecorders) != 1 ||
		aws.ToString(recs.ConfigurationRecorders[0].RoleARN) != "arn:aws:iam::123456789012:role/config" {
		t.Fatalf("unexpected recorders: %+v", recs.ConfigurationRecorders)
	}

	if _, err = c.StopConfigurationRecorder(ctx, &cfgsdk.StopConfigurationRecorderInput{
		ConfigurationRecorderName: aws.String("default"),
	}); err != nil {
		t.Fatalf("StopConfigurationRecorder: %v", err)
	}

	chs, err := c.DescribeDeliveryChannels(ctx, &cfgsdk.DescribeDeliveryChannelsInput{})
	if err != nil {
		t.Fatalf("DescribeDeliveryChannels: %v", err)
	}

	if len(chs.DeliveryChannels) != 1 ||
		aws.ToString(chs.DeliveryChannels[0].S3BucketName) != "my-config-bucket" {
		t.Fatalf("unexpected channels: %+v", chs.DeliveryChannels)
	}
}

func TestSDKConfigRuleLifecycleAndTags(t *testing.T) {
	ctx := context.Background()
	c, drv := newConfigClientWithDriver(t)

	_, err := c.PutConfigRule(ctx, &cfgsdk.PutConfigRuleInput{
		ConfigRule: &cfgtypes.ConfigRule{
			ConfigRuleName: aws.String("s3-sse"),
			Source: &cfgtypes.Source{
				Owner: cfgtypes.OwnerAws, SourceIdentifier: aws.String("S3_BUCKET_SSE_ENABLED"),
			},
			Scope: &cfgtypes.Scope{ComplianceResourceTypes: []string{"AWS::S3::Bucket"}},
		},
	})
	if err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	rules, err := c.DescribeConfigRules(ctx, &cfgsdk.DescribeConfigRulesInput{})
	if err != nil {
		t.Fatalf("DescribeConfigRules: %v", err)
	}

	if len(rules.ConfigRules) != 1 || aws.ToString(rules.ConfigRules[0].ConfigRuleName) != "s3-sse" {
		t.Fatalf("unexpected rules: %+v", rules.ConfigRules)
	}

	arn := aws.ToString(rules.ConfigRules[0].ConfigRuleArn)
	if !strings.Contains(arn, ":config:") {
		t.Fatalf("unexpected rule ARN: %s", arn)
	}

	if _, err = c.TagResource(ctx, &cfgsdk.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        []cfgtypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &cfgsdk.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" {
		t.Fatalf("unexpected tags: %+v", tags.Tags)
	}

	// PutEvaluations rolls compliance up to NON_COMPLIANT. The result token is an
	// opaque value the emulator issued for the rule; the SDK never surfaces it, so
	// fetch it from the driver directly.
	token, ok := drv.ResultTokenForRule("s3-sse")
	if !ok {
		t.Fatal("no result token issued for rule s3-sse")
	}

	if _, err = c.PutEvaluations(ctx, &cfgsdk.PutEvaluationsInput{
		ResultToken: aws.String(token),
		Evaluations: []cfgtypes.Evaluation{{
			ComplianceResourceType: aws.String("AWS::S3::Bucket"),
			ComplianceResourceId:   aws.String("b1"),
			ComplianceType:         cfgtypes.ComplianceTypeNonCompliant,
			OrderingTimestamp:      aws.Time(nowUTC()),
		}},
	}); err != nil {
		t.Fatalf("PutEvaluations: %v", err)
	}

	comp, err := c.DescribeComplianceByConfigRule(ctx, &cfgsdk.DescribeComplianceByConfigRuleInput{})
	if err != nil {
		t.Fatalf("DescribeComplianceByConfigRule: %v", err)
	}

	if len(comp.ComplianceByConfigRules) != 1 ||
		comp.ComplianceByConfigRules[0].Compliance.ComplianceType != cfgtypes.ComplianceTypeNonCompliant {
		t.Fatalf("unexpected compliance: %+v", comp.ComplianceByConfigRules)
	}

	if _, err = c.DeleteConfigRule(ctx, &cfgsdk.DeleteConfigRuleInput{ConfigRuleName: aws.String("s3-sse")}); err != nil {
		t.Fatalf("DeleteConfigRule: %v", err)
	}
}

func TestSDKMissingRecorderTypedError(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.DescribeConfigurationRecorders(ctx, &cfgsdk.DescribeConfigurationRecordersInput{
		ConfigurationRecorderNames: []string{"nope"},
	})
	if err == nil {
		t.Fatal("expected error for missing recorder")
	}

	var nse *cfgtypes.NoSuchConfigurationRecorderException
	if !errors.As(err, &nse) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want NoSuchConfigurationRecorderException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want NoSuchConfigurationRecorderException, got %v", err)
	}
}

func TestSDKMissingConfigRuleTypedError(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.DescribeConfigRules(ctx, &cfgsdk.DescribeConfigRulesInput{
		ConfigRuleNames: []string{"ghost"},
	})
	if err == nil {
		t.Fatal("expected error for missing rule")
	}

	var nse *cfgtypes.NoSuchConfigRuleException
	if !errors.As(err, &nse) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want NoSuchConfigRuleException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want NoSuchConfigRuleException, got %v", err)
	}
}

func TestSDKAggregatorAndStoredQuery(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutConfigurationAggregator(ctx, &cfgsdk.PutConfigurationAggregatorInput{
		ConfigurationAggregatorName: aws.String("org-agg"),
		AccountAggregationSources: []cfgtypes.AccountAggregationSource{{
			AccountIds: []string{"123456789012"}, AllAwsRegions: true,
		}},
	})
	if err != nil {
		t.Fatalf("PutConfigurationAggregator: %v", err)
	}

	aggs, err := c.DescribeConfigurationAggregators(ctx, &cfgsdk.DescribeConfigurationAggregatorsInput{})
	if err != nil {
		t.Fatalf("DescribeConfigurationAggregators: %v", err)
	}

	if len(aggs.ConfigurationAggregators) != 1 {
		t.Fatalf("want 1 aggregator, got %d", len(aggs.ConfigurationAggregators))
	}

	_, err = c.PutStoredQuery(ctx, &cfgsdk.PutStoredQueryInput{
		StoredQuery: &cfgtypes.StoredQuery{
			QueryName: aws.String("my-query"), Expression: aws.String("SELECT resourceId"),
		},
	})
	if err != nil {
		t.Fatalf("PutStoredQuery: %v", err)
	}

	got, err := c.GetStoredQuery(ctx, &cfgsdk.GetStoredQueryInput{QueryName: aws.String("my-query")})
	if err != nil {
		t.Fatalf("GetStoredQuery: %v", err)
	}

	if aws.ToString(got.StoredQuery.Expression) != "SELECT resourceId" {
		t.Fatalf("unexpected stored query: %+v", got.StoredQuery)
	}
}

// TestSDKRecorderExclusionRoundTrip guards the exclusion-list round-trip: a
// recorder created with EXCLUSION_BY_RESOURCE_TYPES must re-emit its exclusion
// list on describe (otherwise Terraform sees phantom drift).
func TestSDKRecorderExclusionRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutConfigurationRecorder(ctx, &cfgsdk.PutConfigurationRecorderInput{
		ConfigurationRecorder: &cfgtypes.ConfigurationRecorder{
			Name:    aws.String("default"),
			RoleARN: aws.String("arn:aws:iam::123456789012:role/config"),
			RecordingGroup: &cfgtypes.RecordingGroup{
				RecordingStrategy: &cfgtypes.RecordingStrategy{
					UseOnly: cfgtypes.RecordingStrategyTypeExclusionByResourceTypes,
				},
				ExclusionByResourceTypes: &cfgtypes.ExclusionByResourceTypes{
					ResourceTypes: []cfgtypes.ResourceType{cfgtypes.ResourceTypeInstance},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("PutConfigurationRecorder: %v", err)
	}

	recs, err := c.DescribeConfigurationRecorders(ctx, &cfgsdk.DescribeConfigurationRecordersInput{})
	if err != nil {
		t.Fatalf("DescribeConfigurationRecorders: %v", err)
	}

	if len(recs.ConfigurationRecorders) != 1 {
		t.Fatalf("want 1 recorder, got %d", len(recs.ConfigurationRecorders))
	}

	rg := recs.ConfigurationRecorders[0].RecordingGroup
	if rg == nil || rg.ExclusionByResourceTypes == nil || len(rg.ExclusionByResourceTypes.ResourceTypes) != 1 {
		t.Fatalf("exclusion list dropped on read-back: %+v", rg)
	}

	if rg.ExclusionByResourceTypes.ResourceTypes[0] != cfgtypes.ResourceTypeInstance {
		t.Fatalf("unexpected exclusion type: %v", rg.ExclusionByResourceTypes.ResourceTypes)
	}
}

func TestSDKRecorderRecordingModeRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	putMode := func(freq cfgtypes.RecordingFrequency, overrides []cfgtypes.RecordingModeOverride) {
		if _, err := c.PutConfigurationRecorder(ctx, &cfgsdk.PutConfigurationRecorderInput{
			ConfigurationRecorder: &cfgtypes.ConfigurationRecorder{
				Name:    aws.String("default"),
				RoleARN: aws.String("arn:aws:iam::123456789012:role/config"),
				RecordingGroup: &cfgtypes.RecordingGroup{
					AllSupported: true,
				},
				RecordingMode: &cfgtypes.RecordingMode{
					RecordingFrequency:     freq,
					RecordingModeOverrides: overrides,
				},
			},
		}); err != nil {
			t.Fatalf("PutConfigurationRecorder(%s): %v", freq, err)
		}
	}

	readMode := func() *cfgtypes.RecordingMode {
		recs, err := c.DescribeConfigurationRecorders(ctx, &cfgsdk.DescribeConfigurationRecordersInput{})
		if err != nil {
			t.Fatalf("DescribeConfigurationRecorders: %v", err)
		}

		if len(recs.ConfigurationRecorders) != 1 {
			t.Fatalf("want 1 recorder, got %d", len(recs.ConfigurationRecorders))
		}

		return recs.ConfigurationRecorders[0].RecordingMode
	}

	// CONTINUOUS round-trips.
	putMode(cfgtypes.RecordingFrequencyContinuous, nil)

	if rm := readMode(); rm == nil || rm.RecordingFrequency != cfgtypes.RecordingFrequencyContinuous {
		t.Fatalf("recordingMode dropped/wrong on read-back: %+v", rm)
	}

	// DAILY with an override round-trips (upsert onto the existing recorder).
	putMode(cfgtypes.RecordingFrequencyDaily, []cfgtypes.RecordingModeOverride{{
		Description:        aws.String("hot types"),
		RecordingFrequency: cfgtypes.RecordingFrequencyContinuous,
		ResourceTypes:      []cfgtypes.ResourceType{cfgtypes.ResourceTypeInstance},
	}})

	rm := readMode()
	if rm == nil || rm.RecordingFrequency != cfgtypes.RecordingFrequencyDaily {
		t.Fatalf("daily recordingMode dropped/wrong on read-back: %+v", rm)
	}

	if len(rm.RecordingModeOverrides) != 1 {
		t.Fatalf("want 1 override, got %d: %+v", len(rm.RecordingModeOverrides), rm.RecordingModeOverrides)
	}

	ov := rm.RecordingModeOverrides[0]
	if aws.ToString(ov.Description) != "hot types" ||
		ov.RecordingFrequency != cfgtypes.RecordingFrequencyContinuous ||
		len(ov.ResourceTypes) != 1 || ov.ResourceTypes[0] != cfgtypes.ResourceTypeInstance {
		t.Fatalf("override not round-tripped faithfully: %+v", ov)
	}
}

func TestSDKRecorderInvalidRecordingFrequency(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutConfigurationRecorder(ctx, &cfgsdk.PutConfigurationRecorderInput{
		ConfigurationRecorder: &cfgtypes.ConfigurationRecorder{
			Name:    aws.String("default"),
			RoleARN: aws.String("arn:aws:iam::123456789012:role/config"),
			RecordingMode: &cfgtypes.RecordingMode{
				RecordingFrequency: cfgtypes.RecordingFrequency("HOURLY"),
			},
		},
	})
	if err == nil {
		t.Fatal("expected an error for an invalid RecordingFrequency, got nil")
	}
}

func TestSDKComplianceDetailsEvaluationResultIdentifier(t *testing.T) {
	ctx := context.Background()
	c, drv := newConfigClientWithDriver(t)

	if _, err := c.PutConfigRule(ctx, &cfgsdk.PutConfigRuleInput{
		ConfigRule: &cfgtypes.ConfigRule{
			ConfigRuleName: aws.String("s3-sse"),
			Source: &cfgtypes.Source{
				Owner: cfgtypes.OwnerAws, SourceIdentifier: aws.String("S3_BUCKET_SSE_ENABLED"),
			},
			Scope: &cfgtypes.Scope{ComplianceResourceTypes: []string{"AWS::S3::Bucket"}},
		},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	token, ok := drv.ResultTokenForRule("s3-sse")
	if !ok {
		t.Fatal("no result token issued for rule s3-sse")
	}

	ordering := nowUTC().Truncate(time.Second)
	if _, err := c.PutEvaluations(ctx, &cfgsdk.PutEvaluationsInput{
		ResultToken: aws.String(token),
		Evaluations: []cfgtypes.Evaluation{{
			ComplianceResourceType: aws.String("AWS::S3::Bucket"),
			ComplianceResourceId:   aws.String("bucket-42"),
			ComplianceType:         cfgtypes.ComplianceTypeNonCompliant,
			OrderingTimestamp:      aws.Time(ordering),
		}},
	}); err != nil {
		t.Fatalf("PutEvaluations: %v", err)
	}

	details, err := c.GetComplianceDetailsByConfigRule(ctx, &cfgsdk.GetComplianceDetailsByConfigRuleInput{
		ConfigRuleName: aws.String("s3-sse"),
	})
	if err != nil {
		t.Fatalf("GetComplianceDetailsByConfigRule: %v", err)
	}

	if len(details.EvaluationResults) != 1 {
		t.Fatalf("want 1 evaluation result, got %d", len(details.EvaluationResults))
	}

	res := details.EvaluationResults[0]
	if res.EvaluationResultIdentifier == nil || res.EvaluationResultIdentifier.EvaluationResultQualifier == nil {
		t.Fatalf("EvaluationResultIdentifier not populated: %+v", res)
	}

	q := res.EvaluationResultIdentifier.EvaluationResultQualifier
	if aws.ToString(q.ConfigRuleName) != "s3-sse" ||
		aws.ToString(q.ResourceType) != "AWS::S3::Bucket" ||
		aws.ToString(q.ResourceId) != "bucket-42" {
		t.Fatalf("qualifier fields wrong: %+v", q)
	}

	if res.EvaluationResultIdentifier.OrderingTimestamp == nil ||
		!res.EvaluationResultIdentifier.OrderingTimestamp.Equal(ordering) {
		t.Fatalf("OrderingTimestamp wrong: got %v want %v",
			res.EvaluationResultIdentifier.OrderingTimestamp, ordering)
	}

	if res.ConfigRuleInvokedTime == nil || res.ResultRecordedTime == nil {
		t.Fatalf("ConfigRuleInvokedTime/ResultRecordedTime not populated: %+v", res)
	}

	if res.ComplianceType != cfgtypes.ComplianceTypeNonCompliant {
		t.Fatalf("unexpected compliance type: %v", res.ComplianceType)
	}
}

func TestSDKConformancePackRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutConformancePack(ctx, &cfgsdk.PutConformancePackInput{
		ConformancePackName: aws.String("cp"),
		TemplateBody:        aws.String(`{"Resources":{}}`),
	})
	if err != nil {
		t.Fatalf("PutConformancePack: %v", err)
	}

	packs, err := c.DescribeConformancePacks(ctx, &cfgsdk.DescribeConformancePacksInput{})
	if err != nil {
		t.Fatalf("DescribeConformancePacks: %v", err)
	}

	if len(packs.ConformancePackDetails) != 1 ||
		aws.ToString(packs.ConformancePackDetails[0].ConformancePackName) != "cp" {
		t.Fatalf("unexpected packs: %+v", packs.ConformancePackDetails)
	}

	status, err := c.DescribeConformancePackStatus(ctx, &cfgsdk.DescribeConformancePackStatusInput{})
	if err != nil {
		t.Fatalf("DescribeConformancePackStatus: %v", err)
	}

	if len(status.ConformancePackStatusDetails) != 1 ||
		status.ConformancePackStatusDetails[0].ConformancePackState != cfgtypes.ConformancePackStateCreateComplete {
		t.Fatalf("unexpected pack status: %+v", status.ConformancePackStatusDetails)
	}

	if _, err = c.DeleteConformancePack(ctx, &cfgsdk.DeleteConformancePackInput{
		ConformancePackName: aws.String("cp"),
	}); err != nil {
		t.Fatalf("DeleteConformancePack: %v", err)
	}
}

func TestSDKOrgRuleAndPackRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutOrganizationConfigRule(ctx, &cfgsdk.PutOrganizationConfigRuleInput{
		OrganizationConfigRuleName: aws.String("org-rule"),
		OrganizationManagedRuleMetadata: &cfgtypes.OrganizationManagedRuleMetadata{
			RuleIdentifier: aws.String("S3_BUCKET_SSE_ENABLED"),
		},
	})
	if err != nil {
		t.Fatalf("PutOrganizationConfigRule: %v", err)
	}

	rules, err := c.DescribeOrganizationConfigRules(ctx, &cfgsdk.DescribeOrganizationConfigRulesInput{})
	if err != nil {
		t.Fatalf("DescribeOrganizationConfigRules: %v", err)
	}

	if len(rules.OrganizationConfigRules) != 1 {
		t.Fatalf("want 1 org rule, got %d", len(rules.OrganizationConfigRules))
	}

	_, err = c.PutOrganizationConformancePack(ctx, &cfgsdk.PutOrganizationConformancePackInput{
		OrganizationConformancePackName: aws.String("org-pack"),
		TemplateBody:                    aws.String(`{"Resources":{}}`),
	})
	if err != nil {
		t.Fatalf("PutOrganizationConformancePack: %v", err)
	}

	packs, err := c.DescribeOrganizationConformancePacks(ctx, &cfgsdk.DescribeOrganizationConformancePacksInput{})
	if err != nil {
		t.Fatalf("DescribeOrganizationConformancePacks: %v", err)
	}

	if len(packs.OrganizationConformancePacks) != 1 {
		t.Fatalf("want 1 org pack, got %d", len(packs.OrganizationConformancePacks))
	}
}

func TestSDKRemediationRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	if _, err := c.PutConfigRule(ctx, &cfgsdk.PutConfigRuleInput{
		ConfigRule: &cfgtypes.ConfigRule{
			ConfigRuleName: aws.String("r"),
			Source:         &cfgtypes.Source{Owner: cfgtypes.OwnerAws, SourceIdentifier: aws.String("X")},
		},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	_, err := c.PutRemediationConfigurations(ctx, &cfgsdk.PutRemediationConfigurationsInput{
		RemediationConfigurations: []cfgtypes.RemediationConfiguration{{
			ConfigRuleName: aws.String("r"),
			TargetType:     cfgtypes.RemediationTargetTypeSsmDocument,
			TargetId:       aws.String("AWS-PublishSNSNotification"),
		}},
	})
	if err != nil {
		t.Fatalf("PutRemediationConfigurations: %v", err)
	}

	got, err := c.DescribeRemediationConfigurations(ctx, &cfgsdk.DescribeRemediationConfigurationsInput{
		ConfigRuleNames: []string{"r"},
	})
	if err != nil {
		t.Fatalf("DescribeRemediationConfigurations: %v", err)
	}

	if len(got.RemediationConfigurations) != 1 ||
		aws.ToString(got.RemediationConfigurations[0].TargetId) != "AWS-PublishSNSNotification" {
		t.Fatalf("unexpected remediation: %+v", got.RemediationConfigurations)
	}
}

func TestSDKResourceConfigAndHistoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutResourceConfig(ctx, &cfgsdk.PutResourceConfigInput{
		ResourceType:    aws.String("AWS::EC2::Instance"),
		ResourceId:      aws.String("i-123"),
		Configuration:   aws.String(`{"state":"running"}`),
		SchemaVersionId: aws.String("1.0"),
	})
	if err != nil {
		t.Fatalf("PutResourceConfig: %v", err)
	}

	hist, err := c.GetResourceConfigHistory(ctx, &cfgsdk.GetResourceConfigHistoryInput{
		ResourceType: cfgtypes.ResourceTypeInstance,
		ResourceId:   aws.String("i-123"),
	})
	if err != nil {
		t.Fatalf("GetResourceConfigHistory: %v", err)
	}

	if len(hist.ConfigurationItems) != 1 ||
		aws.ToString(hist.ConfigurationItems[0].ResourceId) != "i-123" {
		t.Fatalf("unexpected history: %+v", hist.ConfigurationItems)
	}
}

func TestSDKAggregateQueryAuthorizedVsUnauthorized(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutConfigurationAggregator(ctx, &cfgsdk.PutConfigurationAggregatorInput{
		ConfigurationAggregatorName: aws.String("agg"),
		AccountAggregationSources: []cfgtypes.AccountAggregationSource{{
			AccountIds: []string{"123456789012"}, AllAwsRegions: true,
		}},
	})
	if err != nil {
		t.Fatalf("PutConfigurationAggregator: %v", err)
	}

	if _, err = c.PutConfigRule(ctx, &cfgsdk.PutConfigRuleInput{
		ConfigRule: &cfgtypes.ConfigRule{
			ConfigRuleName: aws.String("r"),
			Source:         &cfgtypes.Source{Owner: cfgtypes.OwnerAws, SourceIdentifier: aws.String("X")},
		},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	// Unauthorized: the aggregate read returns nothing.
	unauth, err := c.DescribeAggregateComplianceByConfigRules(ctx,
		&cfgsdk.DescribeAggregateComplianceByConfigRulesInput{ConfigurationAggregatorName: aws.String("agg")})
	if err != nil {
		t.Fatalf("DescribeAggregate (unauthorized): %v", err)
	}

	if len(unauth.AggregateComplianceByConfigRules) != 0 {
		t.Fatalf("unauthorized aggregate must be empty, got %d", len(unauth.AggregateComplianceByConfigRules))
	}

	// Authorize the source, then the aggregate read includes the rule.
	if _, err = c.PutAggregationAuthorization(ctx, &cfgsdk.PutAggregationAuthorizationInput{
		AuthorizedAccountId: aws.String("123456789012"),
		AuthorizedAwsRegion: aws.String("us-east-1"),
	}); err != nil {
		t.Fatalf("PutAggregationAuthorization: %v", err)
	}

	auth, err := c.DescribeAggregateComplianceByConfigRules(ctx,
		&cfgsdk.DescribeAggregateComplianceByConfigRulesInput{ConfigurationAggregatorName: aws.String("agg")})
	if err != nil {
		t.Fatalf("DescribeAggregate (authorized): %v", err)
	}

	if len(auth.AggregateComplianceByConfigRules) != 1 {
		t.Fatalf("authorized aggregate must include the rule, got %d", len(auth.AggregateComplianceByConfigRules))
	}
}

func TestSDKRetentionRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newConfigClient(t)

	_, err := c.PutRetentionConfiguration(ctx, &cfgsdk.PutRetentionConfigurationInput{
		RetentionPeriodInDays: aws.Int32(90),
	})
	if err != nil {
		t.Fatalf("PutRetentionConfiguration: %v", err)
	}

	got, err := c.DescribeRetentionConfigurations(ctx, &cfgsdk.DescribeRetentionConfigurationsInput{})
	if err != nil {
		t.Fatalf("DescribeRetentionConfigurations: %v", err)
	}

	if len(got.RetentionConfigurations) != 1 ||
		aws.ToInt32(got.RetentionConfigurations[0].RetentionPeriodInDays) != 90 {
		t.Fatalf("unexpected retention: %+v", got.RetentionConfigurations)
	}
}
