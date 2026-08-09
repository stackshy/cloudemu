package wafv2_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awswaf "github.com/aws/aws-sdk-go-v2/service/wafv2"
	waftypes "github.com/aws/aws-sdk-go-v2/service/wafv2/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newWAFClient(t *testing.T) *awswaf.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{WAFv2: cloud.WAFv2})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awswaf.NewFromConfig(cfg, func(o *awswaf.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func visConfig(name string) *waftypes.VisibilityConfig {
	return &waftypes.VisibilityConfig{
		CloudWatchMetricsEnabled: true,
		MetricName:               aws.String(name),
		SampledRequestsEnabled:   true,
	}
}

func TestSDKWebACLCreateGetUpdateDelete(t *testing.T) {
	ctx := context.Background()
	c := newWAFClient(t)

	created, err := c.CreateWebACL(ctx, &awswaf.CreateWebACLInput{
		Name:             aws.String("acl1"),
		Scope:            waftypes.ScopeRegional,
		DefaultAction:    &waftypes.DefaultAction{Allow: &waftypes.AllowAction{}},
		VisibilityConfig: visConfig("acl1"),
	})
	if err != nil {
		t.Fatalf("CreateWebACL: %v", err)
	}

	id := aws.ToString(created.Summary.Id)
	arn := aws.ToString(created.Summary.ARN)

	got, err := c.GetWebACL(ctx, &awswaf.GetWebACLInput{
		Name: aws.String("acl1"), Scope: waftypes.ScopeRegional, Id: aws.String(id),
	})
	if err != nil {
		t.Fatalf("GetWebACL: %v", err)
	}

	if got.WebACL.DefaultAction == nil || got.WebACL.DefaultAction.Allow == nil {
		t.Fatalf("DefaultAction did not round-trip: %+v", got.WebACL.DefaultAction)
	}

	lockToken := aws.ToString(got.LockToken)

	// Stale lock token is rejected.
	_, err = c.UpdateWebACL(ctx, &awswaf.UpdateWebACLInput{
		Name: aws.String("acl1"), Scope: waftypes.ScopeRegional, Id: aws.String(id),
		LockToken:        aws.String("stale"),
		DefaultAction:    &waftypes.DefaultAction{Block: &waftypes.BlockAction{}},
		VisibilityConfig: visConfig("acl1"),
	})
	var optLock *waftypes.WAFOptimisticLockException
	if !errors.As(err, &optLock) {
		t.Fatalf("want WAFOptimisticLockException, got %v", err)
	}

	upd, err := c.UpdateWebACL(ctx, &awswaf.UpdateWebACLInput{
		Name: aws.String("acl1"), Scope: waftypes.ScopeRegional, Id: aws.String(id),
		LockToken:        aws.String(lockToken),
		DefaultAction:    &waftypes.DefaultAction{Block: &waftypes.BlockAction{}},
		VisibilityConfig: visConfig("acl1"),
	})
	if err != nil {
		t.Fatalf("UpdateWebACL: %v", err)
	}

	if _, err := c.DeleteWebACL(ctx, &awswaf.DeleteWebACLInput{
		Name: aws.String("acl1"), Scope: waftypes.ScopeRegional, Id: aws.String(id),
		LockToken: upd.NextLockToken,
	}); err != nil {
		t.Fatalf("DeleteWebACL: %v", err)
	}

	_ = arn
}

func TestSDKWebACLDuplicate(t *testing.T) {
	ctx := context.Background()
	c := newWAFClient(t)

	in := &awswaf.CreateWebACLInput{
		Name: aws.String("dup"), Scope: waftypes.ScopeRegional,
		DefaultAction:    &waftypes.DefaultAction{Allow: &waftypes.AllowAction{}},
		VisibilityConfig: visConfig("dup"),
	}

	if _, err := c.CreateWebACL(ctx, in); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := c.CreateWebACL(ctx, in)
	var dup *waftypes.WAFDuplicateItemException
	if !errors.As(err, &dup) {
		t.Fatalf("want WAFDuplicateItemException, got %v", err)
	}
}

