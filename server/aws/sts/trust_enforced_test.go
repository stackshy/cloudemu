package sts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
	smithy "github.com/aws/smithy-go"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const (
	acctRoot   = "arn:aws:iam::" + testAccountID + ":root"
	callerARN  = "arn:aws:iam::" + testAccountID + ":user/caller"
	allowDDB   = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"dynamodb:*","Resource":"*"}]}`
	allowStsAR = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sts:*","Resource":"*"}]}`
)

// enforcedSTS is an --enforce-auth server with IAM and STS wired.
type enforcedSTS struct {
	t     *testing.T
	url   string
	cloud *awsprovider.Provider
}

func newEnforcedSTS(t *testing.T) *enforcedSTS {
	t.Helper()

	cloud := cloudemu.NewAWS()
	ts := newServer(t, awsserver.Drivers{
		IAM: cloud.IAM, STS: true, AccountID: testAccountID, Region: testRegion, EnforceAuth: true,
	})

	return &enforcedSTS{t: t, url: ts.URL, cloud: cloud}
}

// user creates an IAM user with an optional inline policy and returns its key.
func (e *enforcedSTS) user(name, doc string) aws.Credentials {
	e.t.Helper()

	ctx := context.Background()

	if _, err := e.cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: name}); err != nil {
		e.t.Fatalf("CreateUser: %v", err)
	}

	if doc != "" {
		if err := e.cloud.IAM.AttachUserPolicy(ctx, name, e.policy(name+"-user", doc)); err != nil {
			e.t.Fatalf("AttachUserPolicy: %v", err)
		}
	}

	ak, err := e.cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: name})
	if err != nil {
		e.t.Fatalf("CreateAccessKey: %v", err)
	}

	return aws.Credentials{AccessKeyID: ak.AccessKeyID, SecretAccessKey: ak.SecretAccessKey}
}

// role creates a role with the given trust document and optional inline policy.
func (e *enforcedSTS) role(name, path, trust, doc string) string {
	e.t.Helper()

	ctx := context.Background()

	info, err := e.cloud.IAM.CreateRole(ctx, iamdriver.RoleConfig{Name: name, Path: path, AssumeRolePolicyDoc: trust})
	if err != nil {
		e.t.Fatalf("CreateRole: %v", err)
	}

	if doc != "" {
		e.attachRole(name, name+"-role", doc)
	}

	return info.ARN
}

// policy creates a managed policy and returns its ARN.
func (e *enforcedSTS) policy(name, doc string) string {
	e.t.Helper()

	pol, err := e.cloud.IAM.CreatePolicy(context.Background(), iamdriver.PolicyConfig{Name: name, PolicyDocument: doc})
	if err != nil {
		e.t.Fatalf("CreatePolicy: %v", err)
	}

	return pol.ARN
}

func (e *enforcedSTS) attachRole(role, name, doc string) {
	e.t.Helper()

	if err := e.cloud.IAM.AttachRolePolicy(context.Background(), role, e.policy(name, doc)); err != nil {
		e.t.Fatalf("AttachRolePolicy: %v", err)
	}
}

func (e *enforcedSTS) config(c aws.Credentials) aws.Config {
	e.t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(testRegion),
		awsconfig.WithRetryMaxAttempts(1),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, c.SessionToken)),
	)
	if err != nil {
		e.t.Fatalf("aws config: %v", err)
	}

	return cfg
}

func (e *enforcedSTS) sts(c aws.Credentials) *awssts.Client {
	return awssts.NewFromConfig(e.config(c), func(o *awssts.Options) { o.BaseEndpoint = aws.String(e.url) })
}

func (e *enforcedSTS) iam(c aws.Credentials) *awsiam.Client {
	return awsiam.NewFromConfig(e.config(c), func(o *awsiam.Options) { o.BaseEndpoint = aws.String(e.url) })
}

func (e *enforcedSTS) assume(c aws.Credentials, in *awssts.AssumeRoleInput) (aws.Credentials, error) {
	if in.RoleSessionName == nil {
		in.RoleSessionName = aws.String("s1")
	}

	out, err := e.sts(c).AssumeRole(context.Background(), in)
	if err != nil {
		return aws.Credentials{}, err
	}

	return toCreds(out.Credentials), nil
}

