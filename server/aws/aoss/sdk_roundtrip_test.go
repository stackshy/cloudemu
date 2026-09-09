package aoss_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsaoss "github.com/aws/aws-sdk-go-v2/service/opensearchserverless"
	aosstypes "github.com/aws/aws-sdk-go-v2/service/opensearchserverless/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const encPolicyJSON = `{"Rules":[{"ResourceType":"collection","Resource":["collection/sdk-logs"]}],"AWSOwnedKey":true}`

const accessPolicyJSON = `[{"Rules":[{"ResourceType":"collection","Resource":["collection/sdk-logs"],` +
	`"Permission":["aoss:*"]}],"Principal":["arn:aws:iam::000000000000:root"]}]`

func newClient(t *testing.T) *awsaoss.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{AOSS: cloud.AOSS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsaoss.NewFromConfig(cfg, func(o *awsaoss.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKCollectionAndPolicyLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	// An encryption security policy is required before a collection can be created.
	secOut, err := c.CreateSecurityPolicy(ctx, &awsaoss.CreateSecurityPolicyInput{
		Name:   aws.String("sdk-enc"),
		Type:   aosstypes.SecurityPolicyTypeEncryption,
		Policy: aws.String(encPolicyJSON),
	})
	if err != nil {
		t.Fatalf("CreateSecurityPolicy: %v", err)
	}

	if secOut.SecurityPolicyDetail == nil || aws.ToString(secOut.SecurityPolicyDetail.PolicyVersion) == "" {
		t.Fatalf("expected a policyVersion on the created security policy")
	}

	firstVersion := aws.ToString(secOut.SecurityPolicyDetail.PolicyVersion)

	// Collection referencing the encryption policy.
	create, err := c.CreateCollection(ctx, &awsaoss.CreateCollectionInput{
		Name:        aws.String("sdk-logs"),
		Type:        aosstypes.CollectionTypeSearch,
		Description: aws.String("managed by test"),
	})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	id := aws.ToString(create.CreateCollectionDetail.Id)
	if id == "" {
		t.Fatalf("expected a collection id")
	}

	// BatchGetCollection must report ACTIVE with stable endpoints so an IaC waiter
	// completes and does not hang.
	get, err := c.BatchGetCollection(ctx, &awsaoss.BatchGetCollectionInput{Ids: []string{id}})
	if err != nil {
		t.Fatalf("BatchGetCollection: %v", err)
	}

	if len(get.CollectionDetails) != 1 {
		t.Fatalf("expected 1 collection detail, got %d (errors: %+v)", len(get.CollectionDetails), get.CollectionErrorDetails)
	}

	detail := get.CollectionDetails[0]
	if detail.Status != aosstypes.CollectionStatusActive {
		t.Fatalf("expected ACTIVE, got %s", detail.Status)
	}

	wantEndpoint := "https://" + id + ".us-east-1.aoss.amazonaws.com"
	if aws.ToString(detail.CollectionEndpoint) != wantEndpoint {
		t.Fatalf("collectionEndpoint = %q, want %q", aws.ToString(detail.CollectionEndpoint), wantEndpoint)
	}

	if aws.ToString(detail.DashboardEndpoint) != wantEndpoint+"/_dashboards" {
		t.Fatalf("dashboardEndpoint = %q", aws.ToString(detail.DashboardEndpoint))
	}

	// A second read is byte-stable on the computed fields.
	get2, err := c.BatchGetCollection(ctx, &awsaoss.BatchGetCollectionInput{Ids: []string{id}})
	if err != nil {
		t.Fatalf("BatchGetCollection#2: %v", err)
	}

	d2 := get2.CollectionDetails[0]
	if aws.ToString(d2.Arn) != aws.ToString(detail.Arn) ||
		aws.ToString(d2.CollectionEndpoint) != aws.ToString(detail.CollectionEndpoint) ||
		aws.ToInt64(d2.CreatedDate) != aws.ToInt64(detail.CreatedDate) {
		t.Fatalf("computed fields drifted across reads")
	}

	// Data access policy.
	if _, err = c.CreateAccessPolicy(ctx, &awsaoss.CreateAccessPolicyInput{
		Name:   aws.String("sdk-access"),
		Type:   aosstypes.AccessPolicyTypeData,
		Policy: aws.String(accessPolicyJSON),
	}); err != nil {
		t.Fatalf("CreateAccessPolicy: %v", err)
	}

	// Update the collection description.
	upd, err := c.UpdateCollection(ctx, &awsaoss.UpdateCollectionInput{
		Id: aws.String(id), Description: aws.String("updated"),
	})
	if err != nil {
		t.Fatalf("UpdateCollection: %v", err)
	}

	if aws.ToString(upd.UpdateCollectionDetail.Description) != "updated" {
		t.Fatalf("description not updated")
	}

	// Update the security policy: a new policyVersion must be minted.
	updSec, err := c.UpdateSecurityPolicy(ctx, &awsaoss.UpdateSecurityPolicyInput{
		Name:          aws.String("sdk-enc"),
		Type:          aosstypes.SecurityPolicyTypeEncryption,
		PolicyVersion: aws.String(firstVersion),
		Description:   aws.String("rotated"),
	})
	if err != nil {
		t.Fatalf("UpdateSecurityPolicy: %v", err)
	}

	if aws.ToString(updSec.SecurityPolicyDetail.PolicyVersion) == firstVersion {
		t.Fatalf("expected policyVersion to change on update")
	}

	// Delete then confirm the collection is gone.
	if _, err = c.DeleteCollection(ctx, &awsaoss.DeleteCollectionInput{Id: aws.String(id)}); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}

	after, err := c.BatchGetCollection(ctx, &awsaoss.BatchGetCollectionInput{Ids: []string{id}})
	if err != nil {
		t.Fatalf("BatchGetCollection after delete: %v", err)
	}

	if len(after.CollectionDetails) != 0 || len(after.CollectionErrorDetails) != 1 {
		t.Fatalf("expected the deleted collection to be reported as missing")
	}
}

func TestSDKListCollections(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	if _, err := c.CreateSecurityPolicy(ctx, &awsaoss.CreateSecurityPolicyInput{
		Name:   aws.String("sdk-enc"),
		Type:   aosstypes.SecurityPolicyTypeEncryption,
		Policy: aws.String(encPolicyJSON),
	}); err != nil {
		t.Fatalf("CreateSecurityPolicy: %v", err)
	}

	if _, err := c.CreateCollection(ctx, &awsaoss.CreateCollectionInput{
		Name: aws.String("sdk-logs"), Type: aosstypes.CollectionTypeSearch,
	}); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	list, err := c.ListCollections(ctx, &awsaoss.ListCollectionsInput{})
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}

	if len(list.CollectionSummaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(list.CollectionSummaries))
	}
}

func TestSDKResourceNotFoundException(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	_, err := c.GetSecurityPolicy(ctx, &awsaoss.GetSecurityPolicyInput{
		Name: aws.String("missing"), Type: aosstypes.SecurityPolicyTypeEncryption,
	})
	if err == nil {
		t.Fatalf("expected an error for a missing policy")
	}

	var nfe *aosstypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}
