package gcp

import (
	"context"
	"testing"

	monitoring "google.golang.org/api/monitoring/v3"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestCloudMonitoringCompat drives the real google-api monitoring/v3 client
// against CloudEmu's in-process CloudMonitoring wire server and records one
// compat result per portable monitoring op the handler routes: alert-policy
// create (CreateAlarm), get/list (DescribeAlarms) and delete (DeleteAlarm).
//
// The handler covers only the alert-policy surface; notification channels,
// metric data and alarm-state ops are gaps and are not asserted here.
func TestCloudMonitoringCompat(t *testing.T) {
	const displayName = "compat-high-cpu"

	cloud := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{Monitoring: cloud.CloudMonitoring})
	ctx := context.Background()

	svc, err := monitoring.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("monitoring.NewService: %v", err)
	}

	parent := "projects/" + compat.GCPProject

	var policyName string

	sess.Op("monitoring", "CreateAlarm", func() error {
		created, cerr := svc.Projects.AlertPolicies.Create(parent, &monitoring.AlertPolicy{
			DisplayName: displayName,
			Combiner:    "OR",
		}).Context(ctx).Do()
		if cerr != nil {
			return cerr
		}

		policyName = created.Name

		return nil
	})

	sess.Op("monitoring", "DescribeAlarms", func() error {
		if _, lerr := svc.Projects.AlertPolicies.List(parent).Context(ctx).Do(); lerr != nil {
			return lerr
		}

		_, gerr := svc.Projects.AlertPolicies.Get(policyName).Context(ctx).Do()

		return gerr
	})

	sess.Op("monitoring", "DeleteAlarm", func() error {
		_, derr := svc.Projects.AlertPolicies.Delete(policyName).Context(ctx).Do()

		return derr
	})
}
