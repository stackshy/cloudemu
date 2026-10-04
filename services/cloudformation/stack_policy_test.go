package cloudformation_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

func TestParseStackPolicyRejectsBadDocuments(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"not json", `{"Statement":`, "not valid JSON"},
		{"no statement", `{}`, "Statement"},
		{"bad effect", `{"Statement":[{"Effect":"Maybe","Action":"Update:*","Principal":"*","Resource":"*"}]}`, "Effect"},
		{"bad action", `{"Statement":[{"Effect":"Allow","Action":"Update:Rename","Principal":"*","Resource":"*"}]}`, "Update:Rename"},
		{"no action", `{"Statement":[{"Effect":"Allow","Principal":"*","Resource":"*"}]}`, "Action"},
		{"both actions", `{"Statement":[{"Effect":"Allow","Action":"Update:*","NotAction":"Update:Delete",` +
			`"Principal":"*","Resource":"*"}]}`, "Action"},
		{"principal not star", `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"me","Resource":"*"}]}`, "Principal"},
		{"no principal", `{"Statement":[{"Effect":"Allow","Action":"Update:*","Resource":"*"}]}`, "Principal"},
		{"bad resource", `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"Table"}]}`, "Table"},
		{"no resource", `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*"}]}`, "Resource"},
		{"bad condition", `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*",` +
			`"Condition":{"StringNotEquals":{"ResourceType":["AWS::S3::Bucket"]}}}]}`, "StringNotEquals"},
		{"bad condition key", `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*",` +
			`"Condition":{"StringEquals":{"LogicalId":["A"]}}}]}`, "LogicalId"},
		{"too long", `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*",` +
			`"Sid":"` + strings.Repeat("x", 16384) + `"}]}`, "16384"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cfn.ParseStackPolicy(tc.body)
			require.Error(t, err)
			assert.True(t, cerrors.IsInvalidArgument(err), "a ValidationError")
			assert.Contains(t, cerrors.Message(err), tc.want)
		})
	}
}

const tableDenyReplace = `{"Statement":[
	{"Effect":"Deny","Action":"Update:Replace","Principal":"*","Resource":"LogicalResourceId/Table"},
	{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}
]}`

func TestStackPolicyEvaluation(t *testing.T) {
	tests := []struct {
		name, policy, action, id, rtype string
		allowed                         bool
		reason                          string
	}{
		{"explicit deny", tableDenyReplace, cfn.StackPolicyReplace, "Table", "AWS::DynamoDB::Table", false,
			"Action denied by stack policy: Statement [#1] does not allow [Update:Replace] for resource [LogicalResourceId/Table]"},
		{"deny does not cover modify", tableDenyReplace, cfn.StackPolicyModify, "Table", "AWS::DynamoDB::Table", true, ""},
		{"other resource allowed", tableDenyReplace, cfn.StackPolicyReplace, "Bucket", "AWS::S3::Bucket", true, ""},
		{"implicit deny", `{"Statement":{"Effect":"Allow","Action":"Update:Modify","Principal":"*",` +
			`"Resource":"LogicalResourceId/Bucket"}}`, cfn.StackPolicyModify, "Table", "AWS::DynamoDB::Table", false,
			"Action denied by stack policy: No statement allows [Update:Modify] for resource [LogicalResourceId/Table]"},
		{"wildcard logical id", `{"Statement":[{"Effect":"Allow","Action":["Update:Modify","Update:Replace"],` +
			`"Principal":"*","Resource":["LogicalResourceId/Crit*"]}]}`, cfn.StackPolicyReplace, "CriticalDB", "T", true, ""},
		{"not action", `{"Statement":[{"Effect":"Allow","NotAction":"Update:Delete","Principal":"*","Resource":"*"}]}`,
			cfn.StackPolicyDelete, "Table", "T", false,
			"Action denied by stack policy: No statement allows [Update:Delete] for resource [LogicalResourceId/Table]"},
		{"not action allows others", `{"Statement":[{"Effect":"Allow","NotAction":"Update:Delete","Principal":"*",` +
			`"Resource":"*"}]}`, cfn.StackPolicyModify, "Table", "T", true, ""},
		{"deny by type", `{"Statement":[
			{"Effect":"Deny","Action":"Update:*","Principal":"*","Resource":"*",
			 "Condition":{"StringEquals":{"ResourceType":["AWS::DynamoDB::Table"]}}},
			{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}]}`,
			cfn.StackPolicyModify, "Table", "AWS::DynamoDB::Table", false,
			"Action denied by stack policy: Statement [#1] does not allow [Update:Modify] for resource [LogicalResourceId/Table]"},
		{"type like", `{"Statement":[
			{"Effect":"Deny","Action":"Update:*","Principal":"*","Resource":"*",
			 "Condition":{"StringLike":{"ResourceType":"AWS::EC2::*"}}},
			{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}]}`,
			cfn.StackPolicyModify, "Table", "AWS::DynamoDB::Table", true, ""},
		{"deny not resource", `{"Statement":[
			{"Effect":"Deny","Action":"Update:*","Principal":"*","NotResource":"LogicalResourceId/Bucket"},
			{"Effect":"Allow","Action":"Update:*","Principal":"*","Resource":"*"}]}`,
			cfn.StackPolicyModify, "Table", "T", false,
			"Action denied by stack policy: Statement [#1] does not allow [Update:Modify] for resource [LogicalResourceId/Table]"},
		// An Allow with NotResource does not protect the excluded resource:
		// its type is still allowed, as the AWS guide warns.
		{"allow not resource does not protect", `{"Statement":[{"Effect":"Allow","Action":"Update:*","Principal":"*",` +
			`"NotResource":"LogicalResourceId/Table"}]}`, cfn.StackPolicyModify, "Table", "T", true, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := cfn.ParseStackPolicy(tc.policy)
			require.NoError(t, err)

			reason, ok := p.Allows(tc.action, tc.id, tc.rtype)
			assert.Equal(t, tc.allowed, ok)
			assert.Equal(t, tc.reason, reason)
		})
	}
}
