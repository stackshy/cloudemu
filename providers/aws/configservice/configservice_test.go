package configservice

import (
	"context"
	"errors"
	"strconv"
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

	// A delivery channel requires a configuration recorder to exist first.
	if m.recorders.Len() == 0 {
		putRecorder(t, m, "default")
	}

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

	// Report a non-compliant evaluation using the rule's issued result token;
	// compliance rolls up.
	token, ok := m.ResultTokenForRule("s3-encrypted")
	if !ok {
		t.Fatal("no result token issued for rule")
	}

	_, err = m.PutEvaluations(ctx, token, []driver.Evaluation{
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

func TestPutEvaluationsUnknownToken(t *testing.T) {
	m := newMock(t)

	_, err := m.PutEvaluations(context.Background(), "ghost", nil, false)

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExInvalidResultToken {
		t.Fatalf("want InvalidResultTokenException, got %v", err)
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

func apiException(t *testing.T, err error) string {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *driver.APIError, got %v", err)
	}

	return apiErr.Exception
}

// TestConcurrentRecorderCreateRaceDistinctNames stresses the single-recorder cap
// under -race: N goroutines each create a DISTINCT recorder name; exactly one
// must survive (the scan+insert is serialized by createMu). Reproduces the bug
// where a Keys()-scan + separate SetIfAbsent let two distinct names both insert.
func TestConcurrentRecorderCreateRaceDistinctNames(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	const n = 200

	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_ = m.PutConfigurationRecorder(ctx, driver.ConfigurationRecorder{
				Name:    "rec-" + strconv.Itoa(i),
				RoleARN: "arn:aws:iam::123456789012:role/config",
			})
		}(i)
	}

	wg.Wait()

	if got := m.recorders.Len(); got != 1 {
		t.Fatalf("want exactly 1 recorder after concurrent distinct-name creates, got %d", got)
	}
}

// TestConcurrentChannelCreateRaceDistinctNames stresses the single-channel cap
// under -race with distinct names.
func TestConcurrentChannelCreateRaceDistinctNames(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	putRecorder(t, m, "default")

	const n = 200

	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_ = m.PutDeliveryChannel(ctx, driver.DeliveryChannel{
				Name: "ch-" + strconv.Itoa(i), S3BucketName: "bkt",
			})
		}(i)
	}

	wg.Wait()

	if got := m.channels.Len(); got != 1 {
		t.Fatalf("want exactly 1 delivery channel after concurrent distinct-name creates, got %d", got)
	}
}

// TestConcurrentServiceLinkedRecorderRace confirms the service-linked recorder
// path shares createMu with PutConfigurationRecorder: a mix of both create paths
// racing with distinct names still leaves exactly one recorder.
func TestConcurrentServiceLinkedRecorderRace(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	const n = 200

	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			if i%2 == 0 {
				_ = m.PutConfigurationRecorder(ctx, driver.ConfigurationRecorder{
					Name:    "rec-" + strconv.Itoa(i),
					RoleARN: "arn:aws:iam::123456789012:role/config",
				})
			} else {
				_, _, _ = m.PutServiceLinkedConfigurationRecorder(ctx,
					"securityhub"+strconv.Itoa(i)+".amazonaws.com", nil)
			}
		}(i)
	}

	wg.Wait()

	if got := m.recorders.Len(); got != 1 {
		t.Fatalf("want exactly 1 recorder after mixed concurrent creates, got %d", got)
	}
}

func TestPutDeliveryChannelWithoutRecorder(t *testing.T) {
	m := newMock(t)

	err := m.PutDeliveryChannel(context.Background(), driver.DeliveryChannel{Name: "default", S3BucketName: "b"})
	if got := apiException(t, err); got != driver.ExNoAvailableConfigurationRecorder {
		t.Fatalf("want NoAvailableConfigurationRecorderException, got %q (%v)", got, err)
	}
}

