package vpc_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
)

// walkPages follows nextPageToken from base (a list URL with maxResults set)
// and returns every item name it saw plus the page count. Aggregated lists
// carry items as a scope map.
func walkPages(t *testing.T, ts *httptest.Server, base string, aggregatedKey string) (names []string, pages int) {
	t.Helper()

	for token := ""; ; {
		u := ts.URL + base
		if token != "" {
			u += "&pageToken=" + url.QueryEscape(token)
		}

		resp, err := http.Get(u) //nolint:gosec,noctx // test server URL
		if err != nil {
			t.Fatalf("GET %s: %v", u, err)
		}

		var body struct {
			Items         json.RawMessage `json:"items"`
			NextPageToken string          `json:"nextPageToken"`
		}

		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK || err != nil {
			t.Fatalf("GET %s: status %d, decode %v", u, resp.StatusCode, err)
		}

		pages++

		names = append(names, itemNames(t, body.Items, aggregatedKey)...)

		if token = body.NextPageToken; token == "" {
			return names, pages
		}

		if pages > 20 {
			t.Fatalf("GET %s: more than 20 pages", base)
		}
	}
}

func itemNames(t *testing.T, raw json.RawMessage, aggregatedKey string) []string {
	t.Helper()

	type named struct {
		Name string `json:"name"`
	}

	var out []string

	if aggregatedKey == "" {
		var items []named

		_ = json.Unmarshal(raw, &items)

		for _, it := range items {
			out = append(out, it.Name)
		}

		return out
	}

	var scopes map[string]map[string]json.RawMessage

	_ = json.Unmarshal(raw, &scopes)

	for _, s := range scopes {
		var items []named

		_ = json.Unmarshal(s[aggregatedKey], &items)

		for _, it := range items {
			out = append(out, it.Name)
		}
	}

	return out
}

func statusOf(t *testing.T, ts *httptest.Server, path string) int {
	t.Helper()

	resp, err := http.Get(ts.URL + path) //nolint:gosec,noctx // test server URL
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	resp.Body.Close()

	return resp.StatusCode
}

func assertWalk(t *testing.T, what string, names []string, pages int, want []string) {
	t.Helper()

	sort.Strings(names)

	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("%s: walked %v, want %v exactly once each", what, names, want)
	}

	if pages != len(want) {
		t.Errorf("%s: maxResults=1 over %d items took %d pages", what, len(want), pages)
	}
}

func TestVPCListPagingAndFilter(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()

	netClient, subClient := newNetAndSubnetClients(t, ctx, ts, "pg-a")

	op, err := netClient.Insert(ctx, &computepb.InsertNetworkRequest{Project: testProject, NetworkResource: &computepb.Network{
		Name: proto.String("pg-b"), AutoCreateSubnetworks: proto.Bool(false),
		RoutingConfig: &computepb.NetworkRoutingConfig{RoutingMode: proto.String("GLOBAL")},
	}})
	if err != nil {
		t.Fatalf("insert pg-b: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("insert pg-b wait: %v", err)
	}

	names, pages := walkPages(t, ts, "/compute/v1/projects/"+testProject+"/global/networks?maxResults=1", "")
	assertWalk(t, "networks", names, pages, []string{"pg-a", "pg-b"})

	global, _ := walkPages(t, ts, "/compute/v1/projects/"+testProject+"/global/networks?maxResults=1&filter="+
		url.QueryEscape("routingConfig.routingMode = GLOBAL"), "")
	if strings.Join(global, ",") != "pg-b" {
		t.Errorf("networks filter routingConfig.routingMode = GLOBAL returned %v, want [pg-b]", global)
	}

	insertSubnet2(t, ctx, subClient, "us-central1", "sub-a", "pg-a", "10.1.0.0/24")
	insertSubnet2(t, ctx, subClient, "us-central1", "sub-b", "pg-a", "10.2.0.0/24")
	insertSubnet2(t, ctx, subClient, "us-east1", "sub-c", "pg-a", "10.3.0.0/24")

	names, pages = walkPages(t, ts, "/compute/v1/projects/"+testProject+"/aggregated/subnetworks?maxResults=1", "subnetworks")
	assertWalk(t, "aggregated subnetworks", names, pages, []string{"sub-a", "sub-b", "sub-c"})

	east, _ := walkPages(t, ts, "/compute/v1/projects/"+testProject+"/aggregated/subnetworks?maxResults=500&filter="+
		url.QueryEscape(`region eq ".*us-east1"`), "subnetworks")
	if strings.Join(east, ",") != "sub-c" {
		t.Errorf("aggregated subnetworks region filter returned %v, want [sub-c]", east)
	}

	if got := statusOf(t, ts, "/compute/v1/projects/"+testProject+"/global/firewalls?filter="+url.QueryEscape("name = (")); got != http.StatusBadRequest {
		t.Errorf("malformed firewall filter status %d, want 400", got)
	}

	routers := newRoutersClient(t, ts)

	for _, n := range []string{"rt-a", "rt-b", "rt-c"} {
		op, err := routers.Insert(ctx, &computepb.InsertRouterRequest{Project: testProject, Region: testRegion, RouterResource: &computepb.Router{
			Name: proto.String(n), Network: proto.String("projects/" + testProject + "/global/networks/pg-a"),
		}})
		if err != nil {
			t.Fatalf("insert router %s: %v", n, err)
		}

		if err := op.Wait(ctx); err != nil {
			t.Fatalf("insert router %s wait: %v", n, err)
		}
	}

	names, pages = walkPages(t, ts, "/compute/v1/projects/"+testProject+"/regions/"+testRegion+"/routers?maxResults=1", "")
	assertWalk(t, "routers", names, pages, []string{"rt-a", "rt-b", "rt-c"})

	it := routers.List(ctx, &computepb.ListRoutersRequest{Project: testProject, Region: testRegion, Filter: proto.String("name != rt-b")})

	var kept []string

	for {
		r, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("routers list: %v", err)
		}

		kept = append(kept, r.GetName())
	}

	if strings.Join(kept, ",") != "rt-a,rt-c" {
		t.Errorf("routers filter name != rt-b returned %v, want [rt-a rt-c]", kept)
	}
}

func TestVPCAddressAggregatedPagingAndLabelFilter(t *testing.T) {
	ts := newGCPNetServer(t)
	ctx := context.Background()
	client, err := gcpcompute.NewAddressesRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("NewAddressesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	insertAddr(t, ctx, client, "us-central1", "ad-a")
	insertAddr(t, ctx, client, "us-central1", "ad-b")
	insertAddr(t, ctx, client, "us-east1", "ad-c")

	names, pages := walkPages(t, ts, "/compute/v1/projects/"+testProject+"/aggregated/addresses?maxResults=1", "addresses")
	assertWalk(t, "aggregated addresses", names, pages, []string{"ad-a", "ad-b", "ad-c"})

	external, _ := walkPages(t, ts, "/compute/v1/projects/"+testProject+"/regions/us-central1/addresses?maxResults=1&filter="+
		url.QueryEscape("(name = ad-a) OR (name = ad-b) addressType = EXTERNAL"), "")
	if strings.Join(external, ",") != "ad-a,ad-b" {
		t.Errorf("addresses compound filter returned %v, want [ad-a ad-b]", external)
	}
}
