package bigtable_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	bt "google.golang.org/api/bigtableadmin/v2"
)

// mkInstance creates an instance (with a single cluster) via the real SDK.
func mkInstance(t *testing.T, svc *bt.Service, id string, labels map[string]string) {
	t.Helper()

	op, err := svc.Projects.Instances.Create(instanceParent(), &bt.CreateInstanceRequest{
		InstanceId: id,
		Instance:   &bt.Instance{DisplayName: id, Type: "PRODUCTION", Labels: labels},
		Clusters: map[string]bt.Cluster{
			"c1": {Location: "projects/" + project + "/locations/us-central1-a", ServeNodes: 3, DefaultStorageType: "SSD"},
		},
	}).Do()
	if err != nil {
		t.Fatalf("create instance %s: %v", id, err)
	}

	if !op.Done {
		t.Fatalf("create instance %s: op not done", id)
	}
}

// assertPagedNames checks that names gathered across pages are the expected
// count, in stable ascending order (no gap), with no duplicates.
func assertPagedNames(t *testing.T, got []string, want int) {
	t.Helper()

	if len(got) != want {
		t.Fatalf("paged names: got %d %v, want %d", len(got), got, want)
	}

	if !sort.StringsAreSorted(got) {
		t.Fatalf("paged names not in stable ascending order: %v", got)
	}

	seen := make(map[string]bool, len(got))
	for _, n := range got {
		if seen[n] {
			t.Fatalf("duplicate name across pages: %q in %v", n, got)
		}

		seen[n] = true
	}
}

// getInstances lists instances over the raw wire. The bigtableadmin SDK exposes
// no PageSize on the instances list call (the real API does not page it), so the
// server's pagination is exercised directly with the real response type.
func getInstances(t *testing.T, base, query string) *bt.ListInstancesResponse {
	t.Helper()

	resp, err := http.Get(base + "/v2/" + instanceParent() + "/instances?" + query) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("GET instances: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test helper

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET instances: status %d", resp.StatusCode)
	}

	var out bt.ListInstancesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode instances: %v", err)
	}

	return &out
}

func TestSDKListInstancesPaging(t *testing.T) {
	svc, ts := newSDKClientServer(t)
	ids := []string{"i-a", "i-b", "i-c"}

	for _, id := range ids {
		mkInstance(t, svc, id, nil)
	}

	p1 := getInstances(t, ts.URL, "pageSize=2")
	if len(p1.Instances) != 2 || p1.NextPageToken == "" {
		t.Fatalf("page 1: got %d instances, token %q; want 2 + token", len(p1.Instances), p1.NextPageToken)
	}

	p2 := getInstances(t, ts.URL, "pageSize=2&pageToken="+p1.NextPageToken)
	if len(p2.Instances) != 1 || p2.NextPageToken != "" {
		t.Fatalf("page 2: got %d instances, token %q; want 1 + empty", len(p2.Instances), p2.NextPageToken)
	}

	got := make([]string, 0, len(ids))
	for _, in := range append(append([]*bt.Instance{}, p1.Instances...), p2.Instances...) {
		got = append(got, in.Name)
	}

	assertPagedNames(t, got, len(ids))
}

func TestSDKListTablesPaging(t *testing.T) {
	svc := newSDKClient(t)
	inst := createAppInstance(t, svc)

	for _, id := range []string{"t-a", "t-b", "t-c"} {
		if _, err := svc.Projects.Instances.Tables.Create(inst, &bt.CreateTableRequest{TableId: id}).Do(); err != nil {
			t.Fatalf("create table %s: %v", id, err)
		}
	}

	p1, err := svc.Projects.Instances.Tables.List(inst).PageSize(2).Do()
	if err != nil || len(p1.Tables) != 2 || p1.NextPageToken == "" {
		t.Fatalf("tables page 1: %v len=%d token=%q", err, len(p1.Tables), p1.NextPageToken)
	}

	p2, err := svc.Projects.Instances.Tables.List(inst).PageSize(2).PageToken(p1.NextPageToken).Do()
	if err != nil || len(p2.Tables) != 1 || p2.NextPageToken != "" {
		t.Fatalf("tables page 2: %v len=%d token=%q", err, len(p2.Tables), p2.NextPageToken)
	}

	got := make([]string, 0, 3)
	for _, x := range p1.Tables {
		got = append(got, x.Name)
	}

	for _, x := range p2.Tables {
		got = append(got, x.Name)
	}

	assertPagedNames(t, got, 3)
}

