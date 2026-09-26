package aws

import (
	"context"
	"testing"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// The SQS and SSM provisioners update in place. Only a new name replaces.
func TestCFNQueueAndParameterUpdateInPlace(t *testing.T) {
	ctx := context.Background()
	p := New()

	stack := func(body string) {
		t.Helper()

		var err error
		if _, err = p.CloudFormation.DescribeStacks(ctx, "s"); err != nil {
			_, err = p.CloudFormation.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: body})
		} else {
			_, err = p.CloudFormation.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: body})
		}

		if err != nil {
			t.Fatalf("stack: %v", err)
		}
	}

	stack(`{"Resources":{
		"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"q1","VisibilityTimeout":45,"DelaySeconds":5}},
		"P":{"Type":"AWS::SSM::Parameter","Properties":{"Name":"/p","Type":"String","Value":"a","Description":"d"}}}}`)

	res, err := p.CloudFormation.DescribeStackResources(ctx, "s")
	if err != nil {
		t.Fatalf("DescribeStackResources: %v", err)
	}

	physical := map[string]string{}
	for _, r := range res {
		physical[r.LogicalID] = r.PhysicalID
	}

	stack(`{"Resources":{
		"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"q1","VisibilityTimeout":60}},
		"P":{"Type":"AWS::SSM::Parameter","Properties":{"Name":"/p","Type":"String","Value":"b"}}}}`)

	attrs, err := p.SQS.GetQueueAttributes(ctx, physical["Q"])
	if err != nil || attrs.VisibilityTimeout != 60 || attrs.DelaySeconds != 0 {
		t.Fatalf("queue attributes: %+v %v", attrs, err)
	}

	param, err := p.SSM.GetParameter(ctx, "/p", false)
	if err != nil || param.Value != "b" || param.Version != 2 || param.Description != "" {
		t.Fatalf("parameter: %+v %v", param, err)
	}

	res, err = p.CloudFormation.DescribeStackResources(ctx, "s")
	if err != nil {
		t.Fatalf("DescribeStackResources: %v", err)
	}

	for _, r := range res {
		if r.Status != cfn.ResourceUpdateComplete || r.PhysicalID != physical[r.LogicalID] {
			t.Fatalf("%s: %s %s, want UPDATE_COMPLETE on the same resource", r.LogicalID, r.Status, r.PhysicalID)
		}
	}
}
