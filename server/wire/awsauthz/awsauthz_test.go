package awsauthz

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/server/authctx"
)

func formRequest(path, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return r
}

func TestQueryChecks(t *testing.T) {
	tests := []struct {
		name       string
		req        *http.Request
		wantAction string
		wantOK     bool
	}{
		{"body Action", formRequest("/", "Action=CreateUser"), "iam:CreateUser", true},
		{"first of duplicated Actions", formRequest("/", "Action=ListUsers&Action=CreateUser"), "iam:ListUsers", true},
		{"body wins over query string", formRequest("/?Action=ListUsers", "Action=CreateUser"), "iam:CreateUser", true},
		{"query string only", httptest.NewRequest(http.MethodGet, "/?Action=GetUser", nil), "iam:GetUser", true},
		{"case is kept", formRequest("/", "Action=createuser"), "iam:createuser", true},
		{"no Action", formRequest("/", "Version=1"), "", false},
		{"body does not parse", formRequest("/", "Action=CreateUser&x=%zz"), "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checks, ok := QueryChecks(tc.req, "iam")
			require.Equal(t, tc.wantOK, ok)

			if !ok {
				assert.Nil(t, checks)
				return
			}

			assert.Equal(t, []Check{{Action: tc.wantAction, Resource: "", Mode: Required}}, checks)
		})
	}
}

func TestSingle(t *testing.T) {
	assert.Equal(t, []Check{{Action: "s3:GetObject", Resource: "arn:aws:s3:::b/k"}}, Single("s3:GetObject", "arn:aws:s3:::b/k"))
}

func TestEvaluationRoundTrip(t *testing.T) {
	_, ok := EvaluationFrom(context.Background())
	assert.False(t, ok)

	c := Check{Action: "sts:AssumeRole", Resource: "arn:aws:iam::1:role/r", Mode: ResourcePolicy}
	e := Evaluation{Principal: authctx.Principal{UserName: "u"}, Decisions: map[Check]Decision{c: ImplicitDeny}}

	got, ok := EvaluationFrom(WithEvaluation(context.Background(), &e))
	require.True(t, ok)
	assert.Equal(t, "u", got.Principal.UserName)
	assert.Equal(t, ImplicitDeny, got.Decisions[c])
}

func TestConditionContext(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.1.2.3:5555"
	r.Header.Set("X-Forwarded-For", "1.1.1.1")

	p := authctx.Principal{
		UserName: "ann", ARN: "arn:aws:iam::123456789012:user/ann", AccountID: "123456789012", UserID: "AIDAANN",
	}

	ctx := ConditionContext(r, &p, Scope{AccountID: "123456789012", Region: "eu-west-1", Partition: "aws"})

	assert.Equal(t, "10.1.2.3", ctx["aws:SourceIp"], "X-Forwarded-For is never trusted")
	assert.Equal(t, "false", ctx["aws:SecureTransport"])
	assert.Equal(t, p.ARN, ctx["aws:PrincipalArn"])
	assert.Equal(t, "ann", ctx["aws:username"])
	assert.Equal(t, "AIDAANN", ctx["aws:userid"])
	assert.Equal(t, "123456789012", ctx["aws:PrincipalAccount"])
	assert.Equal(t, "User", ctx["aws:PrincipalType"])
	assert.Equal(t, "eu-west-1", ctx["aws:RequestedRegion"], "the region comes from the server, not the signature")
	assert.NotEmpty(t, ctx["aws:CurrentTime"])

	r.TLS = &tls.ConnectionState{}
	r.RemoteAddr = "no-port"

	ctx = ConditionContext(r, &authctx.Principal{}, Scope{})
	assert.Equal(t, "true", ctx["aws:SecureTransport"])
	assert.Equal(t, "no-port", ctx["aws:SourceIp"])

	for _, k := range []string{"aws:PrincipalArn", "aws:username", "aws:userid", "aws:PrincipalType", "aws:RequestedRegion"} {
		assert.NotContains(t, ctx, k, "unknown keys are left out")
	}
}

func TestPrincipalType(t *testing.T) {
	for arn, want := range map[string]string{
		"":                                  "",
		"arn:aws:iam::1:root":               "Account",
		"arn:aws:sts::1:assumed-role/r/s":   "AssumedRole",
		"arn:aws:sts::1:federated-user/bob": "FederatedUser",
		"arn:aws:iam::1:user/path/ann":      "User",
	} {
		assert.Equal(t, want, principalType(arn), arn)
	}
}