func TestSDKListAppProfilesPaging(t *testing.T) {
	svc := newSDKClient(t)
	inst := createAppInstance(t, svc)

	for _, id := range []string{"ap-a", "ap-b", "ap-c"} {
		if _, err := svc.Projects.Instances.AppProfiles.Create(inst, &bt.AppProfile{
			SingleClusterRouting: &bt.SingleClusterRouting{ClusterId: "c1"},
		}).AppProfileId(id).Do(); err != nil {
			t.Fatalf("create app profile %s: %v", id, err)
		}
	}

	p1, err := svc.Projects.Instances.AppProfiles.List(inst).PageSize(2).Do()
	if err != nil || len(p1.AppProfiles) != 2 || p1.NextPageToken == "" {
		t.Fatalf("appProfiles page 1: %v len=%d token=%q", err, len(p1.AppProfiles), p1.NextPageToken)
	}

	p2, err := svc.Projects.Instances.AppProfiles.List(inst).PageSize(2).PageToken(p1.NextPageToken).Do()
	if err != nil || len(p2.AppProfiles) != 1 || p2.NextPageToken != "" {
		t.Fatalf("appProfiles page 2: %v len=%d token=%q", err, len(p2.AppProfiles), p2.NextPageToken)
	}

	got := make([]string, 0, 3)
	for _, x := range p1.AppProfiles {
		got = append(got, x.Name)
	}

	for _, x := range p2.AppProfiles {
		got = append(got, x.Name)
	}

	assertPagedNames(t, got, 3)
}

func TestSDKListBackupsPaging(t *testing.T) {
	svc := newSDKClient(t)
	inst := createAppInstance(t, svc)
	cluster := inst + "/clusters/c1"

	if _, err := svc.Projects.Instances.Tables.Create(inst, &bt.CreateTableRequest{TableId: "src"}).Do(); err != nil {
		t.Fatalf("create table: %v", err)
	}

	for _, id := range []string{"b-a", "b-b", "b-c"} {
		if _, err := svc.Projects.Instances.Clusters.Backups.Create(cluster, &bt.Backup{
			SourceTable: inst + "/tables/src", ExpireTime: "2030-01-01T00:00:00Z",
		}).BackupId(id).Do(); err != nil {
			t.Fatalf("create backup %s: %v", id, err)
		}
	}

	p1, err := svc.Projects.Instances.Clusters.Backups.List(cluster).PageSize(2).Do()
	if err != nil || len(p1.Backups) != 2 || p1.NextPageToken == "" {
		t.Fatalf("backups page 1: %v len=%d token=%q", err, len(p1.Backups), p1.NextPageToken)
	}

	p2, err := svc.Projects.Instances.Clusters.Backups.List(cluster).PageSize(2).PageToken(p1.NextPageToken).Do()
	if err != nil || len(p2.Backups) != 1 || p2.NextPageToken != "" {
		t.Fatalf("backups page 2: %v len=%d token=%q", err, len(p2.Backups), p2.NextPageToken)
	}

	got := make([]string, 0, 3)
	for _, x := range p1.Backups {
		got = append(got, x.Name)
	}

	for _, x := range p2.Backups {
		got = append(got, x.Name)
	}

	assertPagedNames(t, got, 3)
}

