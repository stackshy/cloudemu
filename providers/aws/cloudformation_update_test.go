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

// A table without an Updater keeps its items when only an in-place property
// changes, and is replaced only when TableName changes.
func TestCFNDynamoTableKeepsItemsOnInPlaceChange(t *testing.T) {
	ctx := context.Background()
	p := New()

	tpl := `{"Parameters":{"Mode":{"Type":"String"},"Env":{"Type":"String"},"Name":{"Type":"String"}},
	"Resources":{"T":{"Type":"AWS::DynamoDB::Table","Properties":{
		"TableName":{"Ref":"Name"},"BillingMode":{"Ref":"Mode"},
		"AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],
		"KeySchema":[{"AttributeName":"id","KeyType":"HASH"}],
		"Tags":[{"Key":"env","Value":{"Ref":"Env"}}]}}}}`

	params := func(mode, env, name string) []cfn.Parameter {
		return []cfn.Parameter{{Key: "Mode", Value: mode}, {Key: "Env", Value: env}, {Key: "Name", Value: name}}
	}

	if _, err := p.CloudFormation.CreateStack(ctx, &cfn.CreateStackInput{
		StackName: "t", TemplateBody: tpl, Parameters: params("PAY_PER_REQUEST", "dev", "orders"),
	}); err != nil {
		t.Fatalf("CreateStack: %v", err)
	}

	if err := p.DynamoDB.PutItem(ctx, "orders", map[string]any{"id": "1"}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	// A tag change, then a BillingMode change, are both in place.
	for _, step := range []struct{ mode, env string }{{"PAY_PER_REQUEST", "prod"}, {"PROVISIONED", "prod"}} {
		st, err := p.CloudFormation.UpdateStack(ctx, &cfn.UpdateStackInput{
			StackName: "t", UsePreviousTemplate: true, Parameters: params(step.mode, step.env, "orders"),
		})
		if err != nil || st.Status != cfn.StatusUpdateComplete {
			t.Fatalf("update %+v: %v", step, err)
		}

		if item, gerr := p.DynamoDB.GetItem(ctx, "orders", map[string]any{"id": "1"}); gerr != nil || item == nil {
			t.Fatalf("update %+v lost the item: %v %v", step, item, gerr)
		}
	}

	if _, err := p.CloudFormation.UpdateStack(ctx, &cfn.UpdateStackInput{
		StackName: "t", UsePreviousTemplate: true, Parameters: params("PROVISIONED", "prod", "orders-v2"),
	}); err != nil {
		t.Fatalf("rename: %v", err)
	}

	tables, err := p.DynamoDB.ListTables(ctx)
	if err != nil || len(tables) != 1 || tables[0] != "orders-v2" {
		t.Fatalf("a TableName change replaces the table, got %v %v", tables, err)
	}
}

// A failed table replacement leaves the old table and its items in place, and
// a replacement that keeps the custom TableName is refused without touching it.
func TestCFNDynamoTableReplacementNeverLosesItems(t *testing.T) {
	ctx := context.Background()
	p := New()

	table := func(name, key string) string {
		return `{"Resources":{"T":{"Type":"AWS::DynamoDB::Table","Properties":{
			"TableName":"` + name + `","BillingMode":"PAY_PER_REQUEST",
			"AttributeDefinitions":[{"AttributeName":"` + key + `","AttributeType":"S"}],
			"KeySchema":[{"AttributeName":"` + key + `","KeyType":"HASH"}]}}}}`
	}

	create := func(stack, name string) {
		t.Helper()

		if _, err := p.CloudFormation.CreateStack(ctx, &cfn.CreateStackInput{StackName: stack, TemplateBody: table(name, "id")}); err != nil {
			t.Fatalf("CreateStack %s: %v", stack, err)
		}
	}

	create("t", "keep2")
	create("other", "taken")

	if err := p.DynamoDB.PutItem(ctx, "keep2", map[string]any{"id": "1"}); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	cases := map[string]string{
		"rename onto a taken name": table("taken", "id"),
		"same name, new key":       table("keep2", "pk"),
	}

	for name, body := range cases {
		st, err := p.CloudFormation.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "t", TemplateBody: body})
		if err != nil || st.Status != cfn.StatusUpdateRollbackComplete {
			t.Fatalf("%s: %v %v", name, st, err)
		}

		if item, gerr := p.DynamoDB.GetItem(ctx, "keep2", map[string]any{"id": "1"}); gerr != nil || item == nil {
			t.Fatalf("%s lost the item: %v %v", name, item, gerr)
		}
	}
}
