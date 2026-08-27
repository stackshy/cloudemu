package gcp

import (
	"context"
	"testing"
	"time"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
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

// TestGCEInstanceMonitoredResourceLabels proves a GCE auto-metric's timeSeries
// carries a gce_instance monitored resource whose labels include zone and
// project_id, so a resource.labels.zone filter matches. Pre-fix the resource
// carried only instance_id (a full path), and the zone/project_id filters
// returned zero series.
func TestGCEInstanceMonitoredResourceLabels(t *testing.T) {
	const zone = "us-central1-a"

	cloud := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{Compute: cloud.GCE, Monitoring: cloud.CloudMonitoring})
	ctx := context.Background()

	opts := []option.ClientOption{
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	}

	instances, err := gcpcompute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewInstancesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = instances.Close() })

	strp := func(s string) *string { return &s }
	boolp := func(b bool) *bool { return &b }

	op, err := instances.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: compat.GCPProject,
		Zone:    zone,
		InstanceResource: &computepb.Instance{
			Name:        strp("metric-vm"),
			MachineType: strp("zones/" + zone + "/machineTypes/n1-standard-1"),
			Disks: []*computepb.AttachedDisk{{
				Boot:       boolp(true),
				AutoDelete: boolp(true),
				InitializeParams: &computepb.AttachedDiskInitializeParams{
					SourceImage: strp("projects/debian-cloud/global/images/family/debian-12"),
				},
			}},
			NetworkInterfaces: []*computepb.NetworkInterface{{Network: strp("global/networks/default")}},
		},
	})
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	if werr := op.Wait(ctx); werr != nil {
		t.Fatalf("wait instance: %v", werr)
	}

	mon, err := monitoring.NewService(ctx, opts...)
	if err != nil {
		t.Fatalf("monitoring.NewService: %v", err)
	}

	now := time.Now().UTC()
	resp, err := mon.Projects.TimeSeries.List("projects/"+compat.GCPProject).
		Filter(`metric.type = "compute.googleapis.com/instance/cpu/utilization" AND resource.labels.zone = "`+zone+`"`).
		IntervalStartTime(now.Add(-1 * time.Hour).Format(time.RFC3339)).
		IntervalEndTime(now.Add(1 * time.Minute).Format(time.RFC3339)).
		Context(ctx).Do()
	if err != nil {
		t.Fatalf("timeSeries.list: %v", err)
	}

	if len(resp.TimeSeries) == 0 {
		t.Fatalf("zone-filtered timeSeries.list returned no series")
	}

	res := resp.TimeSeries[0].Resource
	if res == nil || res.Type != "gce_instance" {
		t.Fatalf("resource = %+v, want gce_instance", res)
	}

	if res.Labels["zone"] != zone {
		t.Fatalf("resource.labels.zone = %q, want %q", res.Labels["zone"], zone)
	}

	if res.Labels["project_id"] == "" {
		t.Fatalf("resource.labels.project_id is empty, want the instance's project")
	}
}

// TestNotificationChannelPatch proves notificationChannels.patch updates a
// channel's displayName/labels in place (keeping its name), rather than 405.
func TestNotificationChannelPatch(t *testing.T) {
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

	created, err := svc.Projects.NotificationChannels.Create(parent, &monitoring.NotificationChannel{
		Type:        "email",
		DisplayName: "on-call",
		Labels:      map[string]string{"email_address": "a@example.com"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	patched, err := svc.Projects.NotificationChannels.Patch(created.Name, &monitoring.NotificationChannel{
		DisplayName: "on-call-updated",
		Labels:      map[string]string{"email_address": "b@example.com"},
	}).UpdateMask("display_name,labels").Context(ctx).Do()
	if err != nil {
		t.Fatalf("patch channel: %v", err)
	}

	if patched.Name != created.Name {
		t.Fatalf("patch changed channel name %q -> %q", created.Name, patched.Name)
	}

	if patched.DisplayName != "on-call-updated" {
		t.Fatalf("patched displayName = %q, want on-call-updated", patched.DisplayName)
	}

	if patched.Labels["email_address"] != "b@example.com" {
		t.Fatalf("patched label email_address = %q, want b@example.com", patched.Labels["email_address"])
	}
}

// TestMetricDescriptorDelete proves metricDescriptors.delete removes a custom
// descriptor (create then delete then get -> 404), rather than 405.
func TestMetricDescriptorDelete(t *testing.T) {
	const mtype = "custom.googleapis.com/compat/widgets"

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

	if _, err := svc.Projects.MetricDescriptors.Create(parent, &monitoring.MetricDescriptor{
		Type:       mtype,
		MetricKind: "GAUGE",
		ValueType:  "DOUBLE",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("create descriptor: %v", err)
	}

	if _, err := svc.Projects.MetricDescriptors.Delete(parent + "/metricDescriptors/" + mtype).Context(ctx).Do(); err != nil {
		t.Fatalf("delete descriptor: %v", err)
	}

	if _, err := svc.Projects.MetricDescriptors.Get(parent + "/metricDescriptors/" + mtype).Context(ctx).Do(); err == nil {
		t.Fatalf("descriptor still readable after delete")
	}
}
