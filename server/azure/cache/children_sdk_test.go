package cache_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3"
)

// newRedisFactory returns a client factory sharing the server behind
// newRedisClientAndServer, for the child clients.
func newRedisFactory(t *testing.T) *armredis.ClientFactory {
	t.Helper()

	_, ts := newRedisClientAndServer(t)

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armredis.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	return cf
}

func statusOf(t *testing.T, err error) int {
	t.Helper()

	var re *azcore.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("error %v is not a ResponseError", err)
	}

	return re.StatusCode
}

func patchParams(day armredis.DayOfWeek, hour int32) armredis.PatchSchedule {
	return armredis.PatchSchedule{Properties: &armredis.ScheduleEntries{
		ScheduleEntries: []*armredis.ScheduleEntry{{DayOfWeek: to.Ptr(day), StartHourUTC: to.Ptr(hour)}},
	}}
}

func TestSDKRedisPatchScheduleLifecycle(t *testing.T) {
	cf := newRedisFactory(t)
	ctx := context.Background()
	createCache(t, cf.NewClient(), "patch-cache")

	ps := cf.NewPatchSchedulesClient()
	def := armredis.DefaultNameDefault

	if _, err := ps.Get(ctx, testRG, "patch-cache", def, nil); err == nil || statusOf(t, err) != http.StatusNotFound {
		t.Fatalf("Get before PUT: err=%v, want 404", err)
	}

	var resp *http.Response

	cctx := runtime.WithCaptureResponse(ctx, &resp)

	got, err := ps.CreateOrUpdate(cctx, testRG, "patch-cache", def, patchParams(armredis.DayOfWeekWeekend, 2), nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("first PUT status = %d, want 201", resp.StatusCode)
	}

	if *got.Name != "patch-cache/default" || *got.Type != "Microsoft.Cache/Redis/PatchSchedules" {
		t.Errorf("name/type = %s/%s", *got.Name, *got.Type)
	}

	if _, err := ps.CreateOrUpdate(cctx, testRG, "patch-cache", def, patchParams(armredis.DayOfWeekSaturday, 4), nil); err != nil {
		t.Fatalf("second CreateOrUpdate: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("second PUT status = %d, want 200", resp.StatusCode)
	}

	read, err := ps.Get(ctx, testRG, "patch-cache", def, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	e := read.Properties.ScheduleEntries[0]
	if *e.DayOfWeek != armredis.DayOfWeekSaturday || *e.StartHourUTC != 4 || *e.MaintenanceWindow != "PT5H" {
		t.Errorf("entry = %s/%d/%s, want Saturday/4/PT5H", *e.DayOfWeek, *e.StartHourUTC, *e.MaintenanceWindow)
	}

	pager := ps.NewListByRedisResourcePager(testRG, "patch-cache", nil)

	page, err := pager.NextPage(ctx)
	if err != nil || len(page.Value) != 1 {
		t.Fatalf("list: err=%v len=%d, want 1", err, len(page.Value))
	}

	if _, err := ps.Delete(cctx, testRG, "patch-cache", def, nil); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Delete: err=%v status=%d, want 200", err, resp.StatusCode)
	}

	if _, err := ps.Delete(cctx, testRG, "patch-cache", def, nil); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second Delete: err=%v status=%d, want 204", err, resp.StatusCode)
	}
}

func TestSDKRedisPatchScheduleRejects(t *testing.T) {
	cf := newRedisFactory(t)
	ctx := context.Background()
	createCache(t, cf.NewClient(), "patch-bad")

	ps := cf.NewPatchSchedulesClient()

	tests := []struct {
		name  string
		cache string
		body  armredis.PatchSchedule
		want  int
	}{
		{"hour out of range", "patch-bad", patchParams(armredis.DayOfWeekMonday, 24), http.StatusBadRequest},
		{"bad day", "patch-bad", patchParams(armredis.DayOfWeek("Funday"), 1), http.StatusBadRequest},
		{"missing cache", "nope", patchParams(armredis.DayOfWeekMonday, 1), http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ps.CreateOrUpdate(ctx, testRG, tc.cache, armredis.DefaultNameDefault, tc.body, nil)
			if err == nil || statusOf(t, err) != tc.want {
				t.Fatalf("err = %v, want %d", err, tc.want)
			}
		})
	}
}

func fwRule(start, end string) armredis.FirewallRule {
	return armredis.FirewallRule{Properties: &armredis.FirewallRuleProperties{StartIP: to.Ptr(start), EndIP: to.Ptr(end)}}
}

func TestSDKRedisFirewallRules(t *testing.T) {
	cf := newRedisFactory(t)
	ctx := context.Background()
	redis := cf.NewClient()
	createCache(t, redis, "fw-cache")

	fw := cf.NewFirewallRulesClient()

	var resp *http.Response

	cctx := runtime.WithCaptureResponse(ctx, &resp)

	if _, err := fw.CreateOrUpdate(cctx, testRG, "fw-cache", "r1", fwRule("10.0.0.1", "10.0.0.9"), nil); err != nil {
		t.Fatalf("PUT r1: %v", err)
	}

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("create status = %d, want 201", resp.StatusCode)
	}

	got, err := fw.CreateOrUpdate(cctx, testRG, "fw-cache", "r1", fwRule("10.0.0.1", "10.0.0.20"), nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("update r1: err=%v status=%d, want 200", err, resp.StatusCode)
	}

	if *got.Name != "fw-cache/r1" || *got.Properties.EndIP != "10.0.0.20" {
		t.Errorf("got %s end %s", *got.Name, *got.Properties.EndIP)
	}

	if _, err := fw.CreateOrUpdate(ctx, testRG, "fw-cache", "bad", fwRule("10.0.0.9", "10.0.0.1"), nil); err == nil ||
		statusOf(t, err) != http.StatusBadRequest {
		t.Errorf("start > end: err=%v, want 400", err)
	}

	if _, err := fw.CreateOrUpdate(ctx, testRG, "fw-cache", "r2", fwRule("10.1.0.1", "10.1.0.1"), nil); err != nil {
		t.Fatalf("PUT r2: %v", err)
	}

	page, err := fw.NewListPager(testRG, "fw-cache", nil).NextPage(ctx)
	if err != nil || len(page.Value) != 2 {
		t.Fatalf("list: err=%v len=%d, want 2", err, len(page.Value))
	}

	if _, err := fw.Delete(cctx, testRG, "fw-cache", "r2", nil); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Delete r2: err=%v status=%d", err, resp.StatusCode)
	}

	if _, err := fw.Get(ctx, testRG, "fw-cache", "r2", nil); err == nil || statusOf(t, err) != http.StatusNotFound {
		t.Errorf("Get deleted r2: err=%v, want 404", err)
	}

	// Deleting the cache removes its rules: a same-name cache starts empty.
	poller, err := redis.BeginDelete(ctx, testRG, "fw-cache", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete poll: %v", err)
	}

	if _, err := fw.Get(ctx, testRG, "fw-cache", "r1", nil); err == nil || statusOf(t, err) != http.StatusNotFound {
		t.Errorf("Get rule of deleted cache: err=%v, want 404", err)
	}

	createCache(t, redis, "fw-cache")

	page, err = fw.NewListPager(testRG, "fw-cache", nil).NextPage(ctx)
	if err != nil || len(page.Value) != 0 {
		t.Fatalf("recreated cache rules: err=%v len=%d, want 0", err, len(page.Value))
	}
}
