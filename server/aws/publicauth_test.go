package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentity"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"
	smithyauth "github.com/aws/smithy-go/auth"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// anonymousOps asks an SDK client's default auth scheme resolver, which is
// generated from the service's Smithy model, which of the client's operations
// resolve to the anonymous scheme (smithy.api#noAuth). Operations are the
// client's exported methods other than Options.
func anonymousOps(t *testing.T, client any, resolve func(op string) []*smithyauth.Option) map[string]bool {
	t.Helper()

	out := map[string]bool{}
	typ := reflect.TypeOf(client)

	for i := range typ.NumMethod() {
		op := typ.Method(i).Name
		if op == "Options" {
			continue
		}

		opts := resolve(op)
		out[op] = len(opts) > 0 && opts[0].SchemeID == smithyauth.SchemeIDAnonymous
	}

	return out
}

// newerModelNoAuth lists operations the botocore models the tables were taken
// from mark noAuth while the aws-sdk-go-v2 version pinned in go.mod still signs
// them. Drop an entry once the pinned SDK catches up.
//
//nolint:gochecknoglobals // test fixture
var newerModelNoAuth = map[string]struct{}{
	"cognito-idp GetTokensFromRefreshToken": {},
}

// checkAgainstModel asserts the exemption table agrees with the SDK model: every
// operation the model marks noAuth is exempt, and every exempt operation the SDK
// knows is noAuth (bar newerModelNoAuth). Exempt operations newer than the
// pinned SDK are tolerated.
func checkAgainstModel(t *testing.T, service string, model map[string]bool, exempt map[string]struct{}) {
	t.Helper()

	anon := 0

	for op, isAnon := range model {
		_, ok := exempt[op]
		if isAnon {
			anon++
		}

		if _, newer := newerModelNoAuth[service+" "+op]; newer && !isAnon && ok {
			continue
		}

		if isAnon != ok {
			t.Errorf("%s %s: model noAuth=%v, exempt=%v", service, op, isAnon, ok)
		}
	}

	if anon == 0 {
		t.Fatalf("%s: SDK model reports no anonymous operations; resolver probe is broken", service)
	}
}

func TestPublicOpsMatchSDKModels(t *testing.T) {
	ctx := context.Background()

	idp := cognitoidentityprovider.New(cognitoidentityprovider.Options{Region: "us-east-1"})
	checkAgainstModel(t, "cognito-idp", anonymousOps(t, idp, func(op string) []*smithyauth.Option {
		o, _ := idp.Options().AuthSchemeResolver.ResolveAuthSchemes(ctx,
			&cognitoidentityprovider.AuthResolverParameters{Operation: op, Region: "us-east-1"})
		return o
	}), cognitoIDPPublicOps)

	ident := cognitoidentity.New(cognitoidentity.Options{Region: "us-east-1"})
	checkAgainstModel(t, "cognito-identity", anonymousOps(t, ident, func(op string) []*smithyauth.Option {
		o, _ := ident.Options().AuthSchemeResolver.ResolveAuthSchemes(ctx,
			&cognitoidentity.AuthResolverParameters{Operation: op, Region: "us-east-1"})
		return o
	}), cognitoIdentityPublicOps)

	sts := awssts.New(awssts.Options{Region: "us-east-1"})
	checkAgainstModel(t, "sts", anonymousOps(t, sts, func(op string) []*smithyauth.Option {
		o, _ := sts.Options().AuthSchemeResolver.ResolveAuthSchemes(ctx,
			&awssts.AuthResolverParameters{Operation: op, Region: "us-east-1"})
		return o
	}), stsPublicActions)
}

// enforcedServer starts the full AWS wire server with --enforce-auth on.
func enforcedServer(t *testing.T) (*httptest.Server, *awsprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewAWS()
	d := DriversFrom(cloud)
	d.EnforceAuth = true

	ts := httptest.NewServer(New(d))
	t.Cleanup(ts.Close)

	return ts, cloud
}

type rawReq struct {
	method, path, host, body string
	header                   map[string]string
}

