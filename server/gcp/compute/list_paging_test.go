package compute_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/proto"
)

var pagingNames = []string{"d-a", "d-b", "d-c", "d-d", "d-e"}

func TestComputeDiskListPagingAndFilter(t *testing.T) {
	ts := newGCPTestServer(t)
	client := newDisksSDKClient(t, ts)
	ctx := context.Background()

	for i, n := range pagingNames {
		env := "a"
		if i%2 == 1 {
			env = "b"
		}

		insertDisk(t, client, &computepb.Disk{Name: proto.String(n), SizeGb: proto.Int64(10), Labels: map[string]string{"env": env}})
	}

	list := func(filter string) []string {
		req := &computepb.ListDisksRequest{Project: testProject, Zone: testZone, MaxResults: proto.Uint32(1)}
		if filter != "" {
			req.Filter = proto.String(filter)
		}

		var names []string

		it := client.List(ctx, req)

		for {
			d, err := it.Next()
			if err == iterator.Done {
				break
			}

			if err != nil {
				t.Fatalf("list %q: %v", filter, err)
			}

			names = append(names, d.GetName())
		}

		return names
	}

	tests := []struct {
		filter string
		want   []string
	}{
		{"", pagingNames},
		{"labels.env = a", []string{"d-a", "d-c", "d-e"}},
		{"name != d-a", []string{"d-b", "d-c", "d-d", "d-e"}},
		{`(labels.env = "b") (name = d-d)`, []string{"d-d"}},
		{"(name = d-a) OR (name = d-e)", []string{"d-a", "d-e"}},
		{`name eq "d-[bc]"`, []string{"d-b", "d-c"}},
		{"name = d-*", pagingNames},
	}

	for _, tc := range tests {
		got := list(tc.filter)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("filter %q with maxResults=1: got %v, want %v", tc.filter, got, tc.want)
		}
	}

	page := rawPage(t, ts.URL+zonesPath("/disks?maxResults=2"), http.StatusOK)
	var items []json.RawMessage
	if err := json.Unmarshal(page.Items, &items); err != nil || len(items) != 2 || page.NextPageToken == "" {
		t.Errorf("maxResults=2: %d items (%v), token %q; want 2 items and a token", len(items), err, page.NextPageToken)
	}

	for _, q := range []string{"maxResults=501", "maxResults=-1", "pageToken=bogus", "filter=" + url.QueryEscape("name = (")} {
		rawPage(t, ts.URL+zonesPath("/disks?"+q), http.StatusBadRequest)
	}

	for _, f := range []string{"someUnknownField = x", "someUnknownField != x", "-name = d-a", "labels = x AND nope.x = y"} {
		rawPage(t, ts.URL+zonesPath("/disks?filter="+url.QueryEscape(f)), http.StatusBadRequest)
	}

	for _, raw := range items {
		var d struct {
			ID string `json:"id"`
		}

		_ = json.Unmarshal(raw, &d)

		if _, err := strconv.ParseInt(d.ID, 10, 64); err != nil {
			t.Errorf("disk id %q does not fit int64: %v", d.ID, err)
		}
	}

	empty := rawBody(t, ts.URL+zonesPath("/disks?filter="+url.QueryEscape("name = nope")))
	if !strings.Contains(empty, `"items":[]`) {
		t.Errorf("empty filtered list body %s, want \"items\":[]", empty)
	}

	// The cursor from page 1 (after d-b) survives an insert before it and the
	// delete of the next item: page 2 neither repeats d-b nor returns 400.
	insertDisk(t, client, &computepb.Disk{Name: proto.String("d-0"), SizeGb: proto.Int64(10)})

	del, err := client.Delete(ctx, &computepb.DeleteDiskRequest{Project: testProject, Zone: testZone, Disk: "d-c"})
	if err != nil {
		t.Fatalf("Delete d-c: %v", err)
	}

	if err := del.Wait(ctx); err != nil {
		t.Fatalf("Delete d-c wait: %v", err)
	}

	next := rawBody(t, ts.URL+zonesPath("/disks?maxResults=2&pageToken="+url.QueryEscape(page.NextPageToken)))
	if !strings.Contains(next, `"name":"d-d"`) || !strings.Contains(next, `"name":"d-e"`) ||
		strings.Contains(next, `"name":"d-b"`) || strings.Contains(next, `"name":"d-0"`) {
		t.Errorf("page 2 after insert+delete = %s, want exactly d-d and d-e", next)
	}

	stale := rawBody(t, ts.URL+zonesPath("/disks?filter=name%3Dd-a&pageToken="+url.QueryEscape(page.NextPageToken)))
	if !strings.Contains(stale, `"items":[]`) {
		t.Errorf("cursor past every match = %s, want 200 with no items", stale)
	}
}