func TestSDKIPSetCRUD(t *testing.T) {
	ctx := context.Background()
	c := newWAFClient(t)

	created, err := c.CreateIPSet(ctx, &awswaf.CreateIPSetInput{
		Name: aws.String("ips"), Scope: waftypes.ScopeRegional,
		IPAddressVersion: waftypes.IPAddressVersionIpv4,
		Addresses:        []string{"1.2.3.4/32"},
	})
	if err != nil {
		t.Fatalf("CreateIPSet: %v", err)
	}

	id := aws.ToString(created.Summary.Id)

	got, err := c.GetIPSet(ctx, &awswaf.GetIPSetInput{
		Name: aws.String("ips"), Scope: waftypes.ScopeRegional, Id: aws.String(id),
	})
	if err != nil {
		t.Fatalf("GetIPSet: %v", err)
	}

	if len(got.IPSet.Addresses) != 1 || got.IPSet.Addresses[0] != "1.2.3.4/32" {
		t.Fatalf("addresses did not round-trip: %+v", got.IPSet.Addresses)
	}

	upd, err := c.UpdateIPSet(ctx, &awswaf.UpdateIPSetInput{
		Name: aws.String("ips"), Scope: waftypes.ScopeRegional, Id: aws.String(id),
		LockToken: got.LockToken, Addresses: []string{"5.6.7.8/32"},
	})
	if err != nil {
		t.Fatalf("UpdateIPSet: %v", err)
	}

	if _, err := c.DeleteIPSet(ctx, &awswaf.DeleteIPSetInput{
		Name: aws.String("ips"), Scope: waftypes.ScopeRegional, Id: aws.String(id),
		LockToken: upd.NextLockToken,
	}); err != nil {
		t.Fatalf("DeleteIPSet: %v", err)
	}

	list, err := c.ListIPSets(ctx, &awswaf.ListIPSetsInput{Scope: waftypes.ScopeRegional})
	if err != nil {
		t.Fatalf("ListIPSets: %v", err)
	}

	if len(list.IPSets) != 0 {
		t.Fatalf("want 0 IP sets after delete, got %d", len(list.IPSets))
	}
}