func doRaw(t *testing.T, ts *httptest.Server, rq rawReq) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), rq.method, ts.URL+rq.path, strings.NewReader(rq.body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if rq.host != "" {
		req.Host = rq.host
	}

	for k, v := range rq.header {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

const (
	formCT     = "application/x-www-form-urlencoded"
	amzJSON11  = "application/x-amz-json-1.1"
	missingTok = "MissingAuthenticationToken"
	idpTarget  = "AWSCognitoIdentityProviderService."
	identTgt   = "AWSCognitoIdentityService."
	lambdaPath = "/2015-03-31/functions"
)

func jsonRPC(target, body string) rawReq {
	return rawReq{
		method: http.MethodPost, path: "/", body: body,
		header: map[string]string{"X-Amz-Target": target, "Content-Type": amzJSON11},
	}
}

func queryForm(body string) rawReq {
	return rawReq{method: http.MethodPost, path: "/", body: body, header: map[string]string{"Content-Type": formCT}}
}

// TestEnforcedGateAdmitsUnsignedPublicOps sends unsigned requests for operations
// AWS serves without SigV4 and asserts each reaches its handler instead of the
// gate's 403 MissingAuthenticationToken.
func TestEnforcedGateAdmitsUnsignedPublicOps(t *testing.T) {
	ts, _ := enforcedServer(t)

	cases := []struct {
		name string
		req  rawReq
		want int
	}{
		{"sts AssumeRoleWithWebIdentity", queryForm("Action=AssumeRoleWithWebIdentity&Version=2011-06-15" +
			"&RoleArn=arn%3Aaws%3Aiam%3A%3A123456789012%3Arole%2Fweb&RoleSessionName=s&WebIdentityToken=tok"), http.StatusOK},
		{"sts AssumeRoleWithSAML", queryForm("Action=AssumeRoleWithSAML&Version=2011-06-15" +
			"&RoleArn=arn%3Aaws%3Aiam%3A%3A123456789012%3Arole%2Fsaml" +
			"&PrincipalArn=arn%3Aaws%3Aiam%3A%3A123456789012%3Asaml-provider%2Fidp&SAMLAssertion=eA%3D%3D"), http.StatusOK},
		// Cognito user-pool public ops are not routed yet, so the handler answers
		// UnknownOperationException (400): the point is the gate let them through.
		{"cognito-idp InitiateAuth", jsonRPC(idpTarget+"InitiateAuth", `{}`), http.StatusBadRequest},
		{"cognito-idp SignUp", jsonRPC(idpTarget+"SignUp", `{}`), http.StatusBadRequest},
		{"cognito-idp RespondToAuthChallenge", jsonRPC(idpTarget+"RespondToAuthChallenge", `{}`), http.StatusBadRequest},
		// No identity-pool handler is served yet: the request falls to the
		// dispatcher's 501, not the gate's 403.
		{"cognito-identity GetId", jsonRPC(identTgt+"GetId", `{}`), http.StatusNotImplemented},
		// An unknown API reaches the API Gateway data plane, which answers like
		// real API Gateway: 403 {"message":"Missing Authentication Token"}. That
		// body differs from the gate's MissingAuthenticationToken error.
		{"execute-api host", rawReq{method: http.MethodGet, path: "/prod/pets", host: "abc123.execute-api.us-east-1.amazonaws.com"},
			http.StatusForbidden},
		{"execute-api path", rawReq{method: http.MethodGet, path: "/restapis/abc123/prod/_user_request_/pets"}, http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doRaw(t, ts, tc.req)
			if strings.Contains(body, missingTok) || status != tc.want {
				t.Fatalf("status %d (want %d), body %s", status, tc.want, body)
			}
		})
	}
}

// TestEnforcedGateRejectsUnsignedPrivateOps asserts everything that is not a
// public operation of the handler that actually serves it is still rejected
// when unsigned, including requests that borrow a public marker (an
// execute-api Host, a public Action) but would dispatch elsewhere.
func TestEnforcedGateRejectsUnsignedPrivateOps(t *testing.T) {
	ts, _ := enforcedServer(t)

	cases := []struct {
		name string
		req  rawReq
	}{
		{"ec2 DescribeInstances", queryForm("Action=DescribeInstances&Version=2016-11-15")},
		{"sts GetCallerIdentity", queryForm("Action=GetCallerIdentity&Version=2011-06-15")},
		{"sts AssumeRole", queryForm("Action=AssumeRole&Version=2011-06-15&RoleArn=x&RoleSessionName=s")},
		{"cognito-idp CreateUserPool", jsonRPC(idpTarget+"CreateUserPool", `{"PoolName":"p"}`)},
		{"cognito-idp AdminInitiateAuth", jsonRPC(idpTarget+"AdminInitiateAuth", `{}`)},
		{"cognito-identity CreateIdentityPool", jsonRPC(identTgt+"CreateIdentityPool", `{}`)},
		{"dynamodb ListTables", jsonRPC("DynamoDB_20120810.ListTables", `{}`)},
		{"s3 ListBuckets", rawReq{method: http.MethodGet, path: "/"}},
		{"execute-api host on a lambda path", rawReq{method: http.MethodGet, path: lambdaPath,
			host: "abc123.execute-api.us-east-1.amazonaws.com"}},
		{"public Action on a lambda path", rawReq{method: http.MethodGet, path: lambdaPath + "?Action=AssumeRoleWithWebIdentity"}},
		// Surfaces that are exempt in real AWS but not served yet fall to S3 here,
		// so they must stay gated until their own handler claims them.
		{"jwks before cognito serves it", rawReq{method: http.MethodGet, path: "/us-east-1_abcDEF123/.well-known/jwks.json"}},
		{"appsync graphql before appsync serves it", rawReq{method: http.MethodPost, path: "/graphql", body: `{}`}},
		{"hosted ui before cognito serves it", rawReq{method: http.MethodGet, path: "/oauth2/userInfo",
			host: "mydomain.auth.us-east-1.amazoncognito.com"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doRaw(t, ts, tc.req)
			if status != http.StatusForbidden || !strings.Contains(body, missingTok) {
				t.Fatalf("status %d, body %s; want 403 %s", status, body, missingTok)
			}
		})
	}
}

func TestPublicRequestShapes(t *testing.T) {
	mk := func(method, target, host, path, auth string) *http.Request {
		r := httptest.NewRequest(method, "http://localhost"+path, nil)
		if host != "" {
			r.Host = host
		}

		if target != "" {
			r.Header.Set("X-Amz-Target", target)
		}

		if auth != "" {
			r.Header.Set("Authorization", auth)
		}

		return r
	}

	idp := targetIn(cognitoIDPTargetPrefix, cognitoIDPPublicOps)
	ident := targetIn(cognitoIdentityTargetPrefix, cognitoIdentityPublicOps)

	cases := []struct {
		name  string
		match func(*http.Request, []byte) bool
		r     *http.Request
		want  bool
	}{
		{"idp public target", idp, mk(http.MethodPost, idpTarget+"GetUser", "", "/", ""), true},
		{"idp private target", idp, mk(http.MethodPost, idpTarget+"AdminGetUser", "", "/", ""), false},
		{"idp op under the identity prefix", idp, mk(http.MethodPost, identTgt+"InitiateAuth", "", "/", ""), false},
		{"identity public target", ident, mk(http.MethodPost, identTgt+"GetCredentialsForIdentity", "", "/", ""), true},
		{"identity private target", ident, mk(http.MethodPost, identTgt+"DescribeIdentity", "", "/", ""), false},
		{"jwks", isUserPoolWellKnown, mk(http.MethodGet, "", "", "/eu-west-2_Ab12/.well-known/jwks.json", ""), true},
		{"openid-configuration", isUserPoolWellKnown, mk(http.MethodGet, "", "", "/us-east-1_x/.well-known/openid-configuration", ""), true},
		{"well-known on a bucket-shaped id", isUserPoolWellKnown, mk(http.MethodGet, "", "", "/mybucket/.well-known/jwks.json", ""), false},
		{"well-known POST", isUserPoolWellKnown, mk(http.MethodPost, "", "", "/us-east-1_x/.well-known/jwks.json", ""), false},
		{"hosted domain host", isHostedUI, mk(http.MethodGet, "", "d.auth.us-east-1.amazoncognito.com", "/oauth2/token", ""), true},
		{"hosted domain localhost", isHostedUI, mk(http.MethodPost, "", "d.auth.localhost:4566", "/oauth2/token", ""), true},
		{"hosted path fallback", isHostedUI, mk(http.MethodPost, "", "", "/_cognito/d/oauth2/token", ""), true},
		{"other amazoncognito host", isHostedUI, mk(http.MethodGet, "", "cognito-idp.us-east-1.amazoncognito.com", "/", ""), false},
		{"execute-api host", isExecuteAPI, mk(http.MethodGet, "", "a.execute-api.us-east-1.amazonaws.com", "/p", ""), true},
		{"execute-api path", isExecuteAPI, mk(http.MethodGet, "", "", "/restapis/a/s/_user_request_/p", ""), true},
		{"restapis control plane", isExecuteAPI, mk(http.MethodGet, "", "", "/restapis/a/resources/r", ""), false},
		{"appsync host unsigned", isUnsignedGraphQL, mk(http.MethodPost, "", "a.appsync-api.us-east-1.amazonaws.com", "/graphql", ""), true},
		{"appsync path unsigned", isUnsignedGraphQL, mk(http.MethodPost, "", "", "/graphql", ""), true},
		{"appsync signed (IAM auth mode)", isUnsignedGraphQL, mk(http.MethodPost, "", "", "/graphql", "AWS4-HMAC-SHA256 Credential=x"), false},
		{"graphql-prefixed bucket", isUnsignedGraphQL, mk(http.MethodGet, "", "", "/graphqlbucket/k", ""), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.match(tc.r, nil); got != tc.want {
				t.Fatalf("match = %v, want %v", got, tc.want)
			}
		})
	}

	if rt := publicRouteFor(mk(http.MethodGet, "", "", "/", ""), nil); rt != nil {
		t.Fatalf("plain GET / matched a public route")
	}

	form := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://localhost/", nil)
		r.Header.Set("Content-Type", formCT)

		return r
	}

	if !isPublicSTSAction(form(), []byte("Action=AssumeRoleWithWebIdentity")) {
		t.Fatalf("form Action AssumeRoleWithWebIdentity not recognized as public STS")
	}

	if isPublicSTSAction(form(), []byte("Action=GetCallerIdentity")) {
		t.Fatalf("GetCallerIdentity recognized as public")
	}

	if !isPublicSTSAction(httptest.NewRequest(http.MethodGet, "http://localhost/?Action=AssumeRoleWithSAML", nil), nil) {
		t.Fatalf("query-string Action AssumeRoleWithSAML not recognized as public STS")
	}
}

