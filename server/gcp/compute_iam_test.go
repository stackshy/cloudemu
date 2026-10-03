package gcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

const (
	iamBase   = "/compute/v1/projects/iam-p"
	iamZone   = iamBase + "/zones/us-central1-a"
	iamRegion = iamBase + "/regions/us-central1"
	iamSet    = `{"policy":{"bindings":[{"role":"roles/compute.viewer","members":["user:a@example.com"]}]}}`
)

// iamResource is one compute resource with IAM verbs: the parents it needs
// (path, body pairs), the collection and body that create it, and its item path.
type iamResource struct {
	name       string
	parents    [][2]string
	collection string
	body       string
	item       string
}

func mustDo(t *testing.T, ts *httptest.Server, method, path, body string) string {
	t.Helper()

	code, out := do(t, ts, method, path, body)
	if code != http.StatusOK {
		t.Fatalf("%s %s = %d %.300s", method, path, code, out)
	}

	return out
}

func (res iamResource) setup(t *testing.T, ts *httptest.Server) {
	t.Helper()

	for _, p := range res.parents {
		mustDo(t, ts, http.MethodPost, p[0], p[1])
	}

	res.create(t, ts)
}

func (res iamResource) create(t *testing.T, ts *httptest.Server) {
	t.Helper()
	mustDo(t, ts, http.MethodPost, res.collection, res.body)
}

func iamResources() []iamResource {
	srcDisk := func(name string) [][2]string {
		return [][2]string{{iamZone + "/disks", `{"name":"` + name + `","sizeGb":"10"}`}}
	}

	return []iamResource{
		{
			"subnetwork", [][2]string{{iamBase + "/global/networks", `{"name":"n","autoCreateSubnetworks":false}`}},
			iamRegion + "/subnetworks", `{"name":"s","network":"projects/iam-p/global/networks/n","ipCidrRange":"10.0.0.0/24"}`,
			iamRegion + "/subnetworks/s",
		},
		{"disk", nil, iamZone + "/disks", `{"name":"d","sizeGb":"10"}`, iamZone + "/disks/d"},
		{
			"instance", nil, iamZone + "/instances",
			`{"name":"vm","machineType":"zones/us-central1-a/machineTypes/e2-small"}`, iamZone + "/instances/vm",
		},
		{
			"snapshot", srcDisk("src"), iamBase + "/global/snapshots",
			`{"name":"snap","sourceDisk":"projects/iam-p/zones/us-central1-a/disks/src"}`, iamBase + "/global/snapshots/snap",
		},
		{
			"image", srcDisk("isrc"), iamBase + "/global/images",
			`{"name":"img","sourceDisk":"projects/iam-p/zones/us-central1-a/disks/isrc"}`, iamBase + "/global/images/img",
		},
		{
			"instance template", nil, iamBase + "/global/instanceTemplates",
			`{"name":"tpl","properties":{"machineType":"e2-small"}}`, iamBase + "/global/instanceTemplates/tpl",
		},
		{
			"backend service", nil, iamBase + "/global/backendServices", `{"name":"bs","protocol":"HTTP"}`,
			iamBase + "/global/backendServices/bs",
		},
		{
			"region backend service", nil, iamRegion + "/backendServices",
			`{"name":"rbs","protocol":"TCP","loadBalancingScheme":"INTERNAL"}`, iamRegion + "/backendServices/rbs",
		},
	}
}

// TestComputeResourceIAMVerbs checks getIamPolicy returns a Policy (not the
// resource body), setIamPolicy round-trips, a missing resource is 404, and a
// resource deleted and created again starts with an empty policy.
func TestComputeResourceIAMVerbs(t *testing.T) {
	for _, res := range iamResources() {
		t.Run(res.name, func(t *testing.T) {
			ts := fullServer(t)

			code, _ := do(t, ts, http.MethodGet, res.item+"/getIamPolicy", "")
			if code != http.StatusNotFound {
				t.Errorf("getIamPolicy before create = %d, want 404", code)
			}

			res.setup(t, ts)

			got := policyOf(t, mustDo(t, ts, http.MethodGet, res.item+"/getIamPolicy", ""))
			if got.Kind != "" || got.Etag == "" || len(got.Bindings) != 0 {
				t.Fatalf("unset policy = %+v, want an empty Policy with an etag", got)
			}

			mustDo(t, ts, http.MethodPost, res.item+"/setIamPolicy", iamSet)

			got = policyOf(t, mustDo(t, ts, http.MethodGet, res.item+"/getIamPolicy", ""))
			if len(got.Bindings) != 1 || got.Bindings[0].Role != "roles/compute.viewer" {
				t.Fatalf("policy after set = %+v", got)
			}

			test := mustDo(t, ts, http.MethodPost, res.item+"/testIamPermissions", `{"permissions":["compute.disks.get"]}`)
			if !strings.Contains(test, "compute.disks.get") {
				t.Errorf("testIamPermissions = %s", test)
			}

			mustDo(t, ts, http.MethodDelete, res.item, "")
			res.create(t, ts)

			got = policyOf(t, mustDo(t, ts, http.MethodGet, res.item+"/getIamPolicy", ""))
			if len(got.Bindings) != 0 {
				t.Errorf("policy after delete and recreate = %+v, want empty", got)
			}
		})
	}
}

type policyJSON struct {
	Kind     string `json:"kind"`
	Etag     string `json:"etag"`
	Bindings []struct {
		Role    string   `json:"role"`
		Members []string `json:"members"`
	} `json:"bindings"`
}

func policyOf(t *testing.T, body string) policyJSON {
	t.Helper()

	var p policyJSON
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode policy %.300s: %v", body, err)
	}

	return p
}

// TestSubnetworkIAMGapic drives the subnetwork IAM verbs through the compute
// gapic client, the path google_compute_subnetwork_iam_* takes.
func TestSubnetworkIAMGapic(t *testing.T) {
	ts := fullServer(t)
	ctx := context.Background()

	iamResources()[0].setup(t, ts)

	c, err := gcpcompute.NewSubnetworksRESTClient(ctx,
		option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatal(err)
	}

	defer c.Close()

	role, member := "roles/compute.networkUser", "serviceAccount:sa@iam-p.iam.gserviceaccount.com"

	if _, err := c.SetIamPolicy(ctx, &computepb.SetIamPolicySubnetworkRequest{
		Project: "iam-p", Region: "us-central1", Resource: "s",
		RegionSetPolicyRequestResource: &computepb.RegionSetPolicyRequest{Policy: &computepb.Policy{
			Bindings: []*computepb.Binding{{Role: &role, Members: []string{member}}},
		}},
	}); err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	got, err := c.GetIamPolicy(ctx, &computepb.GetIamPolicySubnetworkRequest{Project: "iam-p", Region: "us-central1", Resource: "s"})
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(got.GetBindings()) != 1 || got.GetBindings()[0].GetMembers()[0] != member {
		t.Errorf("bindings = %v", got.GetBindings())
	}
}