func rawBody(t *testing.T, u string) string {
	t.Helper()

	resp, err := http.Get(u) //nolint:gosec,noctx // test server URL
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", u, resp.StatusCode, body)
	}

	return string(body)
}

func TestComputeInstanceListStatusFilterAndAggregatedPaging(t *testing.T) {
	client, ts, ctx := newInstancesEnv(t)

	for _, n := range pagingNames {
		mustInsert(t, client, testZone, &computepb.Instance{
			Name: proto.String(n), MachineType: proto.String("zones/" + testZone + "/machineTypes/n1-standard-1"),
		})
	}

	op, err := client.Stop(ctx, &computepb.StopInstanceRequest{Project: testProject, Zone: testZone, Instance: "d-c"})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Stop wait: %v", err)
	}

	stopped := listNames(t, client.List(ctx, &computepb.ListInstancesRequest{
		Project: testProject, Zone: testZone, Filter: proto.String("status = TERMINATED"),
	}))
	if strings.Join(stopped, ",") != "d-c" {
		t.Errorf("status = TERMINATED returned %v, want [d-c]", stopped)
	}

	running := listNames(t, client.List(ctx, &computepb.ListInstancesRequest{
		Project: testProject, Zone: testZone, Filter: proto.String("status != TERMINATED"), MaxResults: proto.Uint32(1),
	}))
	if len(running) != 4 {
		t.Errorf("status != TERMINATED returned %v, want 4 instances", running)
	}

	var (
		seen  []string
		pages int
	)

	agg := client.AggregatedList(ctx, &computepb.AggregatedListInstancesRequest{
		Project: testProject, MaxResults: proto.Uint32(2),
	})

	for {
		pair, err := agg.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("aggregated list: %v", err)
		}

		for _, inst := range pair.Value.GetInstances() {
			seen = append(seen, inst.GetName())
		}
	}

	for token := ""; ; pages++ {
		page := rawPage(t, ts.URL+"/compute/v1/projects/"+testProject+"/aggregated/instances?maxResults=2&pageToken="+
			url.QueryEscape(token), http.StatusOK)
		if token = page.NextPageToken; token == "" {
			pages++
			break
		}
	}

	sort.Strings(seen)

	if strings.Join(seen, ",") != strings.Join(pagingNames, ",") {
		t.Errorf("aggregated maxResults=2 walk saw %v, want %v", seen, pagingNames)
	}

	if pages != 3 {
		t.Errorf("aggregated maxResults=2 over 5 instances took %d pages, want 3", pages)
	}
}

type rawListPage struct {
	Items         json.RawMessage `json:"items"`
	NextPageToken string          `json:"nextPageToken"`
}

func rawPage(t *testing.T, u string, wantStatus int) rawListPage {
	t.Helper()

	resp, err := http.Get(u) //nolint:gosec,noctx // test server URL
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: status %d, want %d", u, resp.StatusCode, wantStatus)
	}

	var out rawListPage

	if wantStatus == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode %s: %v", u, err)
		}
	}

	return out
}

// TestComputeNumericIDsFitInt64 pins ids to int64 range: the Terraform
// provider parses image, disk and machine type ids with ParseInt.
func TestComputeNumericIDsFitInt64(t *testing.T) {
	ts := newGCPTestServer(t)

	var list struct {
		Items []struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(rawBody(t, ts.URL+zonesPath("/machineTypes"))), &list); err != nil {
		t.Fatalf("decode machineTypes: %v", err)
	}

	if len(list.Items) < 4 {
		t.Fatalf("machineTypes returned %d items", len(list.Items))
	}

	for _, it := range list.Items {
		if _, err := strconv.ParseInt(it.ID, 10, 64); err != nil {
			t.Errorf("machineType %s id %q does not fit int64: %v", it.Name, it.ID, err)
		}
	}
}
