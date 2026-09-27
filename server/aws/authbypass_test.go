package aws

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssts "github.com/aws/aws-sdk-go-v2/service/sts"
)

// TestEnforcedGateBypassAttempts replays every known way of getting an unsigned
// request past --enforce-auth. Each must be rejected by the gate itself with
// 403 MissingAuthenticationToken, so it never reaches a handler.
func TestEnforcedGateBypassAttempts(t *testing.T) {
	ts, _ := enforcedServer(t)

	cognitoOn := func(host, path, target string) rawReq {
		rq := jsonRPC(idpTarget+target, `{"PoolName":"p"}`)
		rq.host, rq.path = host, path

		return rq
	}

	form := func(path, body string) rawReq {
		rq := queryForm(body)
		rq.path = path

		return rq
	}

	cases := []struct {
		name string
		req  rawReq
	}{
		// STS noAuth ops stay gated until web-identity and SAML validation land.
		{"sts AssumeRoleWithWebIdentity for a missing role", form("/", "Action=AssumeRoleWithWebIdentity&Version=2011-06-15"+
			"&RoleArn=arn%3Aaws%3Aiam%3A%3A000000000000%3Arole%2Fnonexistent&RoleSessionName=s&WebIdentityToken=junk")},
		{"sts AssumeRoleWithSAML", form("/", "Action=AssumeRoleWithSAML&Version=2011-06-15&RoleArn=x&PrincipalArn=y&SAMLAssertion=eA%3D%3D")},
		{"sts GetCallerIdentity", form("/", "Action=GetCallerIdentity&Version=2011-06-15")},

		// Parser disagreement: a body that does not parse plus a public Action in
		// the query string, and duplicated Actions.
		{"bad body, public query Action, GetSessionToken", form("/?Action=AssumeRoleWithWebIdentity",
			"Action=GetSessionToken&x=%zz")},
		{"bad body, public query Action, AssumeRole", form("/?Action=AssumeRoleWithWebIdentity",
			"Action=AssumeRole&RoleArn=x&RoleSessionName=s&x=%zz")},
		{"duplicate Action, public first", form("/", "Action=AssumeRoleWithWebIdentity&Action=GetSessionToken")},
		{"duplicate Action, public last", form("/", "Action=GetSessionToken&Action=AssumeRoleWithWebIdentity")},
		{"public body Action, private query Action", form("/?Action=GetSessionToken", "Action=AssumeRoleWithWebIdentity")},
		{"bad query string", form("/?Action=AssumeRoleWithWebIdentity&x=%zz", "Action=AssumeRoleWithWebIdentity")},
		{"lower-case public Action", form("/", "Action=assumerolewithwebidentity")},

		// Cognito private ops reached through the hosted-UI and well-known
		// markers, or with a public target on a non-JSON-RPC route.
		{"hosted-ui host, CreateUserPool", cognitoOn("x.auth.localhost", "/", "CreateUserPool")},
		{"hosted-ui amazoncognito host, CreateUserPool", cognitoOn("x.auth.us-east-1.amazoncognito.com", "/", "CreateUserPool")},
		{"/_cognito path, CreateUserPool", cognitoOn("", "/_cognito/x", "CreateUserPool")},
		{"/_cognito path, public target", cognitoOn("", "/_cognito/x", "InitiateAuth")},
		{"well-known path, ListUserPools", rawReq{method: http.MethodGet, path: "/us-east-1_abcDEF123/.well-known/jwks.json",
			header: map[string]string{"X-Amz-Target": idpTarget + "ListUserPools"}}},
		{"jwks GET before Cognito serves it", rawReq{method: http.MethodGet, path: "/us-east-1_abcDEF123/.well-known/jwks.json"}},
		{"cognito CreateUserPool", cognitoOn("", "/", "CreateUserPool")},
		{"cognito AdminInitiateAuth", cognitoOn("", "/", "AdminInitiateAuth")},
		{"cognito lower-case op", cognitoOn("", "/", "initiateAuth")},
		{"cognito lower-case prefix", rawReq{method: http.MethodPost, path: "/", body: `{}`,
			header: map[string]string{"X-Amz-Target": "awscognitoidentityproviderservice.InitiateAuth", "Content-Type": amzJSON11}}},
		{"cognito-identity GetId (no handler served)", jsonRPC(identTgt+"GetId", `{}`)},

		// AppSync control plane behind a data-plane marker.
		{"appsync-api host, CreateGraphqlApi", rawReq{method: http.MethodPost, path: "/v1/apis",
			host: "x.appsync-api.us-east-1.amazonaws.com", body: `{"name":"a","authenticationType":"API_KEY"}`,
			header: map[string]string{"Content-Type": "application/json"}}},
		{"appsync /graphql before AppSync serves it", rawReq{method: http.MethodPost, path: "/graphql", body: `{}`}},

		// execute-api markers on requests that dispatch to another service.
		{"execute-api host, DynamoDB target", func() rawReq {
			rq := jsonRPC("DynamoDB_20120810.ListTables", `{}`)
			rq.host = execHost

			return rq
		}()},
		{"execute-api host, lambda path", rawReq{method: http.MethodGet, path: lambdaPath, host: execHost}},
		{"execute-api host, query Action", func() rawReq {
			rq := form("/", "Action=ListUsers&Version=2010-05-08")
			rq.host = execHost

			return rq
		}()},

		// Plain private operations.
		{"ec2 DescribeInstances", form("/", "Action=DescribeInstances&Version=2016-11-15")},
		{"iam CreateUser", form("/", "Action=CreateUser&Version=2010-05-08&UserName=x")},
		{"dynamodb ListTables", jsonRPC("DynamoDB_20120810.ListTables", `{}`)},
		{"s3 ListBuckets", rawReq{method: http.MethodGet, path: "/"}},
		{"public Action on a lambda path", rawReq{method: http.MethodGet, path: lambdaPath + "?Action=AssumeRoleWithWebIdentity"}},
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

// TestSDKAnonymousSTSCallIsRejected drives the real STS client, which sends
// AssumeRoleWithWebIdentity unsigned (its model marks it noAuth). The gate
// rejects it until the token is validated against a registered provider.
func TestSDKAnonymousSTSCallIsRejected(t *testing.T) {
	ts, _ := enforcedServer(t)

	client := awssts.New(awssts.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(ts.URL),
		Credentials:  aws.AnonymousCredentials{},
	})

	_, err := client.AssumeRoleWithWebIdentity(context.Background(), &awssts.AssumeRoleWithWebIdentityInput{
		RoleArn:          aws.String("arn:aws:iam::123456789012:role/nonexistent"),
		RoleSessionName:  aws.String("s"),
		WebIdentityToken: aws.String("junk"),
	})
	if err == nil || !strings.Contains(err.Error(), missingTok) {
		t.Fatalf("unsigned AssumeRoleWithWebIdentity: err = %v, want %s", err, missingTok)
	}
}
