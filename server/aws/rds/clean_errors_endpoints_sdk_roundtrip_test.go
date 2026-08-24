package rds_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	smithy "github.com/aws/smithy-go"
)

// TestSDKRDSErrorMessageHasNoCodePrefix guards that an RDS fault Message carries
// a clean human sentence with no internal enum prefix (e.g. "NotFound: ..."):
// the machine-readable part is the fault Code, not a token embedded in the
// message.
func TestSDKRDSErrorMessageHasNoCodePrefix(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	_, err := client.DescribeDBInstances(ctx, &awsrds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("ghost"),
	})

	var notFound *rdstypes.DBInstanceNotFoundFault
	if !errors.As(err, &notFound) {
		t.Fatalf("DescribeDBInstances(ghost): got %v, want DBInstanceNotFoundFault", err)
	}

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not a smithy APIError: %v", err)
	}

	if apiErr.ErrorCode() != "DBInstanceNotFound" {
		t.Fatalf("ErrorCode = %q, want DBInstanceNotFound", apiErr.ErrorCode())
	}

	msg := apiErr.ErrorMessage()
	for _, tok := range []string{"NotFound:", "InvalidArgument:", "FailedPrecondition:", "AlreadyExists:"} {
		if strings.Contains(msg, tok) {
			t.Fatalf("fault Message %q embeds internal enum prefix %q", msg, tok)
		}
	}

	if !strings.Contains(msg, "ghost") {
		t.Fatalf("fault Message %q should name the missing instance", msg)
	}
}

// TestSDKRDSCreateInstanceErrorCleanMessage guards the same clean-message
// contract on an already-exists fault from CreateDBInstance.
func TestSDKRDSCreateInstanceErrorCleanMessage(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	in := &awsrds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("dup"),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
	}

	if _, err := client.CreateDBInstance(ctx, in); err != nil {
		t.Fatalf("first CreateDBInstance: %v", err)
	}

	_, err := client.CreateDBInstance(ctx, in)

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("second CreateDBInstance: got %v, want an APIError", err)
	}

	if apiErr.ErrorCode() != "DBInstanceAlreadyExists" {
		t.Fatalf("ErrorCode = %q, want DBInstanceAlreadyExists", apiErr.ErrorCode())
	}

	if strings.Contains(apiErr.ErrorMessage(), "AlreadyExists:") {
		t.Fatalf("fault Message %q embeds internal enum prefix", apiErr.ErrorMessage())
	}
}

// TestSDKRDSAuroraBuiltInEndpoints guards that a freshly created Aurora cluster
// exposes its built-in WRITER and READER endpoints via DescribeDBClusterEndpoints
// even before any custom endpoint is created.
func TestSDKRDSAuroraBuiltInEndpoints(t *testing.T) {
	client := newSDKClient(t)
	ctx := context.Background()

	if _, err := client.CreateDBCluster(ctx, &awsrds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String("fresh-cl"),
		Engine:              aws.String("aurora-postgresql"),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	desc, err := client.DescribeDBClusterEndpoints(ctx, &awsrds.DescribeDBClusterEndpointsInput{
		DBClusterIdentifier: aws.String("fresh-cl"),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusterEndpoints: %v", err)
	}

	if len(desc.DBClusterEndpoints) != 2 {
		t.Fatalf("got %d endpoints, want 2 (WRITER + READER)", len(desc.DBClusterEndpoints))
	}

	byType := map[string]rdstypes.DBClusterEndpoint{}
	for i := range desc.DBClusterEndpoints {
		byType[aws.ToString(desc.DBClusterEndpoints[i].EndpointType)] = desc.DBClusterEndpoints[i]
	}

	writer, ok := byType["WRITER"]
	if !ok {
		t.Fatalf("missing built-in WRITER endpoint; got %v", byType)
	}

	reader, ok := byType["READER"]
	if !ok {
		t.Fatalf("missing built-in READER endpoint; got %v", byType)
	}

	if aws.ToString(writer.DBClusterIdentifier) != "fresh-cl" {
		t.Fatalf("WRITER endpoint cluster id = %q, want fresh-cl", aws.ToString(writer.DBClusterIdentifier))
	}

	if aws.ToString(writer.Endpoint) == "" || aws.ToString(reader.Endpoint) == "" {
		t.Fatalf("built-in endpoints must carry an address: writer=%q reader=%q",
			aws.ToString(writer.Endpoint), aws.ToString(reader.Endpoint))
	}

	if aws.ToString(writer.Endpoint) == aws.ToString(reader.Endpoint) {
		t.Fatalf("writer and reader endpoints should differ: %q", aws.ToString(writer.Endpoint))
	}
}
