package monitoring_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	monitoring "google.golang.org/api/monitoring/v3"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newScopedClient(t *testing.T) *monitoring.Service {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return newClient(t, ts)
}

// TestProjectScopeAlertPoliciesIsolated guards that each project is its own
// metrics scope: a policy created in p-a is neither listed nor readable in p-b.
func TestProjectScopeAlertPoliciesIsolated(t *testing.T) {
	svc := newScopedClient(t)

	created, err := svc.Projects.AlertPolicies.Create("projects/p-a", &monitoring.AlertPolicy{
		DisplayName: "cpu", Combiner: "OR",
	}).Do()
	if err != nil {
		t.Fatalf("alertPolicies.create in p-a: %v", err)
	}

	list, err := svc.Projects.AlertPolicies.List("projects/p-b").Do()
	if err != nil {
		t.Fatalf("alertPolicies.list in p-b: %v", err)
	}

	if len(list.AlertPolicies) != 0 {
		t.Fatalf("p-b lists %d policies created in p-a", len(list.AlertPolicies))
	}

	id := created.Name[strings.LastIndex(created.Name, "/")+1:]
	if _, err := svc.Projects.AlertPolicies.Get("projects/p-b/alertPolicies/" + id).Do(); err == nil {
		t.Fatal("p-b reads a policy created in p-a")
	}
}

// TestProjectScopeMetricDescriptorsIsolated guards that the same custom metric
// type in two projects is two descriptors, and a delete in one leaves the other.
func TestProjectScopeMetricDescriptorsIsolated(t *testing.T) {
	svc := newScopedClient(t)

	const mtype = "custom.googleapis.com/widgets"

	descs := map[string]string{"p-a": "from a", "p-b": "from b"}

	for project, desc := range descs {
		if _, err := svc.Projects.MetricDescriptors.Create("projects/"+project, &monitoring.MetricDescriptor{
			Type: mtype, MetricKind: "GAUGE", ValueType: "DOUBLE", Description: desc,
		}).Do(); err != nil {
			t.Fatalf("metricDescriptors.create in %s: %v", project, err)
		}
	}

	for project, desc := range descs {
		got, err := svc.Projects.MetricDescriptors.Get("projects/" + project + "/metricDescriptors/" + mtype).Do()
		if err != nil {
			t.Fatalf("metricDescriptors.get in %s: %v", project, err)
		}

		if got.Description != desc {
			t.Errorf("%s description = %q, want %q", project, got.Description, desc)
		}
	}

	if _, err := svc.Projects.MetricDescriptors.Delete("projects/p-a/metricDescriptors/" + mtype).Do(); err != nil {
		t.Fatalf("metricDescriptors.delete in p-a: %v", err)
	}

	if _, err := svc.Projects.MetricDescriptors.Get("projects/p-b/metricDescriptors/" + mtype).Do(); err != nil {
		t.Fatalf("p-b descriptor gone after deleting p-a's: %v", err)
	}
}