func toCreds(c *ststypes.Credentials) aws.Credentials {
	return aws.Credentials{
		AccessKeyID:     aws.ToString(c.AccessKeyId),
		SecretAccessKey: aws.ToString(c.SecretAccessKey),
		SessionToken:    aws.ToString(c.SessionToken),
	}
}

func trustOf(statements ...string) string {
	return `{"Version":"2012-10-17","Statement":[` + strings.Join(statements, ",") + `]}`
}

func trustStmt(action, principal, condition string) string {
	s := `{"Effect":"Allow","Principal":` + principal + `,"Action":` + action
	if condition != "" {
		s += `,"Condition":` + condition
	}

	return s + "}"
}

func awsPrincipal(arn string) string { return `{"AWS":"` + arn + `"}` }

func errCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}

	return ""
}

func wantAssume(t *testing.T, err error, allowed bool) {
	t.Helper()

	switch {
	case allowed && err != nil:
		t.Fatalf("AssumeRole: want success, got %v", err)
	case !allowed && errCode(err) != "AccessDenied":
		t.Fatalf("AssumeRole: want AccessDenied, got %v", err)
	}
}

// TestEnforcedTrustEvaluatesTheRealCaller covers the trust decision: the trust
// policy is evaluated for the signed caller, a directly named principal needs
// no identity allow, a trusted account still does, and principal types,
// conditions, session tags and source identity are honored.
func TestEnforcedTrustEvaluatesTheRealCaller(t *testing.T) {
	assumeAll := `["sts:AssumeRole","sts:TagSession","sts:SetSourceIdentity"]`
	tags := []ststypes.Tag{{Key: aws.String("team"), Value: aws.String("blue")}}

	cases := []struct {
		name     string
		trust    string
		identity string
		in       awssts.AssumeRoleInput
		allowed  bool
	}{
		{"user named in the trust, no identity allow", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN), "")),
			allowDDB, awssts.AssumeRoleInput{}, true},
		{"account root in the trust, no identity allow", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(acctRoot), "")),
			allowDDB, awssts.AssumeRoleInput{}, false},
		{"account id in the trust, identity allow", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(testAccountID), "")),
			allowStsAR, awssts.AssumeRoleInput{}, true},
		{"another user in the trust, identity allow",
			trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal("arn:aws:iam::"+testAccountID+":user/other"), "")),
			allowStsAR, awssts.AssumeRoleInput{}, false},
		{"Federated star never trusts an IAM user", trustOf(trustStmt(`"sts:AssumeRole"`, `{"Federated":"*"}`, "")),
			allowStsAR, awssts.AssumeRoleInput{}, false},
		{"Service star never trusts an IAM user", trustOf(trustStmt(`"sts:AssumeRole"`, `{"Service":"*"}`, "")),
			allowStsAR, awssts.AssumeRoleInput{}, false},
		{"explicit identity deny beats a named trust", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN), "")),
			`{"Statement":[{"Effect":"Deny","Action":"sts:AssumeRole","Resource":"*"}]}`, awssts.AssumeRoleInput{}, false},
		{"trust deny beats an identity allow", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(acctRoot), ""),
			`{"Effect":"Deny","Principal":{"AWS":"`+callerARN+`"},"Action":"sts:AssumeRole"}`),
			allowStsAR, awssts.AssumeRoleInput{}, false},
		{"NotPrincipal deny hits an unlisted caller", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(acctRoot), ""),
			`{"Effect":"Deny","NotPrincipal":{"AWS":["arn:aws:iam::`+testAccountID+`:user/other","`+acctRoot+`"]},`+
				`"Action":"sts:AssumeRole"}`), allowStsAR, awssts.AssumeRoleInput{}, false},
		{"ExternalId matches", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN),
			`{"StringEquals":{"sts:ExternalId":"ext-1"}}`)), allowDDB,
			awssts.AssumeRoleInput{ExternalId: aws.String("ext-1")}, true},
		{"ExternalId differs", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN),
			`{"StringEquals":{"sts:ExternalId":"ext-1"}}`)), allowDDB,
			awssts.AssumeRoleInput{ExternalId: aws.String("ext-2")}, false},
		{"ExternalId missing", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN),
			`{"StringEquals":{"sts:ExternalId":"ext-1"}}`)), allowDDB, awssts.AssumeRoleInput{}, false},
		{"RoleSessionName condition met", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN),
			`{"StringLike":{"sts:RoleSessionName":"ci-*"}}`)), allowDDB,
			awssts.AssumeRoleInput{RoleSessionName: aws.String("ci-42")}, true},
		{"RoleSessionName condition not met", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN),
			`{"StringLike":{"sts:RoleSessionName":"ci-*"}}`)), allowDDB,
			awssts.AssumeRoleInput{RoleSessionName: aws.String("dev")}, false},
		{"tags need sts:TagSession in the trust", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN), "")),
			allowDDB, awssts.AssumeRoleInput{Tags: tags}, false},
		{"tags with sts:TagSession", trustOf(trustStmt(assumeAll, awsPrincipal(callerARN), "")),
			allowDDB, awssts.AssumeRoleInput{Tags: tags}, true},
		{"request tag condition", trustOf(trustStmt(assumeAll, awsPrincipal(callerARN),
			`{"StringEquals":{"aws:RequestTag/team":"red"}}`)), allowDDB, awssts.AssumeRoleInput{Tags: tags}, false},
		{"tag keys condition", trustOf(trustStmt(assumeAll, awsPrincipal(callerARN),
			`{"ForAllValues:StringEquals":{"aws:TagKeys":["team","env"]}}`)), allowDDB, awssts.AssumeRoleInput{Tags: tags}, true},
		{"source identity needs sts:SetSourceIdentity", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(callerARN), "")),
			allowDDB, awssts.AssumeRoleInput{SourceIdentity: aws.String("alice")}, false},
		{"source identity allowed and matched", trustOf(trustStmt(assumeAll, awsPrincipal(callerARN),
			`{"StringEquals":{"sts:SourceIdentity":"alice"}}`)), allowDDB,
			awssts.AssumeRoleInput{SourceIdentity: aws.String("alice")}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnforcedSTS(t)
			caller := e.user("caller", tc.identity)
			roleArn := e.role("target", "", tc.trust, "")

			in := tc.in
			in.RoleArn = aws.String(roleArn)

			_, err := e.assume(caller, &in)
			wantAssume(t, err, tc.allowed)
		})
	}
}

