package aps_test

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsamp "github.com/aws/aws-sdk-go-v2/service/amp"
	amptypes "github.com/aws/aws-sdk-go-v2/service/amp/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awsamp.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{APS: cloud.APS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsamp.NewFromConfig(cfg, func(o *awsamp.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func describe(t *testing.T, c *awsamp.Client, id string) *amptypes.WorkspaceDescription {
	t.Helper()

	out, err := c.DescribeWorkspace(context.Background(), &awsamp.DescribeWorkspaceInput{
		WorkspaceId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("DescribeWorkspace: %v", err)
	}

	return out.Workspace
}

func TestSDKWorkspaceLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateWorkspace(ctx, &awsamp.CreateWorkspaceInput{
		Alias: aws.String("prod"),
		Tags:  map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	id := aws.ToString(create.WorkspaceId)
	if id == "" || create.Status.StatusCode != amptypes.WorkspaceStatusCodeActive {
		t.Fatalf("create: id=%q status=%q", id, create.Status.StatusCode)
	}

	d1 := describe(t, c, id)

	if d1.Status.StatusCode != amptypes.WorkspaceStatusCodeActive {
		t.Fatalf("status = %q, want ACTIVE", d1.Status.StatusCode)
	}

	if aws.ToString(d1.Arn) != aws.ToString(create.Arn) {
		t.Fatalf("arn drift: %q vs %q", aws.ToString(d1.Arn), aws.ToString(create.Arn))
	}

	if aws.ToString(d1.PrometheusEndpoint) == "" || d1.CreatedAt == nil {
		t.Fatalf("computed fields missing: %+v", d1)
	}

	if aws.ToString(d1.Alias) != "prod" || d1.Tags["env"] != "test" {
		t.Fatalf("alias/tags: %q %v", aws.ToString(d1.Alias), d1.Tags)
	}

	// Second read: every computed field is byte-identical (drift-free plan).
	d2 := describe(t, c, id)
	if aws.ToString(d2.Arn) != aws.ToString(d1.Arn) ||
		aws.ToString(d2.PrometheusEndpoint) != aws.ToString(d1.PrometheusEndpoint) ||
		!d2.CreatedAt.Equal(*d1.CreatedAt) {
		t.Fatal("computed fields drifted across reads")
	}

	// Update the alias; computed fields stay stable.
	if _, err := c.UpdateWorkspaceAlias(ctx, &awsamp.UpdateWorkspaceAliasInput{
		WorkspaceId: aws.String(id), Alias: aws.String("prod-2"),
	}); err != nil {
		t.Fatalf("UpdateWorkspaceAlias: %v", err)
	}

	d3 := describe(t, c, id)
	if aws.ToString(d3.Alias) != "prod-2" {
		t.Fatalf("alias after update = %q", aws.ToString(d3.Alias))
	}

	if aws.ToString(d3.Arn) != aws.ToString(d1.Arn) || !d3.CreatedAt.Equal(*d1.CreatedAt) {
		t.Fatal("computed fields drifted after alias update")
	}

	// Delete -> 404.
	if _, err := c.DeleteWorkspace(ctx, &awsamp.DeleteWorkspaceInput{WorkspaceId: aws.String(id)}); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	_, err = c.DescribeWorkspace(ctx, &awsamp.DescribeWorkspaceInput{WorkspaceId: aws.String(id)})

	var nf *amptypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("describe after delete: got %v, want ResourceNotFoundException", err)
	}
}

