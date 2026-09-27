package eks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
	"github.com/stackshy/cloudemu/v2/services/kubernetes"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// fakeSubnets resolves subnet ids to fixed availability zones.
type fakeSubnets map[string]string

func (f fakeSubnets) DescribeSubnets(_ context.Context, ids []string) ([]netdriver.SubnetInfo, error) {
	out := make([]netdriver.SubnetInfo, 0, len(ids))
	for _, id := range ids {
		if az, ok := f[id]; ok {
			out = append(out, netdriver.SubnetInfo{ID: id, VPCID: "vpc-1", AvailabilityZone: az})
		}
	}

	return out, nil
}

// nodesFixture wires a data plane into m, creates cluster c1 and returns the
// base URL of its Kubernetes API.
func nodesFixture(t *testing.T, m *Mock) string {
	t.Helper()

	api := kubernetes.NewAPIServer()
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)
	api.SetBaseURL(ts.URL)
	m.SetK8sAPI(api)
	mustCluster(t, m, "c1")

	return ts.URL + "/k8s/" + m.k8sUIDs["c1"]
}

type testNode struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		ProviderID string `json:"providerID"`
		Taints     []struct {
			Key, Value, Effect string
		} `json:"taints"`
	} `json:"spec"`
	Status struct {
		Capacity    map[string]string `json:"capacity"`
		Allocatable map[string]string `json:"allocatable"`
		NodeInfo    map[string]string `json:"nodeInfo"`
		Conditions  []struct {
			Type, Status string
		} `json:"conditions"`
	} `json:"status"`
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()

	resp, err := http.Get(url) //nolint:noctx // test against a local httptest server
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func listNodes(t *testing.T, base string) []testNode {
	t.Helper()

	var list struct {
		Items []testNode `json:"items"`
	}

	getJSON(t, base+"/api/v1/nodes", &list)

	return list.Items
}

func TestNodegroupNodesCreatedWithEKSShape(t *testing.T) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc), config.WithRegion("eu-west-1"),
		config.WithAccountID("123456789012")))
	m.SetSubnetResolver(fakeSubnets{"subnet-a": "eu-west-1a", "subnet-b": "eu-west-1b"})
	base := nodesFixture(t, m)

	_, err := m.CreateNodegroup(context.Background(), eksdriver.NodegroupConfig{
		ClusterName: "c1", NodegroupName: "workers",
		Subnets:       []string{"subnet-a", "subnet-b"},
		InstanceTypes: []string{"m5.large"},
		AmiType:       "AL2023_ARM_64_STANDARD",
		CapacityType:  "SPOT",
		ScalingConfig: eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 3, DesiredSize: 2},
		Labels:        map[string]string{"team": "payments"},
		Taints:        []eksdriver.Taint{{Key: "dedicated", Value: "batch", Effect: "NO_SCHEDULE"}},
	})
	requireNoError(t, err)

	nodes := listNodes(t, base)
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}

	zones := map[string]bool{}

	for _, n := range nodes {
		name := n.Metadata.Name
		if !strings.HasPrefix(name, "ip-10-0-") || !strings.HasSuffix(name, ".eu-west-1.compute.internal") {
			t.Fatalf("node name %q, want ip-10-0-x-x.eu-west-1.compute.internal", name)
		}

		want := map[string]string{
			"eks.amazonaws.com/nodegroup":    "workers",
			"eks.amazonaws.com/capacityType": "SPOT",
			"node.kubernetes.io/instance-type": "m5.large",
			"topology.kubernetes.io/region":    "eu-west-1",
			"kubernetes.io/os":                 "linux",
			"kubernetes.io/arch":               "arm64",
			"kubernetes.io/hostname":           name,
			"team":                             "payments",
		}
		for k, v := range want {
			if n.Metadata.Labels[k] != v {
				t.Fatalf("node %s label %s = %q, want %q (labels %v)", name, k, n.Metadata.Labels[k], v, n.Metadata.Labels)
			}
		}

		if !strings.HasPrefix(n.Metadata.Labels["eks.amazonaws.com/nodegroup-image"], "ami-") {
			t.Fatalf("node %s nodegroup-image label = %q", name, n.Metadata.Labels["eks.amazonaws.com/nodegroup-image"])
		}

		zone := n.Metadata.Labels["topology.kubernetes.io/zone"]
		zones[zone] = true

		if !strings.HasPrefix(n.Spec.ProviderID, "aws:///"+zone+"/i-") {
			t.Fatalf("node %s providerID = %q", name, n.Spec.ProviderID)
		}

		if len(n.Spec.Taints) != 1 || n.Spec.Taints[0].Effect != "NoSchedule" || n.Spec.Taints[0].Key != "dedicated" {
			t.Fatalf("node %s taints = %+v, want dedicated=batch:NoSchedule", name, n.Spec.Taints)
		}

		if n.Status.Capacity["cpu"] != "2" || n.Status.Allocatable["cpu"] != "1930m" || n.Status.Capacity["pods"] != "29" {
			t.Fatalf("node %s capacity %v allocatable %v, want m5.large shape", name, n.Status.Capacity, n.Status.Allocatable)
		}

		if !strings.HasPrefix(n.Status.NodeInfo["kubeletVersion"], "v1.") ||
			!strings.Contains(n.Status.NodeInfo["kubeletVersion"], "-eks-") {
			t.Fatalf("node %s kubeletVersion = %q", name, n.Status.NodeInfo["kubeletVersion"])
		}

		if n.Status.NodeInfo["architecture"] != "arm64" {
			t.Fatalf("node %s architecture = %q", name, n.Status.NodeInfo["architecture"])
		}

		ready := false

		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
			}
		}

		if !ready {
			t.Fatalf("node %s not Ready", name)
		}
	}

	if !zones["eu-west-1a"] || !zones["eu-west-1b"] {
		t.Fatalf("nodes not spread over the subnet zones: %v", zones)
	}
}

