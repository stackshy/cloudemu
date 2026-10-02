package awsidentity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/authctx"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const testAccount = "123456789012"

// fakeKeys maps access keys to users like the IAM driver does.
type fakeKeys struct {
	iamdriver.IAM
	users map[string]iamdriver.AccessKeyAuth
}

func (f fakeKeys) AccessKeyByID(_ context.Context, id string) (iamdriver.AccessKeyAuth, bool) {
	info, ok := f.users[id]

	return info, ok
}

func signedRequest(akid string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	if akid != "" {
		r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+akid+
			"/20250101/us-east-1/sts/aws4_request, SignedHeaders=host, Signature=abc")
		r.Header.Set("X-Amz-Date", "20250101T000000Z")
	}

	return r
}

func TestResolve(t *testing.T) {
	keys := fakeKeys{users: map[string]iamdriver.AccessKeyAuth{
		"AKIAALICE": {UserARN: "arn:aws:iam::123456789012:user/alice", UserID: "AIDAALICE"},
	}}

	r := New(testAccount, keys)
	r.Remember("ASIASESSION", Identity{ARN: "arn:aws:sts::123456789012:assumed-role/ops/s1", UserID: "AROA:s1"})

	verified := authctx.WithPrincipal(context.Background(),
		authctx.Principal{AccessKeyID: "AKIABOB", ARN: "arn:aws:iam::123456789012:user/bob"})

	tests := []struct {
		name    string
		req     *http.Request
		wantARN string
	}{
		{"no credentials", signedRequest(""), "arn:aws:iam::123456789012:user/cloudemu"},
		{"minted session", signedRequest("ASIASESSION"), "arn:aws:sts::123456789012:assumed-role/ops/s1"},
		{"iam user key", signedRequest("AKIAALICE"), "arn:aws:iam::123456789012:user/alice"},
		{"unknown key", signedRequest("AKIAOTHER"), "arn:aws:iam::123456789012:user/AKIAOTHER"},
		{"verified principal", signedRequest("AKIABOB").WithContext(verified), "arn:aws:iam::123456789012:user/bob"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Resolve(tc.req)
			if got.ARN != tc.wantARN {
				t.Fatalf("ARN = %q, want %q", got.ARN, tc.wantARN)
			}

			if got.UserID == "" {
				t.Fatal("UserID is empty")
			}
		})
	}
}

func TestRememberIgnoresEmpty(t *testing.T) {
	r := New(testAccount, nil)
	r.Remember("", Identity{ARN: "x"})
	r.Remember("ASIAEMPTY", Identity{})

	if got := r.Resolve(signedRequest("ASIAEMPTY")); !strings.HasSuffix(got.ARN, ":user/ASIAEMPTY") {
		t.Fatalf("ARN = %q, want the synthetic identity", got.ARN)
	}
}

func TestDefaultAccount(t *testing.T) {
	if got := New("", nil).Resolve(signedRequest("")); got.ARN != "arn:aws:iam::000000000000:user/cloudemu" {
		t.Fatalf("ARN = %q", got.ARN)
	}
}

func TestSyntheticUserIDStable(t *testing.T) {
	a, b := syntheticUserID("AKIA1"), syntheticUserID("AKIA2")
	if a != syntheticUserID("AKIA1") || a == b || !strings.HasPrefix(a, "AIDA") || len(a) != len("AIDA")+syntheticUserIDHexLen {
		t.Fatalf("ids %q %q", a, b)
	}

	if syntheticUserID("") != defaultUserID {
		t.Fatal("empty key should give the default id")
	}
}
