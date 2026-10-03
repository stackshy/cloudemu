package loadbalancer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// TestGCPLBListFilterAndPaging covers GLB-23: list filters honour any wire
// field (not only name), maxResults pages with a keyset token, and a malformed
// filter is a 400.
func TestGCPLBListFilterAndPaging(t *testing.T) {
	ts := newGCPLBServer(t)
	ctx := context.Background()

	frs := newForwardingRulesClient(t, ts.URL, option.WithHTTPClient(ts.Client()))

	for name, scheme := range map[string]string{"fr-a": "EXTERNAL_MANAGED", "fr-b": "EXTERNAL", "fr-c": "EXTERNAL_MANAGED"} {
		op, err := frs.Insert(ctx, &computepb.InsertGlobalForwardingRuleRequest{
			Project: testProject,
			ForwardingRuleResource: &computepb.ForwardingRule{
				Name: ptrStr(name), IPProtocol: ptrStr("TCP"), PortRange: ptrStr("443"), LoadBalancingScheme: ptrStr(scheme),
			},
		})
		if err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}

		if err := op.Wait(ctx); err != nil {
			t.Fatalf("insert %s wait: %v", name, err)
		}
	}

	var managed []string

	it := frs.List(ctx, &computepb.ListGlobalForwardingRulesRequest{
		Project: testProject, Filter: ptrStr("loadBalancingScheme = EXTERNAL_MANAGED"), MaxResults: proto32(1),
	})

	for {
		fr, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("list forwarding rules: %v", err)
		}

		managed = append(managed, fr.GetName())
	}

	if strings.Join(managed, ",") != "fr-a,fr-c" {
		t.Errorf("loadBalancingScheme filter returned %v, want [fr-a fr-c]", managed)
	}

	hcs, err := gcpcompute.NewHealthChecksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewHealthChecksRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = hcs.Close() })

	for name, typ := range map[string]string{"hc-a": "HTTP", "hc-b": "TCP", "hc-c": "HTTP"} {
		hc := &computepb.HealthCheck{Name: ptrStr(name), Type: ptrStr(typ)}
		if typ == "HTTP" {
			hc.HttpHealthCheck = &computepb.HTTPHealthCheck{Port: ptrI32(80)}
		} else {
			hc.TcpHealthCheck = &computepb.TCPHealthCheck{Port: ptrI32(80)}
		}

		op, err := hcs.Insert(ctx, &computepb.InsertHealthCheckRequest{Project: testProject, HealthCheckResource: hc})
		if err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}

		if err := op.Wait(ctx); err != nil {
			t.Fatalf("insert %s wait: %v", name, err)
		}
	}

	page := getList(t, ts.URL+"/compute/v1/projects/"+testProject+"/global/healthChecks?maxResults=2&filter="+
		url.QueryEscape("type = HTTP"), http.StatusOK)
	if len(page.Items) != 2 || page.NextPageToken != "" {
		t.Errorf("type = HTTP returned %d items (token %q), want hc-a and hc-c on one page", len(page.Items), page.NextPageToken)
	}

	first := getList(t, ts.URL+"/compute/v1/projects/"+testProject+"/global/healthChecks?maxResults=2", http.StatusOK)
	if len(first.Items) != 2 || first.NextPageToken == "" {
		t.Fatalf("maxResults=2 over 3 health checks: %d items, token %q", len(first.Items), first.NextPageToken)
	}

	rest := getList(t, ts.URL+"/compute/v1/projects/"+testProject+"/global/healthChecks?maxResults=2&pageToken="+
		url.QueryEscape(first.NextPageToken), http.StatusOK)
	if len(rest.Items) != 1 || rest.Items[0].Name != "hc-c" || rest.NextPageToken != "" {
		t.Errorf("page 2 = %+v, want [hc-c] and no token", rest)
	}

	getList(t, ts.URL+"/compute/v1/projects/"+testProject+"/global/healthChecks?filter="+url.QueryEscape("name = ("),
		http.StatusBadRequest)
}

type namedList struct {
	Items []struct {
		Name string `json:"name"`
	} `json:"items"`
	NextPageToken string `json:"nextPageToken"`
}

func getList(t *testing.T, u string, wantStatus int) namedList {
	t.Helper()

	resp, err := http.Get(u) //nolint:gosec,noctx // test server URL
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: status %d, want %d", u, resp.StatusCode, wantStatus)
	}

	var out namedList

	_ = json.NewDecoder(resp.Body).Decode(&out)

	return out
}

func proto32(n uint32) *uint32 { return &n }
