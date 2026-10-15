package lambda

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/server/authctx"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

func TestPrincipalGranted(t *testing.T) {
	user := &authctx.Principal{ARN: "arn:aws:iam::123456789012:user/path/u"}
	session := &authctx.Principal{ARN: "arn:aws:sts::123456789012:assumed-role/app/s1"}

	cases := []struct {
		principal string
		caller    *authctx.Principal
		want      bool
	}{
		{"*", nil, true},
		{"*", user, true},
		{"arn:aws:iam::123456789012:user/path/u", user, true},
		{"arn:aws:iam::123456789012:user/u", user, false},
		{"123456789012", user, false},
		{"arn:aws:iam::123456789012:root", user, false},
		{"s3.amazonaws.com", user, false},
		{"arn:aws:iam::123456789012:role/svc/app", session, false},
		{"arn:aws:iam::999999999999:role/app", session, false},
		{"arn:aws:iam::123456789012:role/other", session, false},
		{"arn:aws:sts::123456789012:assumed-role/app/s1", session, true},
		{"arn:aws:iam::123456789012:user/path/u", nil, false},
	}

	for _, tc := range cases {
		if got := principalGranted(tc.principal, tc.caller); got != tc.want {
			t.Errorf("principalGranted(%q, %v) = %v, want %v", tc.principal, tc.caller, got, tc.want)
		}
	}
}

func TestConditionsHold(t *testing.T) {
	urlCtx := map[string]string{condFunctionURLAuth: urlAuthNone, condInvokedViaURL: "true"}

	cases := []struct {
		name string
		st   sdrv.PermissionStatement
		cctx map[string]string
		want bool
	}{
		{"no condition", sdrv.PermissionStatement{}, nil, true},
		{"auth type matches", sdrv.PermissionStatement{FunctionURLAuthType: "NONE"}, urlCtx, true},
		{"auth type differs", sdrv.PermissionStatement{FunctionURLAuthType: "AWS_IAM"}, urlCtx, false},
		{"via URL on a URL call", sdrv.PermissionStatement{InvokedViaFunctionURL: true}, urlCtx, true},
		{"via URL on a direct call", sdrv.PermissionStatement{InvokedViaFunctionURL: true}, map[string]string{}, false},
		{"source ARN never matches a signed caller", sdrv.PermissionStatement{SourceARN: "arn:aws:s3:::b"}, urlCtx, false},
		{"source account never matches a signed caller", sdrv.PermissionStatement{SourceAccount: "123456789012"}, urlCtx, false},
	}

	for _, tc := range cases {
		if got := conditionsHold(&tc.st, tc.cctx); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestActionMatches(t *testing.T) {
	for _, tc := range []struct {
		stmt string
		want bool
	}{
		{"lambda:InvokeFunction", true},
		{"lambda:invokefunction", true},
		{"lambda:*", true},
		{"*", true},
		{"lambda:InvokeFunctionUrl", false},
		{"lambda:GetFunction", false},
	} {
		if got := actionMatches(tc.stmt, actionInvokeFunction); got != tc.want {
			t.Errorf("actionMatches(%q) = %v, want %v", tc.stmt, got, tc.want)
		}
	}
}
