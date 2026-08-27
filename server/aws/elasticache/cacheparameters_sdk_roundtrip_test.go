package elasticache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/smithy-go"
)

// createRedis7ParamGroup creates a redis7 cache parameter group named `name`.
func createRedis7ParamGroup(t *testing.T, client *awselasticache.Client, name string) {
	t.Helper()

	if _, err := client.CreateCacheParameterGroup(context.Background(), &awselasticache.CreateCacheParameterGroupInput{
		CacheParameterGroupName:   aws.String(name),
		CacheParameterGroupFamily: aws.String("redis7"),
		Description:               aws.String("custom"),
	}); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}
}

// findParam returns the parameter with the given name, or nil.
func findParam(params []ectypes.Parameter, name string) *ectypes.Parameter {
	for i := range params {
		if aws.ToString(params[i].ParameterName) == name {
			return &params[i]
		}
	}

	return nil
}

// TestSDKDescribeCacheParametersDefaults covers that a freshly created group
// returns a non-empty engine-default parameter list including maxmemory-policy
// with Source=system.
func TestSDKDescribeCacheParametersDefaults(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	createRedis7ParamGroup(t, client, "pg-defaults")

	got, err := client.DescribeCacheParameters(ctx, &awselasticache.DescribeCacheParametersInput{
		CacheParameterGroupName: aws.String("pg-defaults"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheParameters: %v", err)
	}

	if len(got.Parameters) == 0 {
		t.Fatalf("expected a non-empty default parameter list")
	}

	mp := findParam(got.Parameters, "maxmemory-policy")
	if mp == nil {
		t.Fatalf("expected maxmemory-policy in default parameters")
	}

	if aws.ToString(mp.ParameterValue) != "volatile-lru" {
		t.Errorf("maxmemory-policy default = %q, want volatile-lru", aws.ToString(mp.ParameterValue))
	}

	if aws.ToString(mp.Source) != "system" {
		t.Errorf("maxmemory-policy Source = %q, want system", aws.ToString(mp.Source))
	}
}

// TestSDKModifyCacheParametersApplied covers that ModifyCacheParameterGroup with
// ParameterNameValues is reflected on the next DescribeCacheParameters with
// Source=user.
func TestSDKModifyCacheParametersApplied(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	createRedis7ParamGroup(t, client, "pg-modify")

	if _, err := client.ModifyCacheParameterGroup(ctx, &awselasticache.ModifyCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg-modify"),
		ParameterNameValues: []ectypes.ParameterNameValue{
			{ParameterName: aws.String("maxmemory-policy"), ParameterValue: aws.String("allkeys-lru")},
		},
	}); err != nil {
		t.Fatalf("ModifyCacheParameterGroup: %v", err)
	}

	got, err := client.DescribeCacheParameters(ctx, &awselasticache.DescribeCacheParametersInput{
		CacheParameterGroupName: aws.String("pg-modify"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheParameters: %v", err)
	}

	mp := findParam(got.Parameters, "maxmemory-policy")
	if mp == nil {
		t.Fatalf("maxmemory-policy missing after modify")
	}

	if aws.ToString(mp.ParameterValue) != "allkeys-lru" {
		t.Errorf("maxmemory-policy = %q, want allkeys-lru", aws.ToString(mp.ParameterValue))
	}

	if aws.ToString(mp.Source) != "user" {
		t.Errorf("maxmemory-policy Source = %q, want user", aws.ToString(mp.Source))
	}
}

// TestSDKModifyCacheParametersUnknownRejected covers that an unknown parameter
// name is rejected with InvalidParameterValue.
func TestSDKModifyCacheParametersUnknownRejected(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	createRedis7ParamGroup(t, client, "pg-unknown")

	_, err := client.ModifyCacheParameterGroup(ctx, &awselasticache.ModifyCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg-unknown"),
		ParameterNameValues: []ectypes.ParameterNameValue{
			{ParameterName: aws.String("not-a-real-parameter"), ParameterValue: aws.String("x")},
		},
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "InvalidParameterValue" {
		t.Fatalf("unknown parameter must surface InvalidParameterValue, got %T: %v", err, err)
	}
}

// TestSDKDescribeCacheParametersMissingGroup covers that a missing group reports
// CacheParameterGroupNotFound.
func TestSDKDescribeCacheParametersMissingGroup(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.DescribeCacheParameters(ctx, &awselasticache.DescribeCacheParametersInput{
		CacheParameterGroupName: aws.String("does-not-exist"),
	})

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "CacheParameterGroupNotFound" {
		t.Fatalf("missing group must surface CacheParameterGroupNotFound, got %T: %v", err, err)
	}
}

// TestSDKDescribeCacheParametersSourceFilter covers that the Source filter
// narrows the result: user returns only modified parameters, system excludes
// them.
func TestSDKDescribeCacheParametersSourceFilter(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	createRedis7ParamGroup(t, client, "pg-filter")

	if _, err := client.ModifyCacheParameterGroup(ctx, &awselasticache.ModifyCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg-filter"),
		ParameterNameValues: []ectypes.ParameterNameValue{
			{ParameterName: aws.String("maxmemory-policy"), ParameterValue: aws.String("allkeys-lru")},
		},
	}); err != nil {
		t.Fatalf("ModifyCacheParameterGroup: %v", err)
	}

	user, err := client.DescribeCacheParameters(ctx, &awselasticache.DescribeCacheParametersInput{
		CacheParameterGroupName: aws.String("pg-filter"), Source: aws.String("user"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheParameters(user): %v", err)
	}

	if len(user.Parameters) != 1 || findParam(user.Parameters, "maxmemory-policy") == nil {
		t.Fatalf("user filter = %d params, want only maxmemory-policy", len(user.Parameters))
	}

	system, err := client.DescribeCacheParameters(ctx, &awselasticache.DescribeCacheParametersInput{
		CacheParameterGroupName: aws.String("pg-filter"), Source: aws.String("system"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheParameters(system): %v", err)
	}

	if findParam(system.Parameters, "maxmemory-policy") != nil {
		t.Errorf("system filter must exclude the user-modified maxmemory-policy")
	}

	if len(system.Parameters) == 0 {
		t.Errorf("system filter should still return the unmodified defaults")
	}
}

// TestSDKResetCacheParameterGroup covers that ResetCacheParameterGroup restores
// a modified parameter back to its engine default (Source=system).
func TestSDKResetCacheParameterGroup(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	createRedis7ParamGroup(t, client, "pg-reset")

	if _, err := client.ModifyCacheParameterGroup(ctx, &awselasticache.ModifyCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg-reset"),
		ParameterNameValues: []ectypes.ParameterNameValue{
			{ParameterName: aws.String("maxmemory-policy"), ParameterValue: aws.String("allkeys-lru")},
		},
	}); err != nil {
		t.Fatalf("ModifyCacheParameterGroup: %v", err)
	}

	if _, err := client.ResetCacheParameterGroup(ctx, &awselasticache.ResetCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg-reset"),
		ResetAllParameters:      aws.Bool(true),
	}); err != nil {
		t.Fatalf("ResetCacheParameterGroup: %v", err)
	}

	got, err := client.DescribeCacheParameters(ctx, &awselasticache.DescribeCacheParametersInput{
		CacheParameterGroupName: aws.String("pg-reset"),
	})
	if err != nil {
		t.Fatalf("DescribeCacheParameters: %v", err)
	}

	mp := findParam(got.Parameters, "maxmemory-policy")
	if mp == nil {
		t.Fatalf("maxmemory-policy missing after reset")
	}

	if aws.ToString(mp.ParameterValue) != "volatile-lru" || aws.ToString(mp.Source) != "system" {
		t.Errorf("after reset maxmemory-policy = %q/%q, want volatile-lru/system",
			aws.ToString(mp.ParameterValue), aws.ToString(mp.Source))
	}
}