func TestNodegroupNodesUSEast1Hostname(t *testing.T) {
	m := newTestMock()
	base := nodesFixture(t, m)

	_, err := m.CreateNodegroup(context.Background(), eksdriver.NodegroupConfig{
		ClusterName: "c1", NodegroupName: "ng",
		ScalingConfig: eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 1, DesiredSize: 1},
	})
	requireNoError(t, err)

	nodes := listNodes(t, base)
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}

	// us-east-1 is the one region whose private DNS names use ec2.internal.
	if name := nodes[0].Metadata.Name; !strings.HasSuffix(name, ".ec2.internal") {
		t.Fatalf("us-east-1 node name %q, want ip-...ec2.internal", name)
	}

	if got := nodes[0].Metadata.Labels["eks.amazonaws.com/capacityType"]; got != "ON_DEMAND" {
		t.Fatalf("default capacityType label = %q, want ON_DEMAND", got)
	}

	if got := nodes[0].Metadata.Labels["kubernetes.io/arch"]; got != "amd64" {
		t.Fatalf("default arch label = %q, want amd64", got)
	}
}

func TestNodegroupNodesScaleUpdateAndDelete(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	base := nodesFixture(t, m)

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName: "c1", NodegroupName: "ng",
		ScalingConfig: eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 3, DesiredSize: 2},
		Labels:        map[string]string{"old": "1"},
		Taints:        []eksdriver.Taint{{Key: "k", Value: "v", Effect: "NO_EXECUTE"}},
	})
	requireNoError(t, err)

	pod := `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"web"},` +
		`"spec":{"tolerations":[{"operator":"Exists"}],"containers":[{"name":"c","image":"nginx"}]}}`

	resp, err := http.Post(base+"/api/v1/namespaces/default/pods", "application/json", strings.NewReader(pod)) //nolint:noctx // test
	requireNoError(t, err)
	resp.Body.Close()

	scale := func(n int) {
		t.Helper()

		_, err := m.UpdateNodegroupConfig(ctx, "c1", "ng", eksdriver.NodegroupConfigUpdate{
			Scaling: &eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 3, DesiredSize: n},
		})
		requireNoError(t, err)
	}

	scale(3)
	assertEqual(t, 3, len(listNodes(t, base)))

	scale(1)

	nodes := listNodes(t, base)
	assertEqual(t, 1, len(nodes))

	var got struct {
		Spec   struct{ NodeName string } `json:"spec"`
		Status struct{ Phase string }    `json:"status"`
	}

	getJSON(t, base+"/api/v1/namespaces/default/pods/web", &got)

	if got.Status.Phase != "Running" || got.Spec.NodeName != nodes[0].Metadata.Name {
		t.Fatalf("pod after scale-down: node=%q phase=%q, want Running on %s",
			got.Spec.NodeName, got.Status.Phase, nodes[0].Metadata.Name)
	}

	_, err = m.UpdateNodegroupConfig(ctx, "c1", "ng", eksdriver.NodegroupConfigUpdate{
		AddOrUpdateLabels: map[string]string{"new": "2"},
		RemoveLabels:      []string{"old"},
		AddOrUpdateTaints: []eksdriver.Taint{{Key: "k2", Value: "v2", Effect: "PREFER_NO_SCHEDULE"}},
		RemoveTaints:      []eksdriver.Taint{{Key: "k", Effect: "NO_EXECUTE"}},
	})
	requireNoError(t, err)

	updated := listNodes(t, base)
	assertEqual(t, 1, len(updated))
	assertEqual(t, nodes[0].Metadata.Name, updated[0].Metadata.Name)

	if updated[0].Metadata.Labels["new"] != "2" || updated[0].Metadata.Labels["old"] != "" {
		t.Fatalf("labels after update = %v", updated[0].Metadata.Labels)
	}

	if len(updated[0].Spec.Taints) != 1 || updated[0].Spec.Taints[0].Effect != "PreferNoSchedule" {
		t.Fatalf("taints after update = %+v", updated[0].Spec.Taints)
	}

	_, err = m.DeleteNodegroup(ctx, "c1", "ng")
	requireNoError(t, err)
	assertEqual(t, 0, len(listNodes(t, base)))

	got.Spec.NodeName, got.Status.Phase = "", ""
	getJSON(t, base+"/api/v1/namespaces/default/pods/web", &got)

	if got.Status.Phase != "Pending" || got.Spec.NodeName != "" {
		t.Fatalf("pod after nodegroup delete: node=%q phase=%q, want Pending", got.Spec.NodeName, got.Status.Phase)
	}
}

