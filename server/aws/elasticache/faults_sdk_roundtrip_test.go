package elasticache_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	ectypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/smithy-go"
)

// assertFault checks the SDK error's code, HTTP status, and that its message
// carries no code token.
func assertFault(t *testing.T, op string, err error, wantCode string, wantStatus int) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != wantCode {
		t.Fatalf("%s: got %v, want %s", op, err, wantCode)
	}

	msg := apiErr.ErrorMessage()
	if msg == "" || strings.HasPrefix(msg, wantCode) || strings.Contains(msg, "NotFound: ") ||
		strings.Contains(msg, "AlreadyExists: ") || strings.Contains(msg, "FailedPrecondition: ") {
		t.Fatalf("%s: message %q is empty or leaks a code prefix", op, msg)
	}

	var respErr *awshttp.ResponseError
	if !errors.As(err, &respErr) || respErr.HTTPStatusCode() != wantStatus {
		t.Fatalf("%s: HTTP status = %v, want %d", op, err, wantStatus)
	}
}

// TestTypedFaultsSurviveSDK proves the provider's typed faults reach the SDK as
// its modelled error types, with the botocore status and a prefix-free message.
func TestTypedFaultsSurviveSDK(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	in := &awselasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String("dup"), Engine: aws.String("redis"),
		CacheNodeType: aws.String("cache.t3.micro"), NumCacheNodes: aws.Int32(1),
	}
	if _, err := client.CreateCacheCluster(ctx, in); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	_, err := client.CreateCacheCluster(ctx, in)
	assertFault(t, "CreateCacheCluster(dup)", err, "CacheClusterAlreadyExists", http.StatusBadRequest)

	var exists *ectypes.CacheClusterAlreadyExistsFault
	if !errors.As(err, &exists) {
		t.Fatalf("duplicate create: %T is not *types.CacheClusterAlreadyExistsFault", err)
	}

	_, err = client.DescribeReplicationGroups(ctx, &awselasticache.DescribeReplicationGroupsInput{
		ReplicationGroupId: aws.String("nope"),
	})
	assertFault(t, "DescribeReplicationGroups(nope)", err, "ReplicationGroupNotFoundFault", http.StatusNotFound)

	var rgMissing *ectypes.ReplicationGroupNotFoundFault
	if !errors.As(err, &rgMissing) {
		t.Fatalf("missing group: %T is not *types.ReplicationGroupNotFoundFault", err)
	}

	_, err = client.DeleteCacheSubnetGroup(ctx, &awselasticache.DeleteCacheSubnetGroupInput{
		CacheSubnetGroupName: aws.String("nope"),
	})
	assertFault(t, "DeleteCacheSubnetGroup(nope)", err, "CacheSubnetGroupNotFoundFault", http.StatusBadRequest)

	if _, err = client.CreateCacheParameterGroup(ctx, &awselasticache.CreateCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg"), CacheParameterGroupFamily: aws.String("redis7"),
		Description: aws.String("d"),
	}); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}

	_, err = client.CreateCacheParameterGroup(ctx, &awselasticache.CreateCacheParameterGroupInput{
		CacheParameterGroupName: aws.String("pg"), CacheParameterGroupFamily: aws.String("redis7"),
		Description: aws.String("d"),
	})
	assertFault(t, "CreateCacheParameterGroup(dup)", err, "CacheParameterGroupAlreadyExists", http.StatusBadRequest)
}