// TestEnforcedTrustRoleArnMustMatch covers STS-X2: a RoleArn in another account
// or with a different path names a different role, so it never assumes the
// local one.
func TestEnforcedTrustRoleArnMustMatch(t *testing.T) {
	e := newEnforcedSTS(t)
	caller := e.user("caller", "")
	e.role("app", "/team/", trustOf(trustStmt(`"sts:AssumeRole"`, `"*"`, "")), "")

	for _, arn := range []string{
		"arn:aws:iam::999999999999:role/team/app",
		"arn:aws:iam::" + testAccountID + ":role/app",
		"arn:aws:iam::" + testAccountID + ":role/other/app",
	} {
		_, err := e.assume(caller, &awssts.AssumeRoleInput{RoleArn: aws.String(arn)})
		wantAssume(t, err, false)

		if !strings.Contains(err.Error(), "on resource: "+arn) {
			t.Fatalf("the deny must name the RoleArn as sent: %v", err)
		}
	}

	_, err := e.assume(caller, &awssts.AssumeRoleInput{RoleArn: aws.String("arn:aws:iam::" + testAccountID + ":role/team/app")})
	wantAssume(t, err, true)
}

// TestEnforcedTrustDenyIsNeutral checks a refused AssumeRole names the caller
// and the RoleArn as sent, and reads the same whether the role exists or not.
func TestEnforcedTrustDenyIsNeutral(t *testing.T) {
	e := newEnforcedSTS(t)
	caller := e.user("caller", allowDDB)
	e.role("exists", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(acctRoot), "")), "")

	message := func(name string) string {
		_, err := e.assume(caller, &awssts.AssumeRoleInput{RoleArn: aws.String("arn:aws:iam::" + testAccountID + ":role/" + name)})

		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "AccessDenied" {
			t.Fatalf("AssumeRole %s: want AccessDenied, got %v", name, err)
		}

		return apiErr.ErrorMessage()
	}

	existing, missing := message("exists"), message("absent")

	want := "User: " + callerARN + " is not authorized to perform: sts:AssumeRole on resource: arn:aws:iam::" +
		testAccountID + ":role/exists"
	if existing != want {
		t.Fatalf("message = %q, want %q", existing, want)
	}

	if strings.ReplaceAll(missing, "role/absent", "role/exists") != existing {
		t.Fatalf("messages differ:\n existing: %s\n missing:  %s", existing, missing)
	}
}

