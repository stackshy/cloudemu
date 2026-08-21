package gcp

import (
	"context"
	"testing"
	"time"

	logging "google.golang.org/api/logging/v2"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestCloudLoggingCompat drives the real google.golang.org/api/logging/v2
// client against CloudEmu's in-process CloudLogging wire server and records one
// compat result per portable logging op the handler routes: writing entries
// (PutLogEvents), listing them back (GetLogEvents), listing logs
// (ListLogGroups) and deleting a log (DeleteLogGroup).
func TestCloudLoggingCompat(t *testing.T) {
	const logID = "compat-log"

	cloud := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{CloudLogging: cloud.CloudLogging})
	ctx := context.Background()

	svc, err := logging.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("logging.NewService: %v", err)
	}

	project := compat.GCPProject
	logName := "projects/" + project + "/logs/" + logID
	base := time.Now().UTC().Truncate(time.Millisecond)

	sess.Op("logging", "PutLogEvents", func() error {
		_, werr := svc.Entries.Write(&logging.WriteLogEntriesRequest{
			LogName: logName,
			Resource: &logging.MonitoredResource{
				Type:   "global",
				Labels: map[string]string{"project_id": project},
			},
			Entries: []*logging.LogEntry{
				{Timestamp: base.Format(time.RFC3339Nano), TextPayload: "hello compat"},
				{Timestamp: base.Add(time.Second).Format(time.RFC3339Nano), TextPayload: "second line"},
			},
		}).Context(ctx).Do()

		return werr
	})

	sess.Op("logging", "GetLogEvents", func() error {
		_, lerr := svc.Entries.List(&logging.ListLogEntriesRequest{
			ResourceNames: []string{"projects/" + project},
			Filter:        `logName="` + logName + `"`,
		}).Context(ctx).Do()

		return lerr
	})

	sess.Op("logging", "ListLogGroups", func() error {
		_, lerr := svc.Projects.Logs.List("projects/" + project).Context(ctx).Do()

		return lerr
	})

	sess.Op("logging", "DeleteLogGroup", func() error {
		_, derr := svc.Projects.Logs.Delete(logName).Context(ctx).Do()

		return derr
	})
}
