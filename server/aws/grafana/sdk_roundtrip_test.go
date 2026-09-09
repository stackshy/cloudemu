package grafana_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsgrafana "github.com/aws/aws-sdk-go-v2/service/grafana"
	gtypes "github.com/aws/aws-sdk-go-v2/service/grafana/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awsgrafana.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Grafana: cloud.Grafana})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsgrafana.NewFromConfig(cfg, func(o *awsgrafana.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func createInput(name string) *awsgrafana.CreateWorkspaceInput {
	return &awsgrafana.CreateWorkspaceInput{
		AccountAccessType:                 gtypes.AccountAccessTypeCurrentAccount,
		AuthenticationProviders:           []gtypes.AuthenticationProviderTypes{gtypes.AuthenticationProviderTypesAwsSso},
		PermissionType:                    gtypes.PermissionTypeServiceManaged,
		WorkspaceName:                     aws.String(name),
		WorkspaceDescription:              aws.String("managed by test"),
		WorkspaceDataSources:              []gtypes.DataSourceType{gtypes.DataSourceTypeCloudwatch, gtypes.DataSourceTypePrometheus},
		WorkspaceNotificationDestinations: []gtypes.NotificationDestinationType{gtypes.NotificationDestinationTypeSns},
		VpcConfiguration: &gtypes.VpcConfiguration{
			SecurityGroupIds: []string{"sg-0123456789abcdef0"},
			SubnetIds:        []string{"subnet-0123456789abcdef0", "subnet-0123456789abcdef1"},
		},
	}
}

func describe(t *testing.T, c *awsgrafana.Client, id string) *gtypes.WorkspaceDescription {
	t.Helper()

	out, err := c.DescribeWorkspace(context.Background(), &awsgrafana.DescribeWorkspaceInput{WorkspaceId: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeWorkspace: %v", err)
	}

	return out.Workspace
}

func TestSDKWorkspaceLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateWorkspace(ctx, createInput("sdk-ws"))
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	id := aws.ToString(create.Workspace.Id)
	if id == "" {
		t.Fatal("create workspace id empty")
	}

	w1 := describe(t, c, id)

	if string(w1.Status) != "ACTIVE" {
		t.Fatalf("status = %q, want ACTIVE", w1.Status)
	}

	assertComputedPresent(t, w1, id)
	assertConfigRoundTrip(t, w1)

	// Second read: every computed field must be byte-identical.
	w2 := describe(t, c, id)
	assertStable(t, w1, w2)
}

func assertComputedPresent(t *testing.T, w *gtypes.WorkspaceDescription, id string) {
	t.Helper()

	wantEndpoint := id + ".grafana-workspace.us-east-1.amazonaws.com"
	if aws.ToString(w.Endpoint) != wantEndpoint {
		t.Fatalf("endpoint = %q, want %q", aws.ToString(w.Endpoint), wantEndpoint)
	}

	if aws.ToString(w.GrafanaVersion) != "9.4" {
		t.Fatalf("grafanaVersion = %q, want default 9.4", aws.ToString(w.GrafanaVersion))
	}

	if w.Created == nil || w.Modified == nil {
		t.Fatal("created/modified nil")
	}

	if w.Authentication == nil || len(w.Authentication.Providers) != 1 ||
		w.Authentication.Providers[0] != gtypes.AuthenticationProviderTypesAwsSso {
		t.Fatalf("authentication providers = %+v, want [AWS_SSO]", w.Authentication)
	}

	if w.Authentication.SamlConfigurationStatus != gtypes.SamlConfigurationStatusNotConfigured {
		t.Fatalf("samlConfigurationStatus = %q, want NOT_CONFIGURED", w.Authentication.SamlConfigurationStatus)
	}
}

func assertConfigRoundTrip(t *testing.T, w *gtypes.WorkspaceDescription) {
	t.Helper()

	if w.AccountAccessType != gtypes.AccountAccessTypeCurrentAccount {
		t.Fatalf("accountAccessType = %q", w.AccountAccessType)
	}

	if w.PermissionType != gtypes.PermissionTypeServiceManaged {
		t.Fatalf("permissionType = %q", w.PermissionType)
	}

	if aws.ToString(w.Description) != "managed by test" {
		t.Fatalf("description = %q", aws.ToString(w.Description))
	}

	if len(w.DataSources) != 2 {
		t.Fatalf("dataSources = %v, want 2 entries", w.DataSources)
	}

	if w.VpcConfiguration == nil || len(w.VpcConfiguration.SubnetIds) != 2 {
		t.Fatalf("vpcConfiguration not round-tripped: %+v", w.VpcConfiguration)
	}
}

func assertStable(t *testing.T, a, b *gtypes.WorkspaceDescription) {
	t.Helper()

	if aws.ToString(a.Id) != aws.ToString(b.Id) {
		t.Fatalf("id drifted: %q != %q", aws.ToString(a.Id), aws.ToString(b.Id))
	}

	if aws.ToString(a.Endpoint) != aws.ToString(b.Endpoint) {
		t.Fatal("endpoint drifted")
	}

	if aws.ToString(a.GrafanaVersion) != aws.ToString(b.GrafanaVersion) {
		t.Fatal("grafanaVersion drifted")
	}

	if !a.Created.Equal(*b.Created) {
		t.Fatal("created drifted")
	}
}