func TestSDKUpdateMaskInstancePreservesAndClears(t *testing.T) {
	svc := newSDKClient(t)
	mkInstance(t, svc, "im", map[string]string{"env": "prod", "team": "data"})
	inst := "projects/" + project + "/instances/im"

	// A displayName-only mask must leave labels and type untouched.
	if _, err := svc.Projects.Instances.PartialUpdateInstance(inst,
		&bt.Instance{DisplayName: "Renamed"}).UpdateMask("displayName").Do(); err != nil {
		t.Fatalf("partial update displayName: %v", err)
	}

	got, err := svc.Projects.Instances.Get(inst).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.DisplayName != "Renamed" {
		t.Fatalf("displayName not applied: %q", got.DisplayName)
	}

	if len(got.Labels) != 2 || got.Labels["env"] != "prod" || got.Labels["team"] != "data" {
		t.Fatalf("labels not preserved under displayName mask: %+v", got.Labels)
	}

	// A labels mask with an empty body clears labels (masked field written even
	// when empty) while leaving the unmasked displayName intact.
	if _, err := svc.Projects.Instances.PartialUpdateInstance(inst,
		&bt.Instance{}).UpdateMask("labels").Do(); err != nil {
		t.Fatalf("partial update clear labels: %v", err)
	}

	got, _ = svc.Projects.Instances.Get(inst).Do()
	if len(got.Labels) != 0 {
		t.Fatalf("labels not cleared by mask: %+v", got.Labels)
	}

	if got.DisplayName != "Renamed" {
		t.Fatalf("displayName changed by an unrelated labels mask: %q", got.DisplayName)
	}
}

func TestSDKUpdateMaskTablePreservesDeletionProtection(t *testing.T) {
	svc := newSDKClient(t)
	inst := createAppInstance(t, svc)
	name := inst + "/tables/dp"

	if _, err := svc.Projects.Instances.Tables.Create(inst, &bt.CreateTableRequest{
		TableId: "dp", Table: &bt.Table{DeletionProtection: true},
	}).Do(); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Patching an unrelated masked field must NOT reset deletionProtection.
	if _, err := svc.Projects.Instances.Tables.Patch(name,
		&bt.Table{}).UpdateMask("changeStreamConfig").Do(); err != nil {
		t.Fatalf("patch table (unrelated field): %v", err)
	}

	got, err := svc.Projects.Instances.Tables.Get(name).Do()
	if err != nil {
		t.Fatalf("get table: %v", err)
	}

	if !got.DeletionProtection {
		t.Fatal("deletionProtection was reset by an unrelated masked patch")
	}

	// Masking deletionProtection itself does change it.
	if _, err := svc.Projects.Instances.Tables.Patch(name,
		&bt.Table{DeletionProtection: false}).UpdateMask("deletionProtection").Do(); err != nil {
		t.Fatalf("patch table (deletionProtection): %v", err)
	}

	got, _ = svc.Projects.Instances.Tables.Get(name).Do()
	if got.DeletionProtection {
		t.Fatal("deletionProtection not cleared when masked")
	}
}

func TestSDKUpdateMaskAbsentKeepsHeuristic(t *testing.T) {
	svc := newSDKClient(t)
	mkInstance(t, svc, "ih", map[string]string{"k": "v"})
	inst := "projects/" + project + "/instances/ih"

	// No updateMask: legacy presence heuristic — a non-empty displayName is
	// applied while empty type/labels are kept.
	if _, err := svc.Projects.Instances.PartialUpdateInstance(inst, &bt.Instance{DisplayName: "H2"}).Do(); err != nil {
		t.Fatalf("partial update (no mask): %v", err)
	}

	got, _ := svc.Projects.Instances.Get(inst).Do()
	if got.DisplayName != "H2" {
		t.Fatalf("displayName: %q", got.DisplayName)
	}

	if got.Type != "PRODUCTION" {
		t.Fatalf("type not preserved by heuristic: %q", got.Type)
	}

	if got.Labels["k"] != "v" {
		t.Fatalf("labels not preserved by heuristic: %+v", got.Labels)
	}
}

func TestSDKCreateDuplicateInstanceConflict(t *testing.T) {
	svc := newSDKClient(t)
	mkInstance(t, svc, "dup", nil)

	_, err := svc.Projects.Instances.Create(instanceParent(), &bt.CreateInstanceRequest{
		InstanceId: "dup",
		Instance:   &bt.Instance{DisplayName: "dup", Type: "PRODUCTION"},
		Clusters: map[string]bt.Cluster{
			"c1": {Location: "projects/" + project + "/locations/us-central1-a", ServeNodes: 3},
		},
	}).Do()
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate create: got %v, want alreadyExists", err)
	}
}
