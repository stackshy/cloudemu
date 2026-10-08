package iam_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	iamsrv "github.com/stackshy/cloudemu/v2/server/aws/iam"
	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const (
	acct  = "123456789012"
	iamNS = "arn:aws:iam::" + acct + ":"
)

func iamForm(values url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return r
}

// iamCheckHandler returns a handler over an IAM backend holding one entity of
// each kind under the /dev/ path.
func iamCheckHandler(t *testing.T) *iamsrv.Handler {
	t.Helper()

	ctx := context.Background()
	cloud := cloudemu.NewAWS()
	drv := cloud.IAM

	must := func(what string, err error) {
		t.Helper()

		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	_, err := drv.CreateUser(ctx, iamdriver.UserConfig{Name: "alice", Path: "/dev/"})
	must("CreateUser", err)
	_, err = drv.CreateRole(ctx, iamdriver.RoleConfig{Name: "app", Path: "/dev/", AssumeRolePolicyDoc: `{"Version":"2012-10-17","Statement":[]}`})
	must("CreateRole", err)
	_, err = drv.CreateGroup(ctx, iamdriver.GroupConfig{Name: "devs", Path: "/dev/"})
	must("CreateGroup", err)
	_, err = drv.CreateInstanceProfile(ctx, iamdriver.InstanceProfileConfig{Name: "ip", Path: "/dev/"})
	must("CreateInstanceProfile", err)

	return iamsrv.New(drv, acct)
}

func TestIAMChecksEntityARNs(t *testing.T) {
	scope := awsauthz.Scope{AccountID: acct, Region: "us-east-1", Partition: "aws"}
	h := iamCheckHandler(t)

	p := func(kv ...string) url.Values {
		v := url.Values{}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}

		return v
	}

	cases := []struct {
		action  string
		params  url.Values
		want    string // evaluated resource
		message string // resource the deny message names; "" means want
	}{
		// Existing entities: the stored ARN, path included. The message names
		// only what the request sent.
		{"GetUser", p("UserName", "alice"), iamNS + "user/dev/alice", iamNS + "user/alice"},
		{"DeleteUser", p("UserName", "alice"), iamNS + "user/dev/alice", iamNS + "user/alice"},
		{"CreateAccessKey", p("UserName", "alice"), iamNS + "user/dev/alice", iamNS + "user/alice"},
		{"PutUserPolicy", p("UserName", "alice"), iamNS + "user/dev/alice", iamNS + "user/alice"},
		{"ListGroupsForUser", p("UserName", "alice"), iamNS + "user/dev/alice", iamNS + "user/alice"},
		{"EnableMFADevice", p("UserName", "alice"), iamNS + "user/dev/alice", iamNS + "user/alice"},
		{"GetRole", p("RoleName", "app"), iamNS + "role/dev/app", iamNS + "role/app"},
		{"UpdateAssumeRolePolicy", p("RoleName", "app"), iamNS + "role/dev/app", iamNS + "role/app"},
		{"ListInstanceProfilesForRole", p("RoleName", "app"), iamNS + "role/dev/app", iamNS + "role/app"},
		{"TagRole", p("RoleName", "app"), iamNS + "role/dev/app", iamNS + "role/app"},
		{"GetGroup", p("GroupName", "devs"), iamNS + "group/dev/devs", iamNS + "group/devs"},
		{"AddUserToGroup", p("GroupName", "devs", "UserName", "alice"), iamNS + "group/dev/devs", iamNS + "group/devs"},
		{"PutGroupPolicy", p("GroupName", "devs"), iamNS + "group/dev/devs", iamNS + "group/devs"},
		{"GetInstanceProfile", p("InstanceProfileName", "ip"), iamNS + "instance-profile/dev/ip", iamNS + "instance-profile/ip"},
		{"AddRoleToInstanceProfile", p("InstanceProfileName", "ip", "RoleName", "app"),
			iamNS + "instance-profile/dev/ip", iamNS + "instance-profile/ip"},
		// Missing entities: built from the request's Path and name.
		{"CreateUser", p("UserName", "carol", "Path", "/ops/"), iamNS + "user/ops/carol", ""},
		{"CreateUser", p("UserName", "carol"), iamNS + "user/carol", ""},
		{"DeleteUser", p("UserName", "nobody"), iamNS + "user/nobody", ""},
		{"CreateRole", p("RoleName", "r2", "Path", "/svc/"), iamNS + "role/svc/r2", ""},
		{"CreateGroup", p("GroupName", "g2"), iamNS + "group/g2", ""},
		{"CreateInstanceProfile", p("InstanceProfileName", "ip2", "Path", "/a/b/"), iamNS + "instance-profile/a/b/ip2", ""},
		{"CreatePolicy", p("PolicyName", "pol", "Path", "/team/"), iamNS + "policy/team/pol", ""},
		{"CreateVirtualMFADevice", p("VirtualMFADeviceName", "m1"), iamNS + "mfa/m1", ""},
		// ARN parameters, accepted only in this account (or AWS managed).
		{"GetPolicy", p("PolicyArn", iamNS+"policy/team/pol"), iamNS + "policy/team/pol", ""},
		{"DeletePolicyVersion", p("PolicyArn", iamNS+"policy/pol"), iamNS + "policy/pol", ""},
		{"ListEntitiesForPolicy", p("PolicyArn", "arn:aws:iam::aws:policy/ReadOnlyAccess"), "arn:aws:iam::aws:policy/ReadOnlyAccess", ""},
		{"GetPolicy", p("PolicyArn", "arn:aws:iam::999999999999:policy/pol"), "", ""},
		{"GetPolicy", p("PolicyArn", iamNS+"user/alice"), "", ""},
		{"DeleteVirtualMFADevice", p("SerialNumber", iamNS+"mfa/m1"), iamNS + "mfa/m1", ""},
		{"SimulatePrincipalPolicy", p("PolicySourceArn", iamNS+"user/dev/alice"), iamNS + "user/dev/alice", ""},
		{"SimulatePrincipalPolicy", p("PolicySourceArn", iamNS+"policy/x"), "", ""},
		// Account-level and list operations.
		{"ListUsers", nil, "*", ""},
		{"ListRoles", nil, "*", ""},
		{"ListPolicies", nil, "*", ""},
		{"GetAccountAuthorizationDetails", nil, "*", ""},
		{"GetAccountSummary", nil, "*", ""},
		{"UpdateAccountPasswordPolicy", nil, "*", ""},
		{"ListVirtualMFADevices", nil, "*", ""},
		{"SimulateCustomPolicy", nil, "*", ""},
		// Anything the request does not name cleanly stays unknown.
		{"GetUser", nil, "", ""},
		{"ListAccessKeys", nil, "", ""},
		{"CreateUser", p("UserName", "bad name"), "", ""},
		{"CreateUser", p("UserName", "carol", "Path", "ops"), "", ""},
		{"CreateServiceLinkedRole", p("AWSServiceName", "elasticache.amazonaws.com"), "", ""},
	}

	for _, tc := range cases {
		v := url.Values{"Action": {tc.action}}
		for k, vals := range tc.params {
			v[k] = vals
		}

		checks, ok := h.IAMChecks(iamForm(v), scope)
		if !ok || len(checks) != 1 {
			t.Errorf("%s %v: got %+v ok=%v", tc.action, tc.params, checks, ok)
			continue
		}

		c := checks[0]
		if c.Action != "iam:"+tc.action || c.Resource != tc.want || c.MessageResource != tc.message {
			t.Errorf("%s %v: got %s on %q (message %q), want %q (message %q)",
				tc.action, tc.params, c.Action, c.Resource, c.MessageResource, tc.want, tc.message)
		}
	}

	// An Action the handler does not serve is still named, with an unknown
	// resource; the handler answers it with InvalidAction.
	checks, ok := h.IAMChecks(iamForm(url.Values{"Action": {"CreateLoginProfile"}, "UserName": {"alice"}}), scope)
	if !ok || len(checks) != 1 || checks[0].Action != "iam:CreateLoginProfile" || checks[0].Resource != "" {
		t.Errorf("unserved action: got %+v ok=%v", checks, ok)
	}

	if checks, ok := h.IAMChecks(iamForm(url.Values{"Version": {"2010-05-08"}}), scope); ok {
		t.Errorf("no Action: got %+v, want ok=false", checks)
	}
}
