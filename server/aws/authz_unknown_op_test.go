package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	"github.com/stackshy/cloudemu/v2/server"
)

// providerState is every service's resource state, without CloudTrail's
// management-event log, which records each call (failed ones too) like real
// CloudTrail does.
func providerState(t *testing.T, cloud *awsprovider.Provider) []byte {
	t.Helper()

	ps, err := persist.Export(context.Background(), cloud.SnapshotServices(), persist.Options{IncludeAssets: true})
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	delete(ps.Services, "cloudtrail")

	// The id counter moves with every id minted (CloudTrail event ids too);
	// it is not resource state.
	ps.IDCounter = 0

	raw, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return raw
}

// TestUnknownQueryActionHasNoSideEffect backs the unknown-operation plan: an
// Action a query handler does not know is let through for unrestricted
// callers only because the handler then answers with an error and changes
// nothing. It checks that for each of the ten query handlers, both on the
// handler's own default branch and through the gate as a shortcut principal.
func TestUnknownQueryActionHasNoSideEffect(t *testing.T) {
	cloud := cloudemu.NewAWS()
	d := DriversFrom(cloud)
	d.EnforceAuth = true

	srv, _ := newServer(d)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	boot := userWithPolicy(t, cloud, "boot", "")

	byType := map[string]server.Handler{}
	for _, h := range srv.Handlers() {
		byType[fmt.Sprintf("%T", h)] = h
	}

	const unknown = "Action=NoSuchAction&Version=2010-05-08"

	for _, tc := range []struct{ handler, scope string }{
		{"*iam.Handler", "iam"},
		{"*sts.Handler", "sts"},
		{"*rds.Handler", "rds"},
		{"*redshift.Handler", "redshift"},
		{"*elasticache.Handler", "elasticache"},
		{"*elbv2.Handler", "elasticloadbalancing"},
		{"*sns.Handler", "sns"},
		{"*cloudformation.Handler", "cloudformation"},
		{"*cloudwatch.Handler", "monitoring"},
		{"*ec2.Handler", "ec2"},
	} {
		t.Run(tc.handler, func(t *testing.T) {
			h, ok := byType[tc.handler]
			if !ok {
				t.Fatalf("%s not registered", tc.handler)
			}

			before := providerState(t, cloud)

			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(unknown))
			req.Header.Set("Content-Type", formCT)
			req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260101/us-east-1/"+tc.scope+
				"/aws4_request, SignedHeaders=host, Signature=0")

			if err := req.ParseForm(); err != nil {
				t.Fatalf("parse: %v", err)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code < http.StatusBadRequest || rec.Code >= http.StatusInternalServerError {
				t.Fatalf("default branch answered %d: %s", rec.Code, rec.Body)
			}

			for _, body := range []string{unknown, "Version=2010-05-08"} {
				if status, resp := doSigned(t, ts, boot, form(tc.scope, body)); status < http.StatusBadRequest ||
					status >= http.StatusInternalServerError {
					t.Fatalf("through the gate %q answered %d: %s", body, status, resp)
				}
			}

			if after := providerState(t, cloud); !bytes.Equal(before, after) {
				t.Fatal("an unknown Action changed backend state")
			}
		})
	}

	t.Run("a restricted caller is denied an Action the handler cannot name", func(t *testing.T) {
		dyn := userWithPolicy(t, cloud, "dynonly", allowDynamo)
		status, body := doSigned(t, ts, dyn, form("ec2", "Version=2016-11-15"))
		wantDenied(t, status, body, "ec2:UnknownOperation")
	})
}

// TestUnknownRESTOpHasNoSideEffect is the REST counterpart: a path or method
// an op-level REST handler cannot name is let through for unrestricted
// callers only because the handler then answers with an error and changes
// nothing, and a restricted caller is denied it.
func TestUnknownRESTOpHasNoSideEffect(t *testing.T) {
	cloud := cloudemu.NewAWS()
	d := DriversFrom(cloud)
	d.EnforceAuth = true

	srv, _ := newServer(d)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	boot := userWithPolicy(t, cloud, "boot", "")
	limited := userWithPolicy(t, cloud, "limited", allow("route53:GetHostedZone", "cloudfront:GetDistribution", "eks:DescribeCluster"))

	for _, rq := range []sreq{
		{method: http.MethodGet, path: r53Path + "/Z1/other", service: r53Signed},
		{method: http.MethodPut, path: r53Path + "/Z1", service: r53Signed},
		{method: http.MethodPatch, path: "/2013-04-01/healthcheck/h1", service: r53Signed},
		{method: http.MethodPut, path: "/2013-04-01/tags/hostedzone/Z1", service: r53Signed},
		{method: http.MethodPost, path: "/2013-04-01/tags/bucket/Z1", body: "<ChangeTagsForResourceRequest/>", service: r53Signed},
		{method: http.MethodPost, path: r53Path, ctype: r53XMLCT, body: "<x", service: r53Signed},
		{method: http.MethodGet, path: cfPath + "/E1/other", service: cfSigned},
		{method: http.MethodPatch, path: cfPath + "/E1", service: cfSigned},
		{method: http.MethodPut, path: "/2020-05-31/tagging?Resource=x", service: cfSigned},
		{method: http.MethodPost, path: "/2020-05-31/tagging?Operation=Tag&Resource=arn:aws:cloudfront::999999999999:distribution/E1",
			body: "<Tags/>", service: cfSigned},
		{method: http.MethodGet, path: eksPath + "/c1/other", service: eksSigned},
		{method: http.MethodPut, path: eksPath + "/c1", service: eksSigned},
		{method: http.MethodGet, path: eksPath + "/c1/node-groups/ng/update-config", service: eksSigned},
		{method: http.MethodPost, path: eksPath, ctype: jsonCT, body: "{", service: eksSigned},
		{method: http.MethodPut, path: "/tags/" + eksARN + "cluster/c1", service: eksSigned},
		{method: http.MethodPost, path: "/tags/arn:aws:eks:us-east-1:999999999999:cluster/c1", ctype: jsonCT,
			body: `{"tags":{"k":"v"}}`, service: eksSigned},
		{method: http.MethodPost, path: "/tags/c1", ctype: jsonCT, body: `{"tags":{"k":"v"}}`, service: eksSigned},
	} {
		t.Run(rq.method+" "+rq.path, func(t *testing.T) {
			before := providerState(t, cloud)

			if status, body := doSigned(t, ts, boot, rq); status < http.StatusBadRequest || status >= http.StatusInternalServerError {
				t.Fatalf("shortcut caller got %d: %s", status, body)
			}

			if status, body := doSigned(t, ts, limited, rq); status != http.StatusForbidden {
				t.Fatalf("restricted caller got %d: %s", status, body)
			}

			if after := providerState(t, cloud); !bytes.Equal(before, after) {
				t.Fatal("an unknown operation changed backend state")
			}
		})
	}
}
