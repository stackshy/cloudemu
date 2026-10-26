package cloudwatchlogs

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const subPolicyDoc = `{"DestinationArn":"` + lambdaDestARN + `","FilterPattern":"ERROR"}`

func TestAccountPolicyLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	in := &driver.PutAccountPolicyInput{
		PolicyName: "idx", PolicyType: driver.AccountPolicyFieldIndex,
		PolicyDocument: `{"Fields":["RequestId"]}`, SelectionCriteria: `LogGroupNamePrefix IN ["/app"]`,
	}

	p, err := m.PutAccountPolicy(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, driver.AccountPolicyScopeAll, p.Scope, "scope defaults to ALL")
	assert.Equal(t, m.opts.AccountID, p.AccountID)
	assert.Equal(t, testClockStart(), p.LastUpdatedTime)

	in.PolicyDocument = `{"Fields":["RequestId","TraceId"]}`
	_, err = m.PutAccountPolicy(ctx, in)
	require.NoError(t, err, "update in place")

	got, err := m.DescribeAccountPolicies(ctx, driver.DescribeAccountPoliciesInput{PolicyType: driver.AccountPolicyFieldIndex})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, in.PolicyDocument, got[0].PolicyDocument)
	assert.Equal(t, in.SelectionCriteria, got[0].SelectionCriteria)

	got, err = m.DescribeAccountPolicies(ctx, driver.DescribeAccountPoliciesInput{
		PolicyType: driver.AccountPolicyFieldIndex, PolicyName: "other",
	})
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = m.DescribeAccountPolicies(ctx, driver.DescribeAccountPoliciesInput{
		PolicyType: driver.AccountPolicyFieldIndex, AccountIdentifiers: []string{"111122223333"},
	})
	require.NoError(t, err)
	assert.Empty(t, got, "another account's policies")

	err = m.DeleteAccountPolicy(ctx, "idx", driver.AccountPolicyTransformer)
	assert.True(t, errors.IsNotFound(err), "wrong type: %v", err)

	require.NoError(t, m.DeleteAccountPolicy(ctx, "idx", driver.AccountPolicyFieldIndex))

	err = m.DeleteAccountPolicy(ctx, "idx", driver.AccountPolicyFieldIndex)
	assert.True(t, errors.IsNotFound(err), "second delete: %v", err)
}

func TestPutAccountPolicyValidation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	tests := []struct {
		name string
		in   driver.PutAccountPolicyInput
	}{
		{"no name", driver.PutAccountPolicyInput{PolicyType: driver.AccountPolicyTransformer, PolicyDocument: "[]"}},
		{"aws/ name", driver.PutAccountPolicyInput{PolicyName: "aws/x", PolicyType: driver.AccountPolicyTransformer, PolicyDocument: "[]"}},
		{"bad type", driver.PutAccountPolicyInput{PolicyName: "p", PolicyType: "GLOBAL", PolicyDocument: "[]"}},
		{"bad scope", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicyTransformer, PolicyDocument: "[]", Scope: "SOME",
		}},
		{"no document", driver.PutAccountPolicyInput{PolicyName: "p", PolicyType: driver.AccountPolicyTransformer}},
		{"bad JSON", driver.PutAccountPolicyInput{PolicyName: "p", PolicyType: driver.AccountPolicyTransformer, PolicyDocument: "[{"}},
		{"bad data protection", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicyDataProtection, PolicyDocument: `{"Statement":[]}`,
		}},
		{"criteria on data protection", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicyDataProtection, PolicyDocument: dpPolicy("EmailAddress"),
			SelectionCriteria: `LogGroupName NOT IN ["a"]`,
		}},
		{"subscription without destination", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicySubscriptionFilter, PolicyDocument: `{"FilterPattern":"x"}`,
		}},
		{"subscription bad pattern", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicySubscriptionFilter,
			PolicyDocument: `{"DestinationArn":"` + lambdaDestARN + `","FilterPattern":"{ $.a = "}`,
		}},
		{"subscription bad distribution", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicySubscriptionFilter,
			PolicyDocument: `{"DestinationArn":"` + lambdaDestARN + `","Distribution":"Even"}`,
		}},
		{"subscription IN criteria", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicySubscriptionFilter, PolicyDocument: subPolicyDoc,
			SelectionCriteria: `LogGroupName IN ["a"]`,
		}},
		{"unparseable criteria", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicyTransformer, PolicyDocument: "[]",
			SelectionCriteria: `LogGroupNamePrefix LIKE "a"`,
		}},
		{"field index without fields", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicyFieldIndex, PolicyDocument: `{"Fields":[]}`,
		}},
		{"field index duplicate field", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicyFieldIndex,
			PolicyDocument: `{"Fields":["a"],"FieldsV2":{"a":{"type":"FACET"}}}`,
		}},
		{"field index bad type", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicyFieldIndex, PolicyDocument: `{"FieldsV2":{"a":{"type":"X"}}}`,
		}},
		{"metric extraction equals", driver.PutAccountPolicyInput{
			PolicyName: "p", PolicyType: driver.AccountPolicyMetricExtraction, PolicyDocument: `{}`,
			SelectionCriteria: `LogGroupName = "a"`,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.PutAccountPolicy(ctx, &tt.in)
			assert.True(t, errors.IsInvalidArgument(err), "got %v", err)
		})
	}

	_, err := m.DescribeAccountPolicies(ctx, driver.DescribeAccountPoliciesInput{})
	assert.True(t, errors.IsInvalidArgument(err), "describe without type: %v", err)

	_, err = m.DescribeAccountPolicies(ctx, driver.DescribeAccountPoliciesInput{
		PolicyType: driver.AccountPolicyTransformer, AccountIdentifiers: []string{"12"},
	})
	assert.True(t, errors.IsInvalidArgument(err), "bad account id: %v", err)
}