func TestNodegroupNodesVersionUpdateRefreshesKubelet(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	base := nodesFixture(t, m)

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName: "c1", NodegroupName: "ng", Version: "1.29",
		ScalingConfig: eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 1, DesiredSize: 1},
	})
	requireNoError(t, err)

	if v := listNodes(t, base)[0].Status.NodeInfo["kubeletVersion"]; !strings.HasPrefix(v, "v1.29.") {
		t.Fatalf("kubeletVersion = %q, want v1.29.x", v)
	}

	_, err = m.UpdateNodegroupVersion(ctx, "c1", "ng", eksdriver.NodegroupVersionUpdate{})
	requireNoError(t, err)

	ng, err := m.DescribeNodegroup(ctx, "c1", "ng")
	requireNoError(t, err)

	if v := listNodes(t, base)[0].Status.NodeInfo["kubeletVersion"]; !strings.HasPrefix(v, "v"+ng.Version+".") {
		t.Fatalf("kubeletVersion after version update = %q, want v%s.x", v, ng.Version)
	}
}

func TestNodegroupNodesSurviveSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	api := kubernetes.NewAPIServer()
	m.SetK8sAPI(api)
	mustCluster(t, m, "c1")

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName: "c1", NodegroupName: "ng",
		ScalingConfig: eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 3, DesiredSize: 2},
	})
	requireNoError(t, err)

	eksSnap, err := m.Snapshot(ctx, false)
	requireNoError(t, err)
	k8sSnap, err := api.Snapshot(ctx, false)
	requireNoError(t, err)

	m2 := newTestMock()
	api2 := kubernetes.NewAPIServer()
	m2.SetK8sAPI(api2)
	requireNoError(t, m2.Restore(ctx, eksSnap))
	requireNoError(t, api2.Restore(ctx, k8sSnap))

	ts := httptest.NewServer(api2)
	defer ts.Close()

	base := ts.URL + "/k8s/" + m2.k8sUIDs["c1"]

	before := map[string]bool{}
	for _, n := range listNodes(t, base) {
		before[n.Metadata.Name] = true
	}

	assertEqual(t, 2, len(before))

	_, err = m2.UpdateNodegroupConfig(ctx, "c1", "ng", eksdriver.NodegroupConfigUpdate{
		Scaling: &eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 3, DesiredSize: 3},
	})
	requireNoError(t, err)

	after := listNodes(t, base)
	assertEqual(t, 3, len(after))

	kept := 0

	for _, n := range after {
		if before[n.Metadata.Name] {
			kept++
		}
	}

	assertEqual(t, 2, kept)
}

// TestKubeletVersionCoversSupportedVersions checks that every Kubernetes minor
// the provider accepts gets a real patch release in its kubelet version.
func TestKubeletVersionCoversSupportedVersions(t *testing.T) {
	for minor := supportedMinMinor; minor <= catalogMaxMinor; minor++ {
		version := fmt.Sprintf("1.%d", minor)
		requireNoError(t, validateKubernetesVersion(version))

		got := nodegroupNodeInfo(&eksdriver.Nodegroup{Version: version}, "linux", "amd64").KubeletVersion

		prefix := "v" + version + "."
		if !strings.HasPrefix(got, prefix) || !strings.Contains(got, "-eks-") {
			t.Fatalf("version %s: kubeletVersion = %q", version, got)
		}

		patch := strings.SplitN(strings.TrimPrefix(got, prefix), "-", 2)[0]
		if patch == "" || patch == "0" {
			t.Fatalf("version %s: kubeletVersion %q has no patch release", version, got)
		}
	}
}

func TestNodegroupNodesWithoutDataPlane(t *testing.T) {
	m := newTestMock()
	mustCluster(t, m, "c1")

	_, err := m.CreateNodegroup(context.Background(), eksdriver.NodegroupConfig{
		ClusterName: "c1", NodegroupName: "ng",
		ScalingConfig: eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 2, DesiredSize: 2},
	})
	requireNoError(t, err)

	_, err = m.DeleteNodegroup(context.Background(), "c1", "ng")
	requireNoError(t, err)
}
