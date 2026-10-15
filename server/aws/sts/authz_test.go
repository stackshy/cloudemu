package sts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

func TestIAMChecks(t *testing.T) {
	cloud := cloudemu.NewAWS()
	if _, err := cloud.IAM.CreateRole(context.Background(), iamdriver.RoleConfig{
		Name: "deploy", Path: "/team/", AssumeRolePolicyDoc: `{"Statement":[]}`,
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	h := New("123456789012", "us-east-1", cloud.IAM)
	role := "arn:aws:iam::123456789012:role/team/deploy"
	other := "arn:aws:iam::999999999999:role/other/deploy"
	missing := "arn:aws:iam::123456789012:role/missing"

	assume := func(action, resource, requested string) []awsauthz.Check {
		return []awsauthz.Check{{Action: action, Resource: resource, MessageResource: requested}}
	}

	trusted := func(resource, requested string, actions ...string) []awsauthz.Check {
		checks := make([]awsauthz.Check, 0, len(actions))
		for _, a := range actions {
			checks = append(checks, awsauthz.Check{Action: a, Resource: resource, Mode: awsauthz.ResourcePolicy, MessageResource: requested})
		}

		return checks
	}

	cases := []struct {
		body  string
		want  []awsauthz.Check
		known bool
	}{
		{"Action=GetCallerIdentity", []awsauthz.Check{}, true},
		{"Action=GetSessionToken", []awsauthz.Check{{Action: "sts:GetSessionToken", Resource: "*", Mode: awsauthz.DenyOnly}}, true},
		{"Action=AssumeRole&RoleArn=" + role, trusted(role, role, "sts:AssumeRole"), true},
		// A RoleArn with another account or path names no role here, so the
		// resource is unknown. A deny still names the RoleArn as sent.
		{"Action=AssumeRole&RoleArn=" + other, trusted("", other, "sts:AssumeRole"), true},
		{"Action=AssumeRole&RoleArn=" + missing, trusted("", missing, "sts:AssumeRole"), true},
		{"Action=AssumeRole&Tags.member.1.Key=k&Tags.member.1.Value=v&SourceIdentity=me&RoleArn=" + role,
			trusted(role, role, "sts:AssumeRole", "sts:TagSession", "sts:SetSourceIdentity"), true},
		{"Action=AssumeRoleWithWebIdentity&RoleArn=" + role, assume("sts:AssumeRoleWithWebIdentity", role, role), true},
		{"Action=AssumeRoleWithSAML&RoleArn=" + role, assume("sts:AssumeRoleWithSAML", role, role), true},
		{"Action=GetFederationToken&Name=bob", awsauthz.Single("sts:GetFederationToken",
			"arn:aws:sts::123456789012:federated-user/bob"), true},
		{"Action=GetAccessKeyInfo", awsauthz.Single("sts:GetAccessKeyInfo", "*"), true},
		{"Action=DecodeAuthorizationMessage", awsauthz.Single("sts:DecodeAuthorizationMessage", "*"), true},
		{"Action=NoSuchAction", awsauthz.Single("sts:NoSuchAction", ""), true},
		{"Version=2011-06-15", nil, false},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", formContentType)

		got, ok := h.IAMChecks(r, awsauthz.Scope{})
		if ok != tc.known || len(got) != len(tc.want) {
			t.Errorf("%s: got %+v ok=%v, want %+v ok=%v", tc.body, got, ok, tc.want, tc.known)
			continue
		}

		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: check %d = %+v, want %+v", tc.body, i, got[i], tc.want[i])
			}
		}
	}

	// Without IAM there is no role to resolve, so the resource is unknown.
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("Action=AssumeRole&RoleArn="+role))
	r.Header.Set("Content-Type", formContentType)

	if got, _ := New("", "", nil).IAMChecks(r, awsauthz.Scope{}); len(got) != 1 || got[0].Resource != "" {
		t.Errorf("no IAM: got %+v, want an unknown resource", got)
	}
}