func TestSDKUpdateStability(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateWorkspace(ctx, createInput("upd-ws"))
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	id := aws.ToString(create.Workspace.Id)
	w1 := describe(t, c, id)

	// Description change goes through UpdateWorkspace.
	if _, err := c.UpdateWorkspace(ctx, &awsgrafana.UpdateWorkspaceInput{
		WorkspaceId:          aws.String(id),
		WorkspaceDescription: aws.String("updated description"),
	}); err != nil {
		t.Fatalf("UpdateWorkspace: %v", err)
	}

	// grafana_version upgrade goes through UpdateWorkspaceConfiguration.
	if _, err := c.UpdateWorkspaceConfiguration(ctx, &awsgrafana.UpdateWorkspaceConfigurationInput{
		WorkspaceId:    aws.String(id),
		Configuration:  aws.String(`{"unifiedAlerting":{"enabled":true}}`),
		GrafanaVersion: aws.String("10.4"),
	}); err != nil {
		t.Fatalf("UpdateWorkspaceConfiguration: %v", err)
	}

	w2 := describe(t, c, id)

	if aws.ToString(w2.Description) != "updated description" {
		t.Fatalf("description after update = %q", aws.ToString(w2.Description))
	}

	if aws.ToString(w2.GrafanaVersion) != "10.4" {
		t.Fatalf("grafanaVersion after config update = %q, want 10.4", aws.ToString(w2.GrafanaVersion))
	}

	// Computed identity fields survive the updates.
	if aws.ToString(w2.Id) != aws.ToString(w1.Id) {
		t.Fatal("id drifted after update")
	}

	if aws.ToString(w2.Endpoint) != aws.ToString(w1.Endpoint) {
		t.Fatal("endpoint drifted after update")
	}

	if !w2.Created.Equal(*w1.Created) {
		t.Fatal("created drifted after update")
	}

	// DescribeWorkspaceConfiguration reflects the same version and configuration.
	cfg, err := c.DescribeWorkspaceConfiguration(ctx, &awsgrafana.DescribeWorkspaceConfigurationInput{
		WorkspaceId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("DescribeWorkspaceConfiguration: %v", err)
	}

	if aws.ToString(cfg.GrafanaVersion) != "10.4" {
		t.Fatalf("config grafanaVersion = %q, want 10.4", aws.ToString(cfg.GrafanaVersion))
	}

	if aws.ToString(cfg.Configuration) != `{"unifiedAlerting":{"enabled":true}}` {
		t.Fatalf("configuration not round-tripped: %q", aws.ToString(cfg.Configuration))
	}
}

func TestSDKConfigurationDefault(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateWorkspace(ctx, createInput("cfg-ws"))
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	id := aws.ToString(create.Workspace.Id)

	cfg, err := c.DescribeWorkspaceConfiguration(ctx, &awsgrafana.DescribeWorkspaceConfigurationInput{
		WorkspaceId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("DescribeWorkspaceConfiguration: %v", err)
	}

	if aws.ToString(cfg.Configuration) != "{}" {
		t.Fatalf("default configuration = %q, want {}", aws.ToString(cfg.Configuration))
	}
}

func TestSDKListAndDelete(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateWorkspace(ctx, createInput("list-ws"))
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	id := aws.ToString(create.Workspace.Id)

	list, err := c.ListWorkspaces(ctx, &awsgrafana.ListWorkspacesInput{})
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}

	if len(list.Workspaces) != 1 || aws.ToString(list.Workspaces[0].Id) != id {
		t.Fatalf("workspaces = %+v, want single %q", list.Workspaces, id)
	}

	if _, err := c.DeleteWorkspace(ctx, &awsgrafana.DeleteWorkspaceInput{WorkspaceId: aws.String(id)}); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	_, err = c.DescribeWorkspace(ctx, &awsgrafana.DescribeWorkspaceInput{WorkspaceId: aws.String(id)})

	var nf *gtypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribeWorkspace after delete: got %v, want ResourceNotFoundException", err)
	}
}

func TestSDKDescribeMissingNotFound(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	// The typed ResourceNotFoundException is selected from the X-Amzn-Errortype
	// header the handler tags on the error response.
	_, err := c.DescribeWorkspace(ctx, &awsgrafana.DescribeWorkspaceInput{WorkspaceId: aws.String("g-deadbeef00")})

	var nf *gtypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribeWorkspace missing: got %v, want ResourceNotFoundException", err)
	}
}

func TestSDKWorkspaceTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateWorkspace(ctx, createInput("tag-ws"))
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	id := aws.ToString(create.Workspace.Id)
	arn := "arn:aws:grafana:us-east-1:000000000000:/workspaces/" + id

	if _, err := c.TagResource(ctx, &awsgrafana.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags:        map[string]string{"team": "obs"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	lt, err := c.ListTagsForResource(ctx, &awsgrafana.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if lt.Tags["team"] != "obs" {
		t.Fatalf("tags = %v, want team=obs", lt.Tags)
	}

	if _, err := c.UntagResource(ctx, &awsgrafana.UntagResourceInput{
		ResourceArn: aws.String(arn),
		TagKeys:     []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	lt2, _ := c.ListTagsForResource(ctx, &awsgrafana.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if _, ok := lt2.Tags["team"]; ok {
		t.Fatal("team tag not removed")
	}
}
