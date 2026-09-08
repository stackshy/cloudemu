package ecr_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// TestSDKECRRegistryPolicy round-trips the registry-level permissions policy and
// asserts the NotFound guard: a GET/DELETE with no policy set surfaces
// RegistryPolicyNotFoundException.
func TestSDKECRRegistryPolicy(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	if _, err := client.GetRegistryPolicy(ctx, &awsecr.GetRegistryPolicyInput{}); err == nil {
		t.Fatal("GetRegistryPolicy with none set: expected error")
	} else {
		var nf *ecrtypes.RegistryPolicyNotFoundException
		if !errors.As(err, &nf) {
			t.Fatalf("GetRegistryPolicy error = %v, want RegistryPolicyNotFoundException", err)
		}
	}

	const policy = `{"Version":"2012-10-17","Statement":[{"Sid":"r","Effect":"Allow","Principal":"*","Action":"ecr:ReplicateImage"}]}`

	put, err := client.PutRegistryPolicy(ctx, &awsecr.PutRegistryPolicyInput{PolicyText: aws.String(policy)})
	if err != nil {
		t.Fatalf("PutRegistryPolicy: %v", err)
	}

	if aws.ToString(put.PolicyText) != policy || aws.ToString(put.RegistryId) != "123456789012" {
		t.Fatalf("PutRegistryPolicy echoed policy=%q registryId=%q", aws.ToString(put.PolicyText), aws.ToString(put.RegistryId))
	}

	got, err := client.GetRegistryPolicy(ctx, &awsecr.GetRegistryPolicyInput{})
	if err != nil {
		t.Fatalf("GetRegistryPolicy: %v", err)
	}

	if aws.ToString(got.PolicyText) != policy {
		t.Fatalf("GetRegistryPolicy = %q, want verbatim", aws.ToString(got.PolicyText))
	}

	if _, err := client.DeleteRegistryPolicy(ctx, &awsecr.DeleteRegistryPolicyInput{}); err != nil {
		t.Fatalf("DeleteRegistryPolicy: %v", err)
	}

	if _, err := client.GetRegistryPolicy(ctx, &awsecr.GetRegistryPolicyInput{}); err == nil {
		t.Fatal("GetRegistryPolicy after delete: expected RegistryPolicyNotFoundException")
	}
}

// TestSDKECRReplicationConfiguration exercises the aws_ecr_replication_configuration
// flow: DescribeRegistry defaults to an empty rule set, PutReplicationConfiguration
// stores rules, and DescribeRegistry reflects them verbatim.
func TestSDKECRReplicationConfiguration(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	desc, err := client.DescribeRegistry(ctx, &awsecr.DescribeRegistryInput{})
	if err != nil {
		t.Fatalf("DescribeRegistry(empty): %v", err)
	}

	if desc.ReplicationConfiguration == nil || len(desc.ReplicationConfiguration.Rules) != 0 {
		t.Fatalf("DescribeRegistry empty: want 0 rules, got %+v", desc.ReplicationConfiguration)
	}

	cfg := ecrtypes.ReplicationConfiguration{Rules: []ecrtypes.ReplicationRule{{
		Destinations: []ecrtypes.ReplicationDestination{{
			Region: aws.String("us-west-2"), RegistryId: aws.String("123456789012"),
		}},
		RepositoryFilters: []ecrtypes.RepositoryFilter{{
			Filter: aws.String("prod"), FilterType: ecrtypes.RepositoryFilterTypePrefixMatch,
		}},
	}}}

	if _, err := client.PutReplicationConfiguration(ctx, &awsecr.PutReplicationConfigurationInput{
		ReplicationConfiguration: &cfg,
	}); err != nil {
		t.Fatalf("PutReplicationConfiguration: %v", err)
	}

	desc, err = client.DescribeRegistry(ctx, &awsecr.DescribeRegistryInput{})
	if err != nil {
		t.Fatalf("DescribeRegistry: %v", err)
	}

	rules := desc.ReplicationConfiguration.Rules
	if len(rules) != 1 || len(rules[0].Destinations) != 1 || len(rules[0].RepositoryFilters) != 1 {
		t.Fatalf("DescribeRegistry rules = %+v", rules)
	}

	if aws.ToString(rules[0].Destinations[0].Region) != "us-west-2" {
		t.Fatalf("destination region = %q", aws.ToString(rules[0].Destinations[0].Region))
	}

	if aws.ToString(rules[0].RepositoryFilters[0].Filter) != "prod" ||
		rules[0].RepositoryFilters[0].FilterType != ecrtypes.RepositoryFilterTypePrefixMatch {
		t.Fatalf("repository filter = %+v", rules[0].RepositoryFilters[0])
	}
}

