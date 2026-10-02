package cognito

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	smithyauth "github.com/aws/smithy-go/auth"
)

// newerModelNoAuth lists operations the botocore model the table was taken from
// marks noAuth while the aws-sdk-go-v2 version pinned in go.mod still signs
// them. Drop an entry once the pinned SDK catches up.
//
//nolint:gochecknoglobals // test fixture
var newerModelNoAuth = map[string]struct{}{
	"GetTokensFromRefreshToken": {},
}

// TestPublicOpsMatchSDKModel asks the SDK client's default auth scheme
// resolver, which is generated from the Smithy model, which operations resolve
// to the anonymous scheme (smithy.api#noAuth), and requires publicOps to be
// exactly that set. Operations are the client's exported methods other than
// Options.
func TestPublicOpsMatchSDKModel(t *testing.T) {
	client := cognitoidentityprovider.New(cognitoidentityprovider.Options{Region: "us-east-1"})
	resolver := client.Options().AuthSchemeResolver
	typ := reflect.TypeOf(client)
	anon := 0

	for i := range typ.NumMethod() {
		op := typ.Method(i).Name
		if op == "Options" {
			continue
		}

		opts, err := resolver.ResolveAuthSchemes(context.Background(),
			&cognitoidentityprovider.AuthResolverParameters{Operation: op, Region: "us-east-1"})
		if err != nil {
			t.Fatalf("%s: resolve: %v", op, err)
		}

		isAnon := len(opts) > 0 && opts[0].SchemeID == smithyauth.SchemeIDAnonymous
		if isAnon {
			anon++
		}

		_, exempt := publicOps[op]
		if _, newer := newerModelNoAuth[op]; newer && !isAnon && exempt {
			continue
		}

		if isAnon != exempt {
			t.Errorf("%s: model noAuth=%v, public=%v", op, isAnon, exempt)
		}
	}

	if anon == 0 {
		t.Fatalf("SDK model reports no anonymous operations; resolver probe is broken")
	}
}

func TestPublicRequestOnlyOnTheJSONRPCRoute(t *testing.T) {
	cases := []struct {
		name, method, host, path, target string
		want                             bool
	}{
		{"InitiateAuth", http.MethodPost, "", "/", targetPrefix + "InitiateAuth", true},
		{"SignUp", http.MethodPost, "", "/", targetPrefix + "SignUp", true},
		{"CreateUserPool", http.MethodPost, "", "/", targetPrefix + "CreateUserPool", false},
		{"AdminInitiateAuth", http.MethodPost, "", "/", targetPrefix + "AdminInitiateAuth", false},
		{"lower-case op", http.MethodPost, "", "/", targetPrefix + "initiateAuth", false},
		{"lower-case prefix", http.MethodPost, "", "/", "awscognitoidentityproviderservice.InitiateAuth", false},
		{"no target", http.MethodPost, "", "/", "", false},
		{"GET", http.MethodGet, "", "/", targetPrefix + "InitiateAuth", false},
		{"hosted-ui path", http.MethodPost, "", "/_cognito/x", targetPrefix + "InitiateAuth", false},
		{"well-known path", http.MethodGet, "", "/us-east-1_abc/.well-known/jwks.json", targetPrefix + "GetUser", false},
		{"hosted-ui host, private op", http.MethodPost, "x.auth.localhost", "/", targetPrefix + "CreateUserPool", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://localhost"+tc.path, nil)
			if tc.host != "" {
				r.Host = tc.host
			}

			if tc.target != "" {
				r.Header.Set("X-Amz-Target", tc.target)
			}

			if got := (&Handler{}).PublicRequest(r); got != tc.want {
				t.Fatalf("PublicRequest = %v, want %v", got, tc.want)
			}
		})
	}
}
