package aws

import (
	"context"
	"slices"
	"testing"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// customNameCase is one resource type with a custom physical name and a way
// to check that the named resource still exists.
type customNameCase struct {
	rtype  string
	name   string
	props  string
	exists func(ctx context.Context, p *Provider) bool
}

func customNameCases() []customNameCase {
	return []customNameCase{
		{"AWS::S3::Bucket", "taken-bucket", `{"BucketName":"taken-bucket"}`,
			func(ctx context.Context, p *Provider) bool {
				buckets, err := p.S3.ListBuckets(ctx)
				return err == nil && slices.ContainsFunc(buckets, func(b storagedriver.BucketInfo) bool { return b.Name == "taken-bucket" })
			}},
		{"AWS::DynamoDB::Table", "taken-table", `{"TableName":"taken-table","BillingMode":"PAY_PER_REQUEST",
			"AttributeDefinitions":[{"AttributeName":"id","AttributeType":"S"}],
			"KeySchema":[{"AttributeName":"id","KeyType":"HASH"}]}`,
			func(ctx context.Context, p *Provider) bool {
				_, err := p.DynamoDB.DescribeTable(ctx, "taken-table")
				return err == nil
			}},
		{"AWS::SQS::Queue", "taken-queue", `{"QueueName":"taken-queue"}`,
			func(ctx context.Context, p *Provider) bool {
				queues, err := p.SQS.ListQueues(ctx, "taken-queue")
				return err == nil && len(queues) == 1
			}},
		{"AWS::SNS::Topic", "taken-topic", `{"TopicName":"taken-topic"}`,
			func(ctx context.Context, p *Provider) bool {
				topics, err := p.SNS.ListTopics(ctx, scope.Scope{})
				return err == nil && slices.ContainsFunc(topics, func(tp notifdriver.TopicInfo) bool { return tp.Name == "taken-topic" })
			}},
		{"AWS::Lambda::Function", "taken-fn", `{"FunctionName":"taken-fn","Runtime":"python3.12","Handler":"i.h",
			"Role":"arn:aws:iam::123456789012:role/r","Code":{"ZipFile":"def h(e, c): pass"}}`,
			func(ctx context.Context, p *Provider) bool {
				_, err := p.Lambda.GetFunction(ctx, "taken-fn")
				return err == nil
			}},
		{"AWS::IAM::Role", "taken-role", `{"RoleName":"taken-role","AssumeRolePolicyDocument":{"Version":"2012-10-17",
			"Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}}`,
			func(ctx context.Context, p *Provider) bool {
				_, err := p.IAM.GetRole(ctx, "taken-role")
				return err == nil
			}},
		{"AWS::SecretsManager::Secret", "taken-secret", `{"Name":"taken-secret","SecretString":"s"}`,
			func(ctx context.Context, p *Provider) bool {
				_, err := p.SecretsManager.GetSecret(ctx, "taken-secret")
				return err == nil
			}},
		{"AWS::SSM::Parameter", "/taken/param", `{"Name":"/taken/param","Type":"String","Value":"v"}`,
			func(ctx context.Context, p *Provider) bool {
				_, err := p.SSM.GetParameter(ctx, "/taken/param", false)
				return err == nil
			}},
	}
}

// A stack whose custom-named resource already exists fails to create with
// "<name> already exists". It never adopts the resource, so its rollback
// leaves the resource another stack owns in place.
func TestCFNCustomNameTakenNeverAdopts(t *testing.T) {
	for _, tc := range customNameCases() {
		t.Run(tc.rtype, func(t *testing.T) {
			ctx := context.Background()
			p := New()
			body := `{"Resources":{"R":{"Type":"` + tc.rtype + `","Properties":` + tc.props + `}}}`
			caps := []string{cfn.CapabilityNamedIAM}

			if _, err := p.CloudFormation.CreateStack(ctx, &cfn.CreateStackInput{
				StackName: "owner", TemplateBody: body, Capabilities: caps,
			}); err != nil {
				t.Fatalf("owner CreateStack: %v", err)
			}

			st, err := p.CloudFormation.CreateStack(ctx, &cfn.CreateStackInput{
				StackName: "intruder", TemplateBody: body, Capabilities: caps,
			})
			if err != nil {
				t.Fatalf("intruder CreateStack: %v", err)
			}

			if st.Status != cfn.StatusRollbackComplete {
				t.Fatalf("intruder status %s, want ROLLBACK_COMPLETE", st.Status)
			}

			events, err := p.CloudFormation.DescribeStackEvents(ctx, "intruder")
			if err != nil {
				t.Fatalf("DescribeStackEvents: %v", err)
			}

			if !slices.ContainsFunc(events, func(e cfn.StackEvent) bool {
				return e.Status == cfn.ResourceCreateFailed && e.StatusReason == tc.name+" already exists"
			}) {
				t.Fatalf("no CREATE_FAILED %q event in %+v", tc.name+" already exists", events)
			}

			if !tc.exists(ctx, p) {
				t.Fatalf("the owner stack's %s was deleted by the intruder's rollback", tc.name)
			}
		})
	}
}