func TestSDKTagsAndMissing(t *testing.T) {
	ctx := context.Background()
	c := newWAFClient(t)

	created, err := c.CreateRuleGroup(ctx, &awswaf.CreateRuleGroupInput{
		Name: aws.String("rg"), Scope: waftypes.ScopeRegional,
		Capacity: aws.Int64(50), VisibilityConfig: visConfig("rg"),
	})
	if err != nil {
		t.Fatalf("CreateRuleGroup: %v", err)
	}

	arn := aws.ToString(created.Summary.ARN)

	if _, err := c.TagResource(ctx, &awswaf.TagResourceInput{
		ResourceARN: aws.String(arn),
		Tags:        []waftypes.Tag{{Key: aws.String("team"), Value: aws.String("sec")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &awswaf.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.TagInfoForResource.TagList) != 1 {
		t.Fatalf("want 1 tag, got %+v", tags.TagInfoForResource.TagList)
	}

	// Missing resource surfaces WAFNonexistentItemException.
	_, err = c.GetRuleGroup(ctx, &awswaf.GetRuleGroupInput{
		Name: aws.String("nope"), Scope: waftypes.ScopeRegional, Id: aws.String("missing"),
	})
	var ne *waftypes.WAFNonexistentItemException
	if !errors.As(err, &ne) {
		t.Fatalf("want WAFNonexistentItemException, got %v", err)
	}
}

func TestSDKCheckCapacity(t *testing.T) {
	ctx := context.Background()
	c := newWAFClient(t)

	out, err := c.CheckCapacity(ctx, &awswaf.CheckCapacityInput{
		Scope: waftypes.ScopeRegional,
		Rules: []waftypes.Rule{
			{
				Name: aws.String("r1"), Priority: 0,
				Statement:        &waftypes.Statement{RateBasedStatement: &waftypes.RateBasedStatement{Limit: aws.Int64(100), AggregateKeyType: waftypes.RateBasedStatementAggregateKeyTypeIp}},
				Action:           &waftypes.RuleAction{Block: &waftypes.BlockAction{}},
				VisibilityConfig: visConfig("r1"),
			},
		},
	})
	if err != nil {
		t.Fatalf("CheckCapacity: %v", err)
	}

	if out.Capacity != 3 {
		t.Fatalf("want capacity 3 for one rate-based rule, got %d", out.Capacity)
	}
}

func TestSDKLoggingConfiguration(t *testing.T) {
	ctx := context.Background()
	c := newWAFClient(t)

	acl, err := c.CreateWebACL(ctx, &awswaf.CreateWebACLInput{
		Name: aws.String("logacl"), Scope: waftypes.ScopeRegional,
		DefaultAction:    &waftypes.DefaultAction{Allow: &waftypes.AllowAction{}},
		VisibilityConfig: visConfig("logacl"),
	})
	if err != nil {
		t.Fatalf("CreateWebACL: %v", err)
	}

	arn := aws.ToString(acl.Summary.ARN)

	put, err := c.PutLoggingConfiguration(ctx, &awswaf.PutLoggingConfigurationInput{
		LoggingConfiguration: &waftypes.LoggingConfiguration{
			ResourceArn:           aws.String(arn),
			LogDestinationConfigs: []string{"arn:aws:firehose:us-east-1:0:deliverystream/aws-waf-logs-x"},
		},
	})
	if err != nil {
		t.Fatalf("PutLoggingConfiguration: %v", err)
	}

	if aws.ToString(put.LoggingConfiguration.ResourceArn) != arn {
		t.Fatalf("logging config did not round-trip: %+v", put.LoggingConfiguration)
	}

	got, err := c.GetLoggingConfiguration(ctx, &awswaf.GetLoggingConfigurationInput{ResourceArn: aws.String(arn)})
	if err != nil || len(got.LoggingConfiguration.LogDestinationConfigs) != 1 {
		t.Fatalf("GetLoggingConfiguration: %v %+v", err, got)
	}

	list, err := c.ListLoggingConfigurations(ctx, &awswaf.ListLoggingConfigurationsInput{Scope: waftypes.ScopeRegional})
	if err != nil || len(list.LoggingConfigurations) != 1 {
		t.Fatalf("ListLoggingConfigurations: %v len=%d", err, len(list.LoggingConfigurations))
	}

	if _, err := c.DeleteLoggingConfiguration(ctx, &awswaf.DeleteLoggingConfigurationInput{
		ResourceArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeleteLoggingConfiguration: %v", err)
	}
}

func TestSDKPermissionPolicy(t *testing.T) {
	ctx := context.Background()
	c := newWAFClient(t)

	rg, err := c.CreateRuleGroup(ctx, &awswaf.CreateRuleGroupInput{
		Name: aws.String("polrg"), Scope: waftypes.ScopeRegional,
		Capacity: aws.Int64(10), VisibilityConfig: visConfig("polrg"),
	})
	if err != nil {
		t.Fatalf("CreateRuleGroup: %v", err)
	}

	arn := aws.ToString(rg.Summary.ARN)
	policy := `{"Version":"2012-10-17","Statement":[]}`

	if _, err := c.PutPermissionPolicy(ctx, &awswaf.PutPermissionPolicyInput{
		ResourceArn: aws.String(arn), Policy: aws.String(policy),
	}); err != nil {
		t.Fatalf("PutPermissionPolicy: %v", err)
	}

	got, err := c.GetPermissionPolicy(ctx, &awswaf.GetPermissionPolicyInput{ResourceArn: aws.String(arn)})
	if err != nil || aws.ToString(got.Policy) != policy {
		t.Fatalf("GetPermissionPolicy: %v %q", err, aws.ToString(got.Policy))
	}

	if _, err := c.DeletePermissionPolicy(ctx, &awswaf.DeletePermissionPolicyInput{
		ResourceArn: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeletePermissionPolicy: %v", err)
	}
}

func TestSDKAPIKeys(t *testing.T) {
	ctx := context.Background()
	c := newWAFClient(t)

	created, err := c.CreateAPIKey(ctx, &awswaf.CreateAPIKeyInput{
		Scope: waftypes.ScopeRegional, TokenDomains: []string{"abc.com"},
	})
	if err != nil || aws.ToString(created.APIKey) == "" {
		t.Fatalf("CreateAPIKey: %v key=%q", err, aws.ToString(created.APIKey))
	}

	apiKey := aws.ToString(created.APIKey)

	list, err := c.ListAPIKeys(ctx, &awswaf.ListAPIKeysInput{Scope: waftypes.ScopeRegional})
	if err != nil || len(list.APIKeySummaries) != 1 {
		t.Fatalf("ListAPIKeys: %v len=%d", err, len(list.APIKeySummaries))
	}

	dec, err := c.GetDecryptedAPIKey(ctx, &awswaf.GetDecryptedAPIKeyInput{
		Scope: waftypes.ScopeRegional, APIKey: aws.String(apiKey),
	})
	if err != nil || len(dec.TokenDomains) != 1 || dec.TokenDomains[0] != "abc.com" {
		t.Fatalf("GetDecryptedAPIKey: %v %+v", err, dec)
	}

	if _, err := c.DeleteAPIKey(ctx, &awswaf.DeleteAPIKeyInput{
		Scope: waftypes.ScopeRegional, APIKey: aws.String(apiKey),
	}); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
}

func TestSDKSynthesizedReadOnlyOps(t *testing.T) {
	ctx := context.Background()
	c := newWAFClient(t)

	if _, err := c.ListAvailableManagedRuleGroups(ctx, &awswaf.ListAvailableManagedRuleGroupsInput{
		Scope: waftypes.ScopeRegional,
	}); err != nil {
		t.Fatalf("ListAvailableManagedRuleGroups: %v", err)
	}

	if _, err := c.DescribeAllManagedProducts(ctx, &awswaf.DescribeAllManagedProductsInput{
		Scope: waftypes.ScopeRegional,
	}); err != nil {
		t.Fatalf("DescribeAllManagedProducts: %v", err)
	}

	if _, err := c.ListManagedRuleSets(ctx, &awswaf.ListManagedRuleSetsInput{
		Scope: waftypes.ScopeRegional,
	}); err != nil {
		t.Fatalf("ListManagedRuleSets: %v", err)
	}

	urlOut, err := c.GenerateMobileSdkReleaseUrl(ctx, &awswaf.GenerateMobileSdkReleaseUrlInput{
		Platform: waftypes.PlatformAndroid, ReleaseVersion: aws.String("1.0"),
	})
	if err != nil || aws.ToString(urlOut.Url) == "" {
		t.Fatalf("GenerateMobileSdkReleaseUrl: %v url=%q", err, aws.ToString(urlOut.Url))
	}

	// A managed rule set that the emulator does not host is reported absent.
	_, err = c.GetManagedRuleSet(ctx, &awswaf.GetManagedRuleSetInput{
		Name: aws.String("none"), Scope: waftypes.ScopeRegional, Id: aws.String("x"),
	})
	var ne *waftypes.WAFNonexistentItemException
	if !errors.As(err, &ne) {
		t.Fatalf("GetManagedRuleSet: want WAFNonexistentItemException, got %v", err)
	}
}