// TestAuthzSkipsPublicOps: a signed caller whose IAM policy allows only
// DynamoDB is still served a public Cognito operation (IAM never governs it),
// while a private Cognito operation is denied by the authorization gate.
func TestAuthzSkipsPublicOps(t *testing.T) {
	ts, cloud := enforcedServer(t)
	ctx := context.Background()

	if _, err := cloud.IAM.CreateUser(ctx, iamdriver.UserConfig{Name: "dynonly"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"dynamodb:*","Resource":"*"}]}`

	pol, err := cloud.IAM.CreatePolicy(ctx, iamdriver.PolicyConfig{Name: "dynonly", PolicyDocument: doc})
	if err != nil {
		t.Fatalf("CreatePolicy: %v", err)
	}

	if err := cloud.IAM.AttachUserPolicy(ctx, "dynonly", pol.ARN); err != nil {
		t.Fatalf("AttachUserPolicy: %v", err)
	}

	ak, err := cloud.IAM.CreateAccessKey(ctx, iamdriver.AccessKeyConfig{UserName: "dynonly"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	creds := aws.Credentials{AccessKeyID: ak.AccessKeyID, SecretAccessKey: ak.SecretAccessKey}

	send := func(op string) (int, string) {
		body := `{}`

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/", strings.NewReader(body))
		req.Header.Set("X-Amz-Target", idpTarget+op)
		req.Header.Set("Content-Type", amzJSON11)

		sum := sha256.Sum256([]byte(body))
		if err := v4.NewSigner().SignHTTP(ctx, creds, req, hex.EncodeToString(sum[:]), "cognito-idp", "us-east-1", time.Now()); err != nil {
			t.Fatalf("sign: %v", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()

		b, _ := io.ReadAll(resp.Body)

		var e struct {
			Type string `json:"__type"`
		}

		_ = json.Unmarshal(b, &e)

		return resp.StatusCode, e.Type
	}

	if status, typ := send("InitiateAuth"); status == http.StatusForbidden || typ == "AccessDeniedException" {
		t.Fatalf("public InitiateAuth denied: %d %s", status, typ)
	}

	if status, typ := send("ListUserPools"); status != http.StatusForbidden || typ != "AccessDeniedException" {
		t.Fatalf("private ListUserPools: %d %s, want 403 AccessDeniedException", status, typ)
	}
}

// TestSDKAnonymousSTSCallPassesEnforcedGate drives the real STS client, which
// sends AssumeRoleWithWebIdentity unsigned because its model marks it noAuth.
func TestSDKAnonymousSTSCallPassesEnforcedGate(t *testing.T) {
	ts, _ := enforcedServer(t)

	client := awssts.New(awssts.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(ts.URL),
		Credentials:  aws.AnonymousCredentials{},
	})

	out, err := client.AssumeRoleWithWebIdentity(context.Background(), &awssts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String("arn:aws:iam::123456789012:role/web"),
		RoleSessionName:  aws.String("s"),
		WebIdentityToken: aws.String("header.payload.sig"),
	})
	if err != nil {
		t.Fatalf("AssumeRoleWithWebIdentity unsigned under --enforce-auth: %v", err)
	}

	if out.Credentials == nil || aws.ToString(out.Credentials.AccessKeyId) == "" {
		t.Fatalf("no credentials returned")
	}

	if _, err := client.GetCallerIdentity(context.Background(), &awssts.GetCallerIdentityInput{}); err == nil ||
		!strings.Contains(err.Error(), missingTok) {
		t.Fatalf("unsigned GetCallerIdentity: err = %v, want %s", err, missingTok)
	}
}