func TestAccountPolicyQuotas(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	put := func(name, typ, doc, crit string) error {
		_, err := m.PutAccountPolicy(ctx, &driver.PutAccountPolicyInput{
			PolicyName: name, PolicyType: typ, PolicyDocument: doc, SelectionCriteria: crit,
		})

		return err
	}

	require.NoError(t, put("dp1", driver.AccountPolicyDataProtection, dpPolicy("EmailAddress"), ""))
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(put("dp2", driver.AccountPolicyDataProtection, dpPolicy("EmailAddress"), "")))

	require.NoError(t, put("s1", driver.AccountPolicySubscriptionFilter, subPolicyDoc, ""))
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(put("s2", driver.AccountPolicySubscriptionFilter, subPolicyDoc, "")))

	require.NoError(t, put("t-all", driver.AccountPolicyTransformer, "[]", ""))
	assert.Equal(t, errors.ResourceExhausted, errors.GetCode(put("t-all2", driver.AccountPolicyTransformer, "[]", "")))

	for i := range maxScopedTransformer {
		require.NoError(t, put("t"+strconv.Itoa(i), driver.AccountPolicyTransformer, "[]",
			`LogGroupNamePrefix IN ["/g`+strconv.Itoa(i)+`x"]`))
	}

	assert.Equal(t, errors.ResourceExhausted,
		errors.GetCode(put("t-over", driver.AccountPolicyTransformer, "[]", `LogGroupNamePrefix IN ["/new"]`)))

	err := put("t-overlap", driver.AccountPolicyTransformer, "[]", `LogGroupNamePrefix = "/g1xyz"`)
	assert.True(t, errors.IsInvalidArgument(err), "overlapping prefix: %v", err)

	require.NoError(t, put("ds", driver.AccountPolicyFieldIndex, `{"Fields":["a"]}`,
		`DataSourceName = "amazon_vpc" AND DataSourceType = "flow"`))
	err = put("ds2", driver.AccountPolicyFieldIndex, `{"Fields":["a"]}`,
		`DataSourceName = "amazon_vpc" AND DataSourceType = "flow"`)
	assert.True(t, errors.IsInvalidArgument(err), "duplicate data source: %v", err)

	require.NoError(t, put("me1", driver.AccountPolicyMetricExtraction, `{}`, `LogGroupNamePrefix NOT IN ["/aws"]`))
	err = put("me2", driver.AccountPolicyMetricExtraction, `{}`, `LogGroupName NOT IN ["x"]`)
	assert.Error(t, err, "a second NOT IN policy")
}

func TestAccountSubscriptionPolicyDelivers(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	inv := &recordingInvoker{}
	m.SetLambdaInvoker(inv)
	setupGroupAndStream(t, m)

	_, err := m.CreateLogGroup(ctx, driver.LogGroupConfig{Name: "excluded"})
	require.NoError(t, err)
	_, err = m.CreateLogStream(ctx, "excluded", "s")
	require.NoError(t, err)

	_, err = m.PutAccountPolicy(ctx, &driver.PutAccountPolicyInput{
		PolicyName: "acct-sub", PolicyType: driver.AccountPolicySubscriptionFilter, PolicyDocument: subPolicyDoc,
		SelectionCriteria: `LogGroupName NOT IN ["excluded", "other"]`,
	})
	require.NoError(t, err)

	ev := []driver.LogEvent{
		{Timestamp: testEventTime(), Message: "ERROR boom"},
		{Timestamp: testEventTime().Add(time.Second), Message: "INFO ok"},
	}
	require.NoError(t, m.PutLogEvents(ctx, "test-group", "test-stream", ev))
	require.NoError(t, m.PutLogEvents(ctx, "excluded", "s", ev))

	require.Len(t, inv.payloads, 1, "only the included group delivers")

	got := decodeAWSLogs(t, inv.payloads[0])
	assert.Equal(t, "test-group", got.LogGroup)
	assert.Equal(t, []string{"acct-sub"}, got.SubscriptionFilters)
	require.Len(t, got.LogEvents, 1)
	assert.Equal(t, "ERROR boom", got.LogEvents[0].Message)
}