// TestEnforcedSessionRestrictions covers what each kind of temporary
// credential may call (IAM User Guide, "Compare AWS STS credentials").
func TestEnforcedSessionRestrictions(t *testing.T) {
	e := newEnforcedSTS(t)
	ctx := context.Background()
	boot := e.user("boot", "")
	e.role("target", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(acctRoot), "")), "")
	roleArn := aws.String("arn:aws:iam::" + testAccountID + ":role/target")

	fed, err := e.sts(boot).GetFederationToken(ctx, &awssts.GetFederationTokenInput{Name: aws.String("fed")})
	if err != nil {
		t.Fatalf("GetFederationToken: %v", err)
	}

	tok, err := e.sts(boot).GetSessionToken(ctx, &awssts.GetSessionTokenInput{})
	if err != nil {
		t.Fatalf("GetSessionToken: %v", err)
	}

	role, err := e.assume(boot, &awssts.AssumeRoleInput{RoleArn: roleArn})
	if err != nil {
		t.Fatalf("AssumeRole: %v", err)
	}

	federation, session := toCreds(fed.Credentials), toCreds(tok.Credentials)

	t.Run("federation", func(t *testing.T) {
		_, err := e.assume(federation, &awssts.AssumeRoleInput{RoleArn: roleArn})
		wantAssume(t, err, false)

		_, err = e.iam(federation).ListUsers(ctx, &awsiam.ListUsersInput{})
		if errCode(err) != "AccessDenied" {
			t.Fatalf("ListUsers: want AccessDenied, got %v", err)
		}

		if _, err := e.sts(federation).GetCallerIdentity(ctx, &awssts.GetCallerIdentityInput{}); err != nil {
			t.Fatalf("GetCallerIdentity: %v", err)
		}
	})

	t.Run("session token", func(t *testing.T) {
		_, err := e.iam(session).ListUsers(ctx, &awsiam.ListUsersInput{})
		if errCode(err) != "AccessDenied" {
			t.Fatalf("ListUsers: want AccessDenied, got %v", err)
		}

		_, err = e.sts(session).GetSessionToken(ctx, &awssts.GetSessionTokenInput{})
		if errCode(err) != "AccessDenied" {
			t.Fatalf("GetSessionToken: want AccessDenied, got %v", err)
		}

		_, err = e.assume(session, &awssts.AssumeRoleInput{RoleArn: roleArn})
		wantAssume(t, err, true)
	})

	t.Run("role session", func(t *testing.T) {
		_, err := e.sts(role).GetSessionToken(ctx, &awssts.GetSessionTokenInput{})
		if errCode(err) != "AccessDenied" {
			t.Fatalf("GetSessionToken: want AccessDenied, got %v", err)
		}

		_, err = e.sts(role).GetFederationToken(ctx, &awssts.GetFederationTokenInput{Name: aws.String("x")})
		if errCode(err) != "AccessDenied" {
			t.Fatalf("GetFederationToken: want AccessDenied, got %v", err)
		}

		if _, err := e.sts(role).GetCallerIdentity(ctx, &awssts.GetCallerIdentityInput{}); err != nil {
			t.Fatalf("GetCallerIdentity: %v", err)
		}
	})
}