func TestSDKRuleGroupsNamespaceByteStability(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	ws, err := c.CreateWorkspace(ctx, &awsamp.CreateWorkspaceInput{Alias: aws.String("rg")})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	id := aws.ToString(ws.WorkspaceId)
	data := []byte("groups:\n  - name: example\n    rules:\n      - record: up\n        expr: up\n")

	if _, err := c.CreateRuleGroupsNamespace(ctx, &awsamp.CreateRuleGroupsNamespaceInput{
		WorkspaceId: aws.String(id), Name: aws.String("rules"), Data: data,
	}); err != nil {
		t.Fatalf("CreateRuleGroupsNamespace: %v", err)
	}

	got, err := c.DescribeRuleGroupsNamespace(ctx, &awsamp.DescribeRuleGroupsNamespaceInput{
		WorkspaceId: aws.String(id), Name: aws.String("rules"),
	})
	if err != nil {
		t.Fatalf("DescribeRuleGroupsNamespace: %v", err)
	}

	if !bytes.Equal(got.RuleGroupsNamespace.Data, data) {
		t.Fatalf("data not byte-stable:\n got %q\nwant %q", got.RuleGroupsNamespace.Data, data)
	}

	if got.RuleGroupsNamespace.Status.StatusCode != amptypes.RuleGroupsNamespaceStatusCodeActive {
		t.Fatalf("status = %q, want ACTIVE", got.RuleGroupsNamespace.Status.StatusCode)
	}

	// Put replaces the data; the new bytes round-trip exactly.
	data2 := []byte("groups:\n  - name: updated\n")
	if _, err := c.PutRuleGroupsNamespace(ctx, &awsamp.PutRuleGroupsNamespaceInput{
		WorkspaceId: aws.String(id), Name: aws.String("rules"), Data: data2,
	}); err != nil {
		t.Fatalf("PutRuleGroupsNamespace: %v", err)
	}

	got2, err := c.DescribeRuleGroupsNamespace(ctx, &awsamp.DescribeRuleGroupsNamespaceInput{
		WorkspaceId: aws.String(id), Name: aws.String("rules"),
	})
	if err != nil {
		t.Fatalf("DescribeRuleGroupsNamespace after put: %v", err)
	}

	if !bytes.Equal(got2.RuleGroupsNamespace.Data, data2) {
		t.Fatalf("data2 not byte-stable: got %q", got2.RuleGroupsNamespace.Data)
	}

	list, err := c.ListRuleGroupsNamespaces(ctx, &awsamp.ListRuleGroupsNamespacesInput{
		WorkspaceId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("ListRuleGroupsNamespaces: %v", err)
	}

	if len(list.RuleGroupsNamespaces) != 1 {
		t.Fatalf("list = %d, want 1", len(list.RuleGroupsNamespaces))
	}
}

func TestSDKAlertManagerDefinition(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	ws, err := c.CreateWorkspace(ctx, &awsamp.CreateWorkspaceInput{Alias: aws.String("am")})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	id := aws.ToString(ws.WorkspaceId)
	def := []byte("alertmanager_config: |\n  route:\n    receiver: default\n")

	if _, err := c.CreateAlertManagerDefinition(ctx, &awsamp.CreateAlertManagerDefinitionInput{
		WorkspaceId: aws.String(id), Data: def,
	}); err != nil {
		t.Fatalf("CreateAlertManagerDefinition: %v", err)
	}

	got, err := c.DescribeAlertManagerDefinition(ctx, &awsamp.DescribeAlertManagerDefinitionInput{
		WorkspaceId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("DescribeAlertManagerDefinition: %v", err)
	}

	if !bytes.Equal(got.AlertManagerDefinition.Data, def) {
		t.Fatalf("definition not byte-stable: got %q", got.AlertManagerDefinition.Data)
	}

	if got.AlertManagerDefinition.Status.StatusCode != amptypes.AlertManagerDefinitionStatusCodeActive {
		t.Fatalf("status = %q", got.AlertManagerDefinition.Status.StatusCode)
	}
}

func TestSDKLoggingConfiguration(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	ws, err := c.CreateWorkspace(ctx, &awsamp.CreateWorkspaceInput{Alias: aws.String("log")})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	id := aws.ToString(ws.WorkspaceId)

	// Absent logging -> ResourceNotFoundException.
	_, err = c.DescribeLoggingConfiguration(ctx, &awsamp.DescribeLoggingConfigurationInput{
		WorkspaceId: aws.String(id),
	})

	var nf *amptypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("describe absent logging: got %v, want ResourceNotFoundException", err)
	}

	lg := "arn:aws:logs:us-east-1:123456789012:log-group:/aps/prod:*"
	if _, err := c.CreateLoggingConfiguration(ctx, &awsamp.CreateLoggingConfigurationInput{
		WorkspaceId: aws.String(id), LogGroupArn: aws.String(lg),
	}); err != nil {
		t.Fatalf("CreateLoggingConfiguration: %v", err)
	}

	got, err := c.DescribeLoggingConfiguration(ctx, &awsamp.DescribeLoggingConfigurationInput{
		WorkspaceId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("DescribeLoggingConfiguration: %v", err)
	}

	if aws.ToString(got.LoggingConfiguration.LogGroupArn) != lg {
		t.Fatalf("logGroupArn = %q, want %q", aws.ToString(got.LoggingConfiguration.LogGroupArn), lg)
	}

	if aws.ToString(got.LoggingConfiguration.Workspace) != id {
		t.Fatalf("workspace = %q, want %q", aws.ToString(got.LoggingConfiguration.Workspace), id)
	}
}

func TestSDKWorkspaceTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	ws, err := c.CreateWorkspace(ctx, &awsamp.CreateWorkspaceInput{
		Alias: aws.String("tagged"), Tags: map[string]string{"a": "1"},
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	arn := aws.ToString(ws.Arn)

	if _, err := c.TagResource(ctx, &awsamp.TagResourceInput{
		ResourceArn: aws.String(arn), Tags: map[string]string{"b": "2"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	lt, err := c.ListTagsForResource(ctx, &awsamp.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if lt.Tags["a"] != "1" || lt.Tags["b"] != "2" {
		t.Fatalf("tags = %v", lt.Tags)
	}

	if _, err := c.UntagResource(ctx, &awsamp.UntagResourceInput{
		ResourceArn: aws.String(arn), TagKeys: []string{"a"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	lt2, _ := c.ListTagsForResource(ctx, &awsamp.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if _, ok := lt2.Tags["a"]; ok {
		t.Fatalf("tag a not removed: %v", lt2.Tags)
	}
}

func TestSDKListWorkspaces(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	for _, alias := range []string{"prod-a", "prod-b", "dev"} {
		if _, err := c.CreateWorkspace(ctx, &awsamp.CreateWorkspaceInput{Alias: aws.String(alias)}); err != nil {
			t.Fatalf("CreateWorkspace %s: %v", alias, err)
		}
	}

	all, err := c.ListWorkspaces(ctx, &awsamp.ListWorkspacesInput{})
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}

	if len(all.Workspaces) != 3 {
		t.Fatalf("list all = %d, want 3", len(all.Workspaces))
	}

	prod, err := c.ListWorkspaces(ctx, &awsamp.ListWorkspacesInput{Alias: aws.String("prod")})
	if err != nil {
		t.Fatalf("ListWorkspaces alias filter: %v", err)
	}

	if len(prod.Workspaces) != 2 {
		t.Fatalf("list prod = %d, want 2", len(prod.Workspaces))
	}
}