// TestSDKECRPullThroughCacheRule exercises the aws_ecr_pull_through_cache_rule
// flow (create, describe, delete) and the PullThroughCacheRuleNotFound guard.
func TestSDKECRPullThroughCacheRule(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	created, err := client.CreatePullThroughCacheRule(ctx, &awsecr.CreatePullThroughCacheRuleInput{
		EcrRepositoryPrefix: aws.String("ecr-public"),
		UpstreamRegistryUrl: aws.String("public.ecr.aws"),
	})
	if err != nil {
		t.Fatalf("CreatePullThroughCacheRule: %v", err)
	}

	if aws.ToString(created.EcrRepositoryPrefix) != "ecr-public" ||
		aws.ToString(created.UpstreamRegistryUrl) != "public.ecr.aws" ||
		aws.ToString(created.RegistryId) != "123456789012" {
		t.Fatalf("CreatePullThroughCacheRule echoed %+v", created)
	}

	// Duplicate prefix is rejected.
	if _, err := client.CreatePullThroughCacheRule(ctx, &awsecr.CreatePullThroughCacheRuleInput{
		EcrRepositoryPrefix: aws.String("ecr-public"),
		UpstreamRegistryUrl: aws.String("public.ecr.aws"),
	}); err == nil {
		t.Fatal("duplicate CreatePullThroughCacheRule: expected error")
	}

	desc, err := client.DescribePullThroughCacheRules(ctx, &awsecr.DescribePullThroughCacheRulesInput{})
	if err != nil {
		t.Fatalf("DescribePullThroughCacheRules: %v", err)
	}

	if len(desc.PullThroughCacheRules) != 1 ||
		aws.ToString(desc.PullThroughCacheRules[0].EcrRepositoryPrefix) != "ecr-public" {
		t.Fatalf("DescribePullThroughCacheRules = %+v", desc.PullThroughCacheRules)
	}

	if _, err := client.DeletePullThroughCacheRule(ctx, &awsecr.DeletePullThroughCacheRuleInput{
		EcrRepositoryPrefix: aws.String("ecr-public"),
	}); err != nil {
		t.Fatalf("DeletePullThroughCacheRule: %v", err)
	}

	// Deleting a missing rule surfaces PullThroughCacheRuleNotFoundException.
	_, err = client.DeletePullThroughCacheRule(ctx, &awsecr.DeletePullThroughCacheRuleInput{
		EcrRepositoryPrefix: aws.String("ecr-public"),
	})
	if err == nil {
		t.Fatal("DeletePullThroughCacheRule missing: expected error")
	}

	var nf *ecrtypes.PullThroughCacheRuleNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DeletePullThroughCacheRule missing error = %v, want PullThroughCacheRuleNotFoundException", err)
	}
}

// TestSDKECRRegistryScanningConfiguration exercises the
// aws_ecr_registry_scanning_configuration flow: the default is BASIC with no
// rules, and Put switches it to ENHANCED with a continuous-scan rule that Get
// reflects.
func TestSDKECRRegistryScanningConfiguration(t *testing.T) {
	client := newECRClient(t)
	ctx := context.Background()

	got, err := client.GetRegistryScanningConfiguration(ctx, &awsecr.GetRegistryScanningConfigurationInput{})
	if err != nil {
		t.Fatalf("GetRegistryScanningConfiguration(default): %v", err)
	}

	if got.ScanningConfiguration == nil || got.ScanningConfiguration.ScanType != ecrtypes.ScanTypeBasic {
		t.Fatalf("default scanType = %+v, want BASIC", got.ScanningConfiguration)
	}

	if _, err := client.PutRegistryScanningConfiguration(ctx, &awsecr.PutRegistryScanningConfigurationInput{
		ScanType: ecrtypes.ScanTypeEnhanced,
		Rules: []ecrtypes.RegistryScanningRule{{
			ScanFrequency: ecrtypes.ScanFrequencyContinuousScan,
			RepositoryFilters: []ecrtypes.ScanningRepositoryFilter{{
				Filter: aws.String("*"), FilterType: ecrtypes.ScanningRepositoryFilterTypeWildcard,
			}},
		}},
	}); err != nil {
		t.Fatalf("PutRegistryScanningConfiguration: %v", err)
	}

	got, err = client.GetRegistryScanningConfiguration(ctx, &awsecr.GetRegistryScanningConfigurationInput{})
	if err != nil {
		t.Fatalf("GetRegistryScanningConfiguration: %v", err)
	}

	sc := got.ScanningConfiguration
	if sc.ScanType != ecrtypes.ScanTypeEnhanced || len(sc.Rules) != 1 ||
		sc.Rules[0].ScanFrequency != ecrtypes.ScanFrequencyContinuousScan {
		t.Fatalf("GetRegistryScanningConfiguration = %+v", sc)
	}

	if len(sc.Rules[0].RepositoryFilters) != 1 ||
		aws.ToString(sc.Rules[0].RepositoryFilters[0].Filter) != "*" {
		t.Fatalf("scanning filter = %+v", sc.Rules[0].RepositoryFilters)
	}
}
