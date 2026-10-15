package cloudfront

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cfprovider "github.com/stackshy/cloudemu/v2/providers/aws/cloudfront"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	cfdriver "github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestIAMChecks(t *testing.T) {
	const (
		acct = "123456789012"
		dist = "/2020-05-31/distribution"
		tag  = "/2020-05-31/tagging"
	)

	drv := cfprovider.New(config.NewOptions(config.WithAccountID(acct)))

	d, err := drv.CreateDistribution(context.Background(), &cfdriver.CreateDistributionInput{
		CallerReference: "r", Enabled: true, Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateDistribution: %v", err)
	}

	arn := "arn:aws:cloudfront::" + acct + ":distribution/" + d.ID
	foreign := "arn:aws:cloudfront::999999999999:distribution/" + d.ID

	type check struct{ action, resource string }

	cases := []struct {
		name, method, target, body string
		checks                     []check
		cond                       map[string]string
		unknown                    bool
	}{
		{name: "create", method: "POST", target: dist, checks: []check{{"cloudfront:CreateDistribution", "*"}}},
		{name: "create with tags", method: "POST", target: dist + "?WithTags",
			body: `<DistributionConfigWithTags><DistributionConfig></DistributionConfig>` +
				`<Tags><Items><Tag><Key>team</Key><Value>a</Value></Tag></Items></Tags></DistributionConfigWithTags>`,
			checks: []check{
				{"cloudfront:CreateDistribution", "*"},
				{"cloudfront:TagResource", "arn:aws:cloudfront::" + acct + ":distribution/*"},
			},
			cond: map[string]string{"aws:RequestTag/team": "a", "aws:TagKeys": "team"}},
		{name: "create with tags and a bad body", method: "POST", target: dist + "?WithTags", body: `<x`, unknown: true},
		{name: "list", method: "GET", target: dist, checks: []check{{"cloudfront:ListDistributions", "*"}}},
		{name: "get", method: "GET", target: dist + "/" + d.ID, checks: []check{{"cloudfront:GetDistribution", arn}},
			cond: map[string]string{"aws:ResourceTag/env": "prod"}},
		{name: "delete", method: "DELETE", target: dist + "/" + d.ID, checks: []check{{"cloudfront:DeleteDistribution", arn}}},
		{name: "get config", method: "GET", target: dist + "/" + d.ID + "/config", checks: []check{{"cloudfront:GetDistributionConfig", arn}}},
		{name: "update", method: "PUT", target: dist + "/" + d.ID + "/config", checks: []check{{"cloudfront:UpdateDistribution", arn}}},
		{name: "create invalidation", method: "POST", target: dist + "/" + d.ID + "/invalidation",
			checks: []check{{"cloudfront:CreateInvalidation", arn}}},
		{name: "list invalidations", method: "GET", target: dist + "/" + d.ID + "/invalidation",
			checks: []check{{"cloudfront:ListInvalidations", arn}}},
		{name: "get invalidation", method: "GET", target: dist + "/" + d.ID + "/invalidation/I1",
			checks: []check{{"cloudfront:GetInvalidation", arn}}},
		{name: "list tags", method: "GET", target: tag + "?Resource=" + arn, checks: []check{{"cloudfront:ListTagsForResource", arn}},
			cond: map[string]string{"aws:ResourceTag/env": "prod"}},
		{name: "tag", method: "POST", target: tag + "?Operation=Tag&Resource=" + arn,
			body:   `<Tags><Items><Tag><Key>k</Key><Value>v</Value></Tag></Items></Tags>`,
			checks: []check{{"cloudfront:TagResource", arn}},
			cond:   map[string]string{"aws:RequestTag/k": "v", "aws:TagKeys": "k", "aws:ResourceTag/env": "prod"}},
		{name: "untag", method: "POST", target: tag + "?Operation=Untag&Resource=" + arn,
			body:   `<TagKeys><Items><Key>a</Key><Key>b</Key></Items></TagKeys>`,
			checks: []check{{"cloudfront:UntagResource", arn}},
			cond:   map[string]string{"aws:TagKeys": "a" + iamdriver.ConditionValueSeparator + "b"}},
		{name: "tag with a bad body", method: "POST", target: tag + "?Operation=Tag&Resource=" + arn, body: `<x`, unknown: true},
		{name: "tags of another account", method: "GET", target: tag + "?Resource=" + foreign, unknown: true},
		{name: "tags of a non-distribution ARN", method: "GET", target: tag + "?Resource=arn:aws:s3:::b", unknown: true},
		{name: "unknown tagging operation", method: "POST", target: tag + "?Operation=X&Resource=" + arn, unknown: true},
		{name: "unknown path", method: "GET", target: dist + "/" + d.ID + "/other", unknown: true},
		{name: "wrong method", method: "PATCH", target: dist + "/" + d.ID, unknown: true},
	}

	h := New(drv, WithAccount(acct))
	s := awsauthz.Scope{AccountID: acct, Region: "eu-west-1"}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))

			checks, cond, ok := h.IAMChecksWithContext(r, s)
			if ok == tc.unknown {
				t.Fatalf("ok = %v, want %v", ok, !tc.unknown)
			}

			got := make([]check, 0, len(checks))
			for _, c := range checks {
				if c.Mode != awsauthz.Required {
					t.Errorf("%s mode %d, want Required", c.Action, c.Mode)
				}

				got = append(got, check{c.Action, c.Resource})
			}

			if !tc.unknown && !reflect.DeepEqual(got, tc.checks) {
				t.Errorf("checks = %v, want %v", got, tc.checks)
			}

			for k, v := range tc.cond {
				if cond[k] != v {
					t.Errorf("cond %s = %q, want %q", k, cond[k], v)
				}
			}
		})
	}
}
