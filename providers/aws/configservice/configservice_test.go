package configservice

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions())
}

func putRecorder(t *testing.T, m *Mock, name string) {
	t.Helper()

	err := m.PutConfigurationRecorder(context.Background(), driver.ConfigurationRecorder{
		Name:    name,
		RoleARN: "arn:aws:iam::123456789012:role/config",
	})
	if err != nil {
		t.Fatalf("PutConfigurationRecorder(%s): %v", name, err)
	}
}

func putChannel(t *testing.T, m *Mock, name string) {
	t.Helper()

	err := m.PutDeliveryChannel(context.Background(), driver.DeliveryChannel{Name: name, S3BucketName: "bkt"})
	if err != nil {
		t.Fatalf("PutDeliveryChannel(%s): %v", name, err)
	}
}

func TestRecorderLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	putRecorder(t, m, "default")
	putChannel(t, m, "default")

	if err := m.StartConfigurationRecorder(ctx, "default"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	status, err := m.DescribeConfigurationRecorderStatus(ctx, nil)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if len(status) != 1 || !status[0].Recording {
		t.Fatalf("expected recording=true, got %+v", status)
	}

	if err := m.StopConfigurationRecorder(ctx, "default"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	status, _ = m.DescribeConfigurationRecorderStatus(ctx, nil)
	if status[0].Recording {
		t.Fatal("expected recording=false after Stop")
	}

	if err := m.DeleteConfigurationRecorder(ctx, "default"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestStartRecorderWithoutChannelFails(t *testing.T) {
	m := newMock(t)
	putRecorder(t, m, "default")

	err := m.StartConfigurationRecorder(context.Background(), "default")

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExNoAvailableDeliveryChannel {
		t.Fatalf("want NoAvailableDeliveryChannelException, got %v", err)
	}
}

func TestSecondDistinctRecorderRejected(t *testing.T) {
	m := newMock(t)
	putRecorder(t, m, "default")

	err := m.PutConfigurationRecorder(context.Background(), driver.ConfigurationRecorder{
		Name: "other", RoleARN: "arn:aws:iam::123456789012:role/config",
	})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExMaxNumberOfConfigurationRecordersExceeded {
		t.Fatalf("want MaxNumberOfConfigurationRecordersExceededException, got %v", err)
	}
}

func TestSecondDistinctChannelRejected(t *testing.T) {
	m := newMock(t)
	putChannel(t, m, "default")

	err := m.PutDeliveryChannel(context.Background(), driver.DeliveryChannel{Name: "other", S3BucketName: "b"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExMaxNumberOfDeliveryChannelsExceeded {
		t.Fatalf("want MaxNumberOfDeliveryChannelsExceededException, got %v", err)
	}
}

func TestDescribeMissingRecorder(t *testing.T) {
	m := newMock(t)

	_, err := m.DescribeConfigurationRecorders(context.Background(), []string{"nope"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExNoSuchConfigurationRecorder {
		t.Fatalf("want NoSuchConfigurationRecorderException, got %v", err)
	}
}

func TestConfigRuleLifecycleAndCompliance(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	rule := driver.ConfigRule{
		ConfigRuleName: "s3-encrypted",
		Source:         &driver.RuleSource{Owner: "AWS", SourceIdentifier: "S3_BUCKET_SSE_ENABLED"},
	}
	if err := m.PutConfigRule(ctx, rule); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	rules, _, err := m.DescribeConfigRules(ctx, nil, driver.Page{})
	if err != nil || len(rules) != 1 {
		t.Fatalf("DescribeConfigRules: %v n=%d", err, len(rules))
	}

	if !strings.Contains(rules[0].ConfigRuleArn, ":config:") {
		t.Fatalf("unexpected rule ARN %q", rules[0].ConfigRuleArn)
	}

	if rules[0].Compliance != driver.ComplianceInsufficientData {
		t.Fatalf("want INSUFFICIENT_DATA, got %s", rules[0].Compliance)
	}

	// Report a non-compliant evaluation; compliance rolls up.
	_, err = m.PutEvaluations(ctx, "s3-encrypted", []driver.Evaluation{
		{ComplianceResourceType: "AWS::S3::Bucket", ComplianceResourceID: "b1", ComplianceType: driver.ComplianceNonCompliant},
	}, false)
	if err != nil {
		t.Fatalf("PutEvaluations: %v", err)
	}

	rules, _, _ = m.DescribeConfigRules(ctx, nil, driver.Page{})
	if rules[0].Compliance != driver.ComplianceNonCompliant {
		t.Fatalf("want NON_COMPLIANT after eval, got %s", rules[0].Compliance)
	}

	if err := m.DeleteConfigRule(ctx, "s3-encrypted"); err != nil {
		t.Fatalf("DeleteConfigRule: %v", err)
	}
}

func TestPutEvaluationsUnknownRule(t *testing.T) {
	m := newMock(t)

	_, err := m.PutEvaluations(context.Background(), "ghost", nil, false)

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExNoSuchConfigRule {
		t.Fatalf("want NoSuchConfigRuleException, got %v", err)
	}
}

func TestInvalidNextToken(t *testing.T) {
	m := newMock(t)
	putRecorder(t, m, "default")

	_, _, err := m.ListConfigurationRecorders(context.Background(), driver.Page{NextToken: "not-a-number"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExInvalidNextToken {
		t.Fatalf("want InvalidNextTokenException, got %v", err)
	}
}

func TestTagsRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if err := m.PutConfigRule(ctx, driver.ConfigRule{
		ConfigRuleName: "r", Source: &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	rules, _, _ := m.DescribeConfigRules(ctx, nil, driver.Page{})
	arn := rules[0].ConfigRuleArn

	if err := m.TagResource(ctx, arn, map[string]string{"team": "sec"}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, _, err := m.ListTagsForResource(ctx, arn, driver.Page{})
	if err != nil || tags["team"] != "sec" {
		t.Fatalf("ListTagsForResource: %v tags=%v", err, tags)
	}

	if err := m.UntagResource(ctx, arn, []string{"team"}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, _, _ = m.ListTagsForResource(ctx, arn, driver.Page{})
	if _, ok := tags["team"]; ok {
		t.Fatal("tag not removed")
	}
}

func TestTagMissingResource(t *testing.T) {
	m := newMock(t)

	err := m.TagResource(context.Background(), "arn:aws:config:us-east-1:0:config-rule/missing", map[string]string{"a": "b"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

// TestConcurrentRuleCreateRace exercises SetIfAbsent under -race: many goroutines
// racing to create the same-named rule; exactly one wins, no data race.
func TestConcurrentRuleCreateRace(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	const n = 50

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		success int
	)

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			err := m.PutConfigRule(ctx, driver.ConfigRule{
				ConfigRuleName: "shared", Source: &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
			})
			// Put is upsert: first creates, rest update the same rule. All succeed
			// but only one distinct rule exists.
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	rules, _, _ := m.DescribeConfigRules(ctx, nil, driver.Page{})
	if len(rules) != 1 {
		t.Fatalf("want exactly 1 rule after concurrent creates, got %d", len(rules))
	}
}

// TestDescribeNoAlias confirms Describe returns deep copies: mutating a returned
// rule's Tags/Scope must not affect stored state. Run under -race.
func TestDescribeNoAlias(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if err := m.PutConfigRule(ctx, driver.ConfigRule{
		ConfigRuleName: "r",
		Source:         &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
		Scope:          &driver.RuleScope{ComplianceResourceTypes: []string{"AWS::S3::Bucket"}},
		Tags:           map[string]string{"k": "v"},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	first, _, _ := m.DescribeConfigRules(ctx, nil, driver.Page{})
	first[0].Tags["k"] = "MUTATED"
	first[0].Scope.ComplianceResourceTypes[0] = "MUTATED"

	second, _, _ := m.DescribeConfigRules(ctx, nil, driver.Page{})
	if second[0].Tags["k"] != "v" {
		t.Fatalf("Tags aliased: %v", second[0].Tags)
	}

	if second[0].Scope.ComplianceResourceTypes[0] != "AWS::S3::Bucket" {
		t.Fatalf("Scope slice aliased: %v", second[0].Scope.ComplianceResourceTypes)
	}
}

func TestAggregatorAndResourceConfig(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	agg, err := m.PutConfigurationAggregator(ctx, driver.ConfigurationAggregator{
		Name:           "org-agg",
		AccountSources: []driver.AccountAggregationSource{{AccountIDs: []string{"1"}, AwsRegions: []string{"us-east-1"}}},
	})
	if err != nil || agg.Arn == "" {
		t.Fatalf("PutConfigurationAggregator: %v", err)
	}

	if err := m.PutResourceConfig(ctx, driver.ConfigurationItem{
		ResourceType: "AWS::EC2::Instance", ResourceID: "i-1", Configuration: `{"state":"running"}`,
	}); err != nil {
		t.Fatalf("PutResourceConfig: %v", err)
	}

	found, unproc, err := m.BatchGetResourceConfig(ctx, []driver.ResourceKey{
		{ResourceType: "AWS::EC2::Instance", ResourceID: "i-1"},
		{ResourceType: "AWS::EC2::Instance", ResourceID: "missing"},
	})
	if err != nil || len(found) != 1 || len(unproc) != 1 {
		t.Fatalf("BatchGet: err=%v found=%d unproc=%d", err, len(found), len(unproc))
	}

	total, counts, _, err := m.GetDiscoveredResourceCounts(ctx, nil, driver.Page{})
	if err != nil || total != 1 || len(counts) != 1 {
		t.Fatalf("counts: err=%v total=%d n=%d", err, total, len(counts))
	}
}

func TestStoredQueryAndRetention(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	arn, err := m.PutStoredQuery(ctx, driver.StoredQuery{QueryName: "q", Expression: "SELECT resourceId"}, nil)
	if err != nil || arn == "" {
		t.Fatalf("PutStoredQuery: %v", err)
	}

	q, err := m.GetStoredQuery(ctx, "q")
	if err != nil || q.Expression != "SELECT resourceId" {
		t.Fatalf("GetStoredQuery: %v %+v", err, q)
	}

	if _, err := m.PutRetentionConfiguration(ctx, 90); err != nil {
		t.Fatalf("PutRetentionConfiguration: %v", err)
	}

	if _, err := m.PutRetentionConfiguration(ctx, 5); err == nil {
		t.Fatal("expected InvalidParameterValue for out-of-range retention")
	}
}

func TestRemediationValidatesBeforeMutate(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if err := m.PutConfigRule(ctx, driver.ConfigRule{
		ConfigRuleName: "r", Source: &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	// Batch with one good + one bad (unknown rule) must not partially apply.
	_, err := m.PutRemediationConfigurations(ctx, []driver.RemediationConfiguration{
		{ConfigRuleName: "r", TargetID: "AWS-Doc", TargetType: "SSM_DOCUMENT"},
		{ConfigRuleName: "ghost", TargetID: "AWS-Doc", TargetType: "SSM_DOCUMENT"},
	})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExNoSuchConfigRule {
		t.Fatalf("want NoSuchConfigRuleException, got %v", err)
	}

	got, _ := m.DescribeRemediationConfigurations(ctx, []string{"r"})
	if len(got) != 0 {
		t.Fatalf("partial mutation: rule r got remediation despite batch failure")
	}
}
