package sns_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/aws/sns"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

const (
	t1ARN  = "arn:aws:sns:us-east-1:123456789012:t1"
	t1Sub  = t1ARN + ":sub-abc"
	formCT = "application/x-www-form-urlencoded"
)

func snsForm(values url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", formCT)

	return r
}

func TestIAMChecksTopicARNs(t *testing.T) {
	scope := awsauthz.Scope{AccountID: "123456789012", Region: "us-east-1", Partition: "aws"}

	cases := []struct {
		action, param, value, wantAction, wantResource string
	}{
		{"CreateTopic", "Name", "t1", "sns:CreateTopic", t1ARN},
		{"DeleteTopic", "TopicArn", t1ARN, "sns:DeleteTopic", t1ARN},
		{"GetTopicAttributes", "TopicArn", t1ARN, "sns:GetTopicAttributes", t1ARN},
		{"SetTopicAttributes", "TopicArn", t1ARN, "sns:SetTopicAttributes", t1ARN},
		{"ListTopics", "", "", "sns:ListTopics", "*"},
		{"Subscribe", "TopicArn", t1ARN, "sns:Subscribe", t1ARN},
		{"ConfirmSubscription", "TopicArn", t1ARN, "sns:ConfirmSubscription", t1ARN},
		{"ListSubscriptions", "", "", "sns:ListSubscriptions", "*"},
		{"ListSubscriptionsByTopic", "TopicArn", t1ARN, "sns:ListSubscriptionsByTopic", t1ARN},
		{"Publish", "TopicArn", t1ARN, "sns:Publish", t1ARN},
		{"Publish", "TargetArn", t1ARN, "sns:Publish", t1ARN},
		{"PublishBatch", "TopicArn", t1ARN, "sns:Publish", t1ARN},
		{"PublishBatch", "TargetArn", t1ARN, "sns:Publish", t1ARN},
		{"AddPermission", "TopicArn", t1ARN, "sns:AddPermission", t1ARN},
		{"RemovePermission", "TopicArn", t1ARN, "sns:RemovePermission", t1ARN},
		{"TagResource", "ResourceArn", t1ARN, "sns:TagResource", t1ARN},
		{"UntagResource", "ResourceArn", t1ARN, "sns:UntagResource", t1ARN},
		{"ListTagsForResource", "ResourceArn", t1ARN, "sns:ListTagsForResource", t1ARN},
		// Subscription operations are authorized on the subscription's topic.
		{"Unsubscribe", "SubscriptionArn", t1Sub, "sns:Unsubscribe", t1ARN},
		{"GetSubscriptionAttributes", "SubscriptionArn", t1Sub, "sns:GetSubscriptionAttributes", t1ARN},
		{"SetSubscriptionAttributes", "SubscriptionArn", t1Sub, "sns:SetSubscriptionAttributes", t1ARN},
		// The handler runs on the topic named by the last ARN field, so that is
		// the topic authorized, in the server's own account and region.
		{"Publish", "TopicArn", "arn:aws:sns:eu-west-1:999999999999:t1", "sns:Publish", t1ARN},
		// Nothing named: unknown.
		{"Publish", "", "", "sns:Publish", ""},
		{"DeleteTopic", "TopicArn", "arn:aws:sns:us-east-1:123456789012:", "sns:DeleteTopic", ""},
		{"Unsubscribe", "SubscriptionArn", "sub-abc", "sns:Unsubscribe", ""},
		{"CreateTopic", "Name", "bad name", "sns:CreateTopic", ""},
	}

	h := sns.New(nil)

	for _, tc := range cases {
		v := url.Values{"Action": {tc.action}}
		if tc.param != "" {
			v.Set(tc.param, tc.value)
		}

		checks, ok := h.IAMChecks(snsForm(v), scope)
		if !ok || len(checks) != 1 || checks[0].Action != tc.wantAction || checks[0].Resource != tc.wantResource {
			t.Errorf("%s %s=%s: got %+v ok=%v, want %s on %q", tc.action, tc.param, tc.value, checks, ok, tc.wantAction, tc.wantResource)
		}
	}

	if checks, ok := h.IAMChecks(snsForm(url.Values{"Version": {"1"}}), scope); ok {
		t.Errorf("no Action: got %+v, want ok=false", checks)
	}
}