// TestEnforcedRoleChaining checks a role session assumes the next role when
// the next role trusts the first role by ARN, or trusts the account and the
// first role's policies allow it.
func TestEnforcedRoleChaining(t *testing.T) {
	e := newEnforcedSTS(t)
	boot := e.user("boot", "")

	firstArn := e.role("first", "/ops/", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(acctRoot), "")), allowDDB)
	byArn := e.role("byarn", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(firstArn), "")), "")
	byAccount := e.role("byaccount", "", trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal(acctRoot), "")), "")
	other := e.role("other", "", trustOf(trustStmt(`"sts:AssumeRole"`,
		awsPrincipal("arn:aws:iam::"+testAccountID+":role/elsewhere"), "")), "")

	first, err := e.assume(boot, &awssts.AssumeRoleInput{RoleArn: aws.String(firstArn)})
	if err != nil {
		t.Fatalf("AssumeRole first: %v", err)
	}

	_, err = e.assume(first, &awssts.AssumeRoleInput{RoleArn: aws.String(byArn)})
	wantAssume(t, err, true)

	_, err = e.assume(first, &awssts.AssumeRoleInput{RoleArn: aws.String(byAccount)})
	wantAssume(t, err, false)

	_, err = e.assume(first, &awssts.AssumeRoleInput{RoleArn: aws.String(other)})
	wantAssume(t, err, false)

	e.attachRole("first", "chain", allowStsAR)

	_, err = e.assume(first, &awssts.AssumeRoleInput{RoleArn: aws.String(byAccount)})
	wantAssume(t, err, true)
}

// TestEnforcedWebIdentityAndSAMLRefused checks signed AssumeRoleWithWebIdentity
// and AssumeRoleWithSAML are refused under --enforce-auth: the token and the
// assertion are not validated, so even an unrestricted caller gets a 403.
func TestEnforcedWebIdentityAndSAMLRefused(t *testing.T) {
	e := newEnforcedSTS(t)
	boot := e.user("boot", "")
	roleArn := e.role("fed", "", trustOf(trustStmt(`"sts:AssumeRoleWithWebIdentity"`, `{"Federated":"*"}`, "")), "")

	for action, form := range map[string]string{
		"AssumeRoleWithWebIdentity": "&RoleSessionName=s&WebIdentityToken=junk",
		"AssumeRoleWithSAML":        "&PrincipalArn=arn:aws:iam::" + testAccountID + ":saml-provider/p&SAMLAssertion=eA%3D%3D",
	} {
		status, body := signedSTSForm(t, e.url, boot, "Action="+action+"&Version=2011-06-15&RoleArn="+roleArn+form)
		if status != http.StatusForbidden || !strings.Contains(body, "<Code>AccessDenied</Code>") ||
			!strings.Contains(body, action+" is not available under --enforce-auth") {
			t.Fatalf("signed %s: %d %s", action, status, body)
		}
	}
}

// signedSTSForm sends a SigV4-signed STS query request and returns the status
// and body.
func signedSTSForm(t *testing.T, endpoint string, c aws.Credentials, body string) (int, string) {
	t.Helper()

	ctx := context.Background()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	sum := sha256.Sum256([]byte(body))
	if err := v4.NewSigner().SignHTTP(ctx, c, req, hex.EncodeToString(sum[:]), "sts", testRegion, time.Now()); err != nil {
		t.Fatalf("sign: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(raw)
}

// TestAuthOffTrustUnchanged checks the auth-off path keeps evaluating the
// trust policy for the account root and ignores conditions, while a RoleArn
// for another account or path is still refused (STS-X2).
func TestAuthOffTrustUnchanged(t *testing.T) {
	ts, iamClient := newTrustServer(t)
	ctx := context.Background()

	trust := trustOf(trustStmt(`"sts:AssumeRole"`, awsPrincipal("arn:aws:iam::123456789012:root"),
		`{"StringEquals":{"sts:ExternalId":"ext-1"}}`))
	if _, err := iamClient.CreateRole(ctx, &awsiam.CreateRoleInput{
		RoleName: aws.String("legacy"), Path: aws.String("/p/"), AssumeRolePolicyDocument: aws.String(trust),
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	client := stsClient(t, ts.URL)

	if _, err := client.AssumeRole(ctx, &awssts.AssumeRoleInput{
		RoleArn: aws.String("arn:aws:iam::" + testAccountID + ":role/p/legacy"), RoleSessionName: aws.String("s"),
	}); err != nil {
		t.Fatalf("AssumeRole: %v", err)
	}

	for _, arn := range []string{"arn:aws:iam::999999999999:role/p/legacy", "arn:aws:iam::" + testAccountID + ":role/legacy"} {
		_, err := client.AssumeRole(ctx, &awssts.AssumeRoleInput{RoleArn: aws.String(arn), RoleSessionName: aws.String("s")})
		assertAccessDenied(t, err)
	}
}
