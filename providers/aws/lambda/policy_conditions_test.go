package lambda

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// TestAddPermissionConditions checks every AddPermission option is kept and
// rendered as the Condition AWS returns from GetPolicy.
func TestAddPermissionConditions(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateFunction(ctx, defaultFuncConfig())
	requireNoError(t, err)

	requireNoError(t, m.AddPermission(ctx, "my-func", "", driver.PermissionStatement{
		StatementID: "url", Action: "lambda:InvokeFunctionUrl", Principal: "*", FunctionURLAuthType: "NONE",
	}))
	requireNoError(t, m.AddPermission(ctx, "my-func", "", driver.PermissionStatement{
		StatementID: "via", Action: "lambda:InvokeFunction", Principal: "*", InvokedViaFunctionURL: true,
	}))
	requireNoError(t, m.AddPermission(ctx, "my-func", "", driver.PermissionStatement{
		StatementID: "s3", Action: "lambda:InvokeFunction", Principal: "s3.amazonaws.com",
		SourceARN: "arn:aws:s3:::b", SourceAccount: "123456789012", PrincipalOrgID: "o-abc", EventSourceToken: "tok",
	}))

	err = m.AddPermission(ctx, "my-func", "", driver.PermissionStatement{
		StatementID: "bad", Action: "lambda:InvokeFunctionUrl", Principal: "*", FunctionURLAuthType: "BASIC",
	})
	assertError(t, err, true)

	doc, err := m.GetPolicy(ctx, "my-func", "")
	requireNoError(t, err)

	var policy struct {
		Statement []struct {
			Sid       string
			Condition map[string]map[string]string
		}
	}

	requireNoError(t, json.Unmarshal([]byte(doc), &policy))

	got := map[string]map[string]map[string]string{}
	for _, s := range policy.Statement {
		got[s.Sid] = s.Condition
	}

	assertEqual(t, "NONE", got["url"]["StringEquals"]["lambda:FunctionUrlAuthType"])
	assertEqual(t, "true", got["via"]["Bool"]["lambda:InvokedViaFunctionUrl"])
	assertEqual(t, "arn:aws:s3:::b", got["s3"]["ArnLike"]["AWS:SourceArn"])
	assertEqual(t, "123456789012", got["s3"]["StringEquals"]["AWS:SourceAccount"])
	assertEqual(t, "o-abc", got["s3"]["StringEquals"]["aws:PrincipalOrgID"])
	assertEqual(t, "tok", got["s3"]["StringEquals"]["lambda:EventSourceToken"])

	resource, stmts, err := m.PolicyStatements(ctx, "my-func", "")
	requireNoError(t, err)
	assertEqual(t, 3, len(stmts))
	assertNotEmpty(t, resource)

	_, stmts, err = m.PolicyStatements(ctx, "my-func", "prod")
	requireNoError(t, err)
	assertEqual(t, 0, len(stmts))

	_, _, err = m.PolicyStatements(ctx, "missing", "")
	assertError(t, err, true)
}