// TestAggregateAuthorizationGating verifies aggregated reads only include local
// data when the local source is both selected by the aggregator AND authorized.
func TestAggregateAuthorizationGating(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	acct := m.opts.AccountID
	region := m.opts.Region

	if _, err := m.PutConfigurationAggregator(ctx, driver.ConfigurationAggregator{
		Name: "agg",
		AccountSources: []driver.AccountAggregationSource{
			{AccountIDs: []string{acct}, AwsRegions: []string{region}},
		},
	}); err != nil {
		t.Fatalf("PutConfigurationAggregator: %v", err)
	}

	if err := m.PutConfigRule(ctx, driver.ConfigRule{
		ConfigRuleName: "r", Source: &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	// No authorization yet: aggregate reads see nothing.
	rules, _, err := m.DescribeAggregateComplianceByConfigRules(ctx, "agg", driver.Page{})
	if err != nil {
		t.Fatalf("DescribeAggregate (unauthorized): %v", err)
	}

	if len(rules) != 0 {
		t.Fatalf("unauthorized source must contribute nothing, got %d rules", len(rules))
	}

	// Authorize the local source: aggregate reads now include local data.
	if _, err := m.PutAggregationAuthorization(ctx, acct, region, nil); err != nil {
		t.Fatalf("PutAggregationAuthorization: %v", err)
	}

	rules, _, err = m.DescribeAggregateComplianceByConfigRules(ctx, "agg", driver.Page{})
	if err != nil {
		t.Fatalf("DescribeAggregate (authorized): %v", err)
	}

	if len(rules) != 1 {
		t.Fatalf("authorized source must contribute its rules, got %d", len(rules))
	}
}

// TestAggregateAuthorizationSourceNotSelected verifies an aggregator that does
// not select the local account/region contributes nothing even when authorized.
func TestAggregateAuthorizationSourceNotSelected(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.PutConfigurationAggregator(ctx, driver.ConfigurationAggregator{
		Name: "agg",
		AccountSources: []driver.AccountAggregationSource{
			{AccountIDs: []string{"999999999999"}, AwsRegions: []string{"eu-west-1"}},
		},
	}); err != nil {
		t.Fatalf("PutConfigurationAggregator: %v", err)
	}

	if _, err := m.PutAggregationAuthorization(ctx, m.opts.AccountID, m.opts.Region, nil); err != nil {
		t.Fatalf("PutAggregationAuthorization: %v", err)
	}

	if err := m.PutConfigRule(ctx, driver.ConfigRule{
		ConfigRuleName: "r", Source: &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	rules, _, err := m.DescribeAggregateComplianceByConfigRules(ctx, "agg", driver.Page{})
	if err != nil || len(rules) != 0 {
		t.Fatalf("non-selected source must contribute nothing: err=%v n=%d", err, len(rules))
	}
}

func TestSelectResourceConfigWhereFilter(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	mustPut := func(rt, id, cfg string) {
		if err := m.PutResourceConfig(ctx, driver.ConfigurationItem{
			ResourceType: rt, ResourceID: id, Configuration: cfg,
		}); err != nil {
			t.Fatalf("PutResourceConfig: %v", err)
		}
	}

	mustPut("AWS::S3::Bucket", "b1", `{"n":1}`)
	mustPut("AWS::EC2::Instance", "i1", `{"n":2}`)

	rows, _, err := m.SelectResourceConfig(ctx, "SELECT configuration WHERE resourceType = 'AWS::S3::Bucket'", driver.Page{})
	if err != nil {
		t.Fatalf("SelectResourceConfig: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("WHERE filter must restrict to 1 row, got %d: %v", len(rows), rows)
	}
}

func TestSelectResourceConfigProjection(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if err := m.PutResourceConfig(ctx, driver.ConfigurationItem{
		ResourceType: "AWS::S3::Bucket", ResourceID: "b1", Configuration: `{"n":1}`,
	}); err != nil {
		t.Fatalf("PutResourceConfig: %v", err)
	}

	rows, _, err := m.SelectResourceConfig(ctx, "SELECT resourceId, resourceType", driver.Page{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("Select projection: err=%v rows=%v", err, rows)
	}

	if !strings.Contains(rows[0], `"resourceid":"b1"`) ||
		!strings.Contains(rows[0], `"resourcetype":"AWS::S3::Bucket"`) {
		t.Fatalf("projection missing requested fields: %s", rows[0])
	}
}

func TestSelectResourceConfigInvalidExpression(t *testing.T) {
	m := newMock(t)

	cases := []string{
		"DELETE FROM resources",
		"SELECT bogusField",
		"SELECT * WHERE resourceId = 'x'",
	}

	for _, expr := range cases {
		_, _, err := m.SelectResourceConfig(context.Background(), expr, driver.Page{})
		if got := apiException(t, err); got != driver.ExInvalidExpression {
			t.Fatalf("expr %q: want InvalidExpressionException, got %q (%v)", expr, got, err)
		}
	}
}

func TestPutEvaluationsTokenLifecycle(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if err := m.PutConfigRule(ctx, driver.ConfigRule{
		ConfigRuleName: "r", Source: &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	create, ok := m.ResultTokenForRule("r")
	if !ok || create == "" {
		t.Fatal("expected a result token at create time")
	}

	// A fresh token is issued on StartConfigRulesEvaluation; the old one stays
	// valid (both map to the rule).
	if err := m.StartConfigRulesEvaluation(ctx, []string{"r"}); err != nil {
		t.Fatalf("StartConfigRulesEvaluation: %v", err)
	}

	started, _ := m.ResultTokenForRule("r")
	if started == create {
		t.Fatal("StartConfigRulesEvaluation should refresh the result token")
	}

	if _, err := m.PutEvaluations(ctx, started, []driver.Evaluation{
		{ComplianceResourceType: "AWS::S3::Bucket", ComplianceResourceID: "b1", ComplianceType: driver.ComplianceNonCompliant},
	}, false); err != nil {
		t.Fatalf("PutEvaluations with valid token: %v", err)
	}

	// A malformed/unknown token is InvalidResultToken.
	_, err := m.PutEvaluations(ctx, "totally-bogus", nil, false)
	if got := apiException(t, err); got != driver.ExInvalidResultToken {
		t.Fatalf("want InvalidResultTokenException, got %q", got)
	}
}

func TestPutConfigRuleValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cases := []driver.ConfigRule{
		{ConfigRuleName: "a", Source: &driver.RuleSource{Owner: "AWS"}},   // missing SourceIdentifier
		{ConfigRuleName: "b", Source: &driver.RuleSource{Owner: "BOGUS"}}, // bad owner
		{ConfigRuleName: "c", Source: &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"}, MaximumExecutionFrequency: "Weekly"},
	}

	for _, rule := range cases {
		err := m.PutConfigRule(ctx, rule)
		if got := apiException(t, err); got != driver.ExInvalidParameterValue {
			t.Fatalf("rule %q: want InvalidParameterValueException, got %q (%v)", rule.ConfigRuleName, got, err)
		}
	}
}

func TestPutConfigRuleIdempotentUpsertNoResourceInUse(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	const n = 100

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		fails []error
	)

	for i := 0; i < n; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			err := m.PutConfigRule(ctx, driver.ConfigRule{
				ConfigRuleName: "shared", Source: &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
			})
			if err != nil {
				mu.Lock()
				fails = append(fails, err)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(fails) != 0 {
		t.Fatalf("same-name re-put must never fail (upsert), got %d errors e.g. %v", len(fails), fails[0])
	}

	rules, _, _ := m.DescribeConfigRules(ctx, nil, driver.Page{})
	if len(rules) != 1 {
		t.Fatalf("want exactly 1 rule, got %d", len(rules))
	}
}

func TestTagConformancePackAndDeliveryChannel(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	arn, err := m.PutConformancePack(ctx, driver.ConformancePack{
		ConformancePackName: "pack", TemplateBody: `{"Resources":{}}`,
	})
	if err != nil {
		t.Fatalf("PutConformancePack: %v", err)
	}

	if err := m.TagResource(ctx, arn, map[string]string{"team": "sec"}); err != nil {
		t.Fatalf("TagResource(pack): %v", err)
	}

	tags, _, err := m.ListTagsForResource(ctx, arn, driver.Page{})
	if err != nil || tags["team"] != "sec" {
		t.Fatalf("pack tags: err=%v tags=%v", err, tags)
	}

	// Delivery channel is taggable via its ARN.
	putRecorder(t, m, "default")

	if err := m.PutDeliveryChannel(ctx, driver.DeliveryChannel{Name: "default", S3BucketName: "b"}); err != nil {
		t.Fatalf("PutDeliveryChannel: %v", err)
	}

	chs, _ := m.DescribeDeliveryChannels(ctx, nil)
	if len(chs) != 1 || chs[0].Arn == "" {
		t.Fatalf("delivery channel must have an ARN: %+v", chs)
	}

	if err := m.TagResource(ctx, chs[0].Arn, map[string]string{"k": "v"}); err != nil {
		t.Fatalf("TagResource(channel): %v", err)
	}

	got, _, _ := m.ListTagsForResource(ctx, chs[0].Arn, driver.Page{})
	if got["k"] != "v" {
		t.Fatalf("channel tags not stored: %v", got)
	}
}

func TestTagValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if err := m.PutConfigRule(ctx, driver.ConfigRule{
		ConfigRuleName: "r", Source: &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	rules, _, _ := m.DescribeConfigRules(ctx, nil, driver.Page{})
	arn := rules[0].ConfigRuleArn

	if err := m.TagResource(ctx, arn, map[string]string{"aws:internal": "x"}); err == nil {
		t.Fatal("expected aws:-prefixed tag key to be rejected")
	}

	long := strings.Repeat("k", maxTagKeyLen+1)
	if err := m.TagResource(ctx, arn, map[string]string{long: "v"}); err == nil {
		t.Fatal("expected over-long tag key to be rejected")
	}
}

func TestGetComplianceSummaryByResourceTypeFilter(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if err := m.PutConfigRule(ctx, driver.ConfigRule{
		ConfigRuleName: "r", Source: &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
	}); err != nil {
		t.Fatalf("PutConfigRule: %v", err)
	}

	token, _ := m.ResultTokenForRule("r")
	if _, err := m.PutEvaluations(ctx, token, []driver.Evaluation{
		{ComplianceResourceType: "AWS::S3::Bucket", ComplianceResourceID: "b1", ComplianceType: driver.ComplianceNonCompliant},
		{ComplianceResourceType: "AWS::EC2::Instance", ComplianceResourceID: "i1", ComplianceType: driver.ComplianceCompliant},
	}, false); err != nil {
		t.Fatalf("PutEvaluations: %v", err)
	}

	_, nonCompliant, err := m.GetComplianceSummaryByResourceType(ctx, []string{"AWS::S3::Bucket"})
	if err != nil {
		t.Fatalf("GetComplianceSummaryByResourceType: %v", err)
	}

	if nonCompliant != 1 {
		t.Fatalf("filter must count only S3 non-compliant, got %d", nonCompliant)
	}

	comp, _, _ := m.GetComplianceSummaryByResourceType(ctx, []string{"AWS::S3::Bucket"})
	if comp != 0 {
		t.Fatalf("EC2 compliant must be excluded by S3 filter, got %d", comp)
	}
}

func TestNextTokenIsOpaqueBase64(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := m.PutConfigRule(ctx, driver.ConfigRule{
			ConfigRuleName: "r" + strconv.Itoa(i),
			Source:         &driver.RuleSource{Owner: "AWS", SourceIdentifier: "X"},
		}); err != nil {
			t.Fatalf("PutConfigRule: %v", err)
		}
	}

	_, next, err := m.DescribeConfigRules(ctx, nil, driver.Page{Limit: 2})
	if err != nil || next == "" {
		t.Fatalf("expected a next token: err=%v next=%q", err, next)
	}

	// Not a bare integer offset.
	if _, atoiErr := strconv.Atoi(next); atoiErr == nil {
		t.Fatalf("NextToken must be opaque, not a plaintext integer: %q", next)
	}

	// Round-trips: decoding advances to the remaining page.
	rest, _, err := m.DescribeConfigRules(ctx, nil, driver.Page{Limit: 2, NextToken: next})
	if err != nil || len(rest) != 1 {
		t.Fatalf("opaque token must resume pagination: err=%v n=%d", err, len(rest))
	}
}

func TestConformancePackTemplateValidation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, err := m.PutConformancePack(ctx, driver.ConformancePack{
		ConformancePackName: "bad", TemplateBody: "not-a-template",
	}); apiException(t, err) != driver.ExInvalidParameterValue {
		t.Fatalf("expected InvalidParameterValueException for bad template")
	}
}
