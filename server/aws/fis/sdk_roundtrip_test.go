package fis_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	fisapi "github.com/aws/aws-sdk-go-v2/service/fis"
	fistypes "github.com/aws/aws-sdk-go-v2/service/fis/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *fisapi.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{FIS: cloud.FIS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return fisapi.NewFromConfig(cfg, func(o *fisapi.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func createTemplate(t *testing.T, c *fisapi.Client) *fistypes.ExperimentTemplate {
	t.Helper()

	out, err := c.CreateExperimentTemplate(context.Background(), &fisapi.CreateExperimentTemplateInput{
		ClientToken: aws.String("tok-1"),
		Description: aws.String("network latency test"),
		RoleArn:     aws.String("arn:aws:iam::123456789012:role/fis-role"),
		StopConditions: []fistypes.CreateExperimentTemplateStopConditionInput{
			{Source: aws.String("none")},
		},
		Actions: map[string]fistypes.CreateExperimentTemplateActionInput{
			"inject": {
				ActionId:   aws.String("aws:ec2:stop-instances"),
				Targets:    map[string]string{"Instances": "targetInstances"},
				Parameters: map[string]string{"startInstancesAfterDuration": "PT2M"},
			},
		},
		Targets: map[string]fistypes.CreateExperimentTemplateTargetInput{
			"targetInstances": {
				ResourceType:  aws.String("aws:ec2:instance"),
				SelectionMode: aws.String("COUNT(1)"),
				ResourceTags:  map[string]string{"env": "test"},
			},
		},
		Tags: map[string]string{"team": "chaos"},
	})
	if err != nil {
		t.Fatalf("CreateExperimentTemplate: %v", err)
	}

	return out.ExperimentTemplate
}

func TestSDKTemplateLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	created := createTemplate(t, c)

	if aws.ToString(created.Id) == "" || aws.ToString(created.Arn) == "" {
		t.Fatalf("create returned empty id/arn: %+v", created)
	}

	if created.CreationTime == nil || created.LastUpdateTime == nil {
		t.Fatal("create returned nil creationTime/lastUpdateTime")
	}

	got, err := c.GetExperimentTemplate(ctx, &fisapi.GetExperimentTemplateInput{Id: created.Id})
	if err != nil {
		t.Fatalf("GetExperimentTemplate: %v", err)
	}

	tpl := got.ExperimentTemplate

	// Computed fields are stable between create and get.
	if aws.ToString(tpl.Arn) != aws.ToString(created.Arn) || !tpl.CreationTime.Equal(*created.CreationTime) {
		t.Fatal("computed fields drifted between create and get")
	}

	// The action and target blocks round-trip verbatim.
	if aws.ToString(tpl.Actions["inject"].ActionId) != "aws:ec2:stop-instances" {
		t.Fatalf("action id not preserved: %+v", tpl.Actions)
	}

	if aws.ToString(tpl.Targets["targetInstances"].SelectionMode) != "COUNT(1)" {
		t.Fatalf("target block not preserved: %+v", tpl.Targets)
	}

	// Update the description; identity is stable.
	upd, err := c.UpdateExperimentTemplate(ctx, &fisapi.UpdateExperimentTemplateInput{
		Id:          created.Id,
		Description: aws.String("updated"),
	})
	if err != nil {
		t.Fatalf("UpdateExperimentTemplate: %v", err)
	}

	if aws.ToString(upd.ExperimentTemplate.Description) != "updated" {
		t.Fatalf("description not updated: %q", aws.ToString(upd.ExperimentTemplate.Description))
	}

	if aws.ToString(upd.ExperimentTemplate.Arn) != aws.ToString(created.Arn) {
		t.Fatal("arn changed on update")
	}

	list, err := c.ListExperimentTemplates(ctx, &fisapi.ListExperimentTemplatesInput{})
	if err != nil {
		t.Fatalf("ListExperimentTemplates: %v", err)
	}

	if len(list.ExperimentTemplates) != 1 || aws.ToString(list.ExperimentTemplates[0].Id) != aws.ToString(created.Id) {
		t.Fatalf("list = %+v", list.ExperimentTemplates)
	}

	// Delete, then a get is ResourceNotFoundException.
	if _, err := c.DeleteExperimentTemplate(ctx, &fisapi.DeleteExperimentTemplateInput{Id: created.Id}); err != nil {
		t.Fatalf("DeleteExperimentTemplate: %v", err)
	}

	_, err = c.GetExperimentTemplate(ctx, &fisapi.GetExperimentTemplateInput{Id: created.Id})

	var nf *fistypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("get after delete err = %v, want ResourceNotFoundException", err)
	}
}

func TestSDKExperimentLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	tpl := createTemplate(t, c)

	started, err := c.StartExperiment(ctx, &fisapi.StartExperimentInput{
		ClientToken:          aws.String("tok-2"),
		ExperimentTemplateId: tpl.Id,
	})
	if err != nil {
		t.Fatalf("StartExperiment: %v", err)
	}

	exp := started.Experiment
	if aws.ToString(exp.Id) == "" || exp.State == nil || exp.State.Status != fistypes.ExperimentStatusRunning {
		t.Fatalf("start returned %+v", exp.State)
	}

	if aws.ToString(exp.ExperimentTemplateId) != aws.ToString(tpl.Id) {
		t.Fatal("experiment did not reference its template")
	}

	got, err := c.GetExperiment(ctx, &fisapi.GetExperimentInput{Id: exp.Id})
	if err != nil {
		t.Fatalf("GetExperiment: %v", err)
	}

	if got.Experiment.State.Status != fistypes.ExperimentStatusRunning || got.Experiment.StartTime == nil {
		t.Fatalf("get experiment state = %+v", got.Experiment.State)
	}

	stopped, err := c.StopExperiment(ctx, &fisapi.StopExperimentInput{Id: exp.Id})
	if err != nil {
		t.Fatalf("StopExperiment: %v", err)
	}

	if stopped.Experiment.State.Status != fistypes.ExperimentStatusStopped {
		t.Fatalf("stopped state = %q", stopped.Experiment.State.Status)
	}

	list, err := c.ListExperiments(ctx, &fisapi.ListExperimentsInput{})
	if err != nil {
		t.Fatalf("ListExperiments: %v", err)
	}

	if len(list.Experiments) != 1 || aws.ToString(list.Experiments[0].Id) != aws.ToString(exp.Id) {
		t.Fatalf("list experiments = %+v", list.Experiments)
	}
}

func TestSDKTagging(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	tpl := createTemplate(t, c)

	if _, err := c.TagResource(ctx, &fisapi.TagResourceInput{
		ResourceArn: tpl.Arn,
		Tags:        map[string]string{"owner": "sre"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &fisapi.ListTagsForResourceInput{ResourceArn: tpl.Arn})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	// The create-time tag and the added tag are both present.
	if tags.Tags["team"] != "chaos" || tags.Tags["owner"] != "sre" {
		t.Fatalf("tags = %+v", tags.Tags)
	}

	if _, err := c.UntagResource(ctx, &fisapi.UntagResourceInput{
		ResourceArn: tpl.Arn,
		TagKeys:     []string{"owner"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags, err = c.ListTagsForResource(ctx, &fisapi.ListTagsForResourceInput{ResourceArn: tpl.Arn})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if _, ok := tags.Tags["owner"]; ok {
		t.Fatalf("owner tag not removed: %+v", tags.Tags)
	}
}
