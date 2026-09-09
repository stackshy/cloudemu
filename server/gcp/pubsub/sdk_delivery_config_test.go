package pubsub_test

import (
	"context"
	"testing"

	pubsubv1 "google.golang.org/api/pubsub/v1"
)

// TestSDKPubSubBigQuerySubscriptionRoundTrip guards that a BigQuery
// subscription's bigqueryConfig survives create -> get. Terraform's
// google_pubsub_subscription bigquery_config block would otherwise show
// perpetual drift because the returned resource drops the field.
func TestSDKPubSubBigQuerySubscriptionRoundTrip(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/bq")

	_, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/bq-sub",
		&pubsubv1.Subscription{
			Topic: "projects/demo/topics/bq",
			BigqueryConfig: &pubsubv1.BigQueryConfig{
				Table:          "my-project.my_dataset.my_table",
				UseTopicSchema: true,
				WriteMetadata:  true,
			},
		}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create BigQuery subscription: %v", err)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/bq-sub").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.BigqueryConfig == nil {
		t.Fatal("bigqueryConfig dropped on round-trip (nil on Get)")
	}

	if got.BigqueryConfig.Table != "my-project.my_dataset.my_table" {
		t.Errorf("bigqueryConfig.table = %q, want my-project.my_dataset.my_table", got.BigqueryConfig.Table)
	}

	if !got.BigqueryConfig.UseTopicSchema {
		t.Error("bigqueryConfig.useTopicSchema = false, want true")
	}
}

// TestSDKPubSubCloudStorageSubscriptionRoundTrip guards cloudStorageConfig
// round-trip for a Cloud Storage subscription (google_pubsub_subscription
// cloud_storage_config).
func TestSDKPubSubCloudStorageSubscriptionRoundTrip(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/gcs")

	_, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/gcs-sub",
		&pubsubv1.Subscription{
			Topic: "projects/demo/topics/gcs",
			CloudStorageConfig: &pubsubv1.CloudStorageConfig{
				Bucket:         "my-bucket",
				FilenamePrefix: "prefix-",
				FilenameSuffix: ".txt",
				MaxDuration:    "300s",
			},
		}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create Cloud Storage subscription: %v", err)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/gcs-sub").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.CloudStorageConfig == nil {
		t.Fatal("cloudStorageConfig dropped on round-trip (nil on Get)")
	}

	if got.CloudStorageConfig.Bucket != "my-bucket" {
		t.Errorf("cloudStorageConfig.bucket = %q, want my-bucket", got.CloudStorageConfig.Bucket)
	}

	if got.CloudStorageConfig.MaxDuration != "300s" {
		t.Errorf("cloudStorageConfig.maxDuration = %q, want 300s", got.CloudStorageConfig.MaxDuration)
	}
}

// TestSDKPubSubBigQueryConfigPatch guards that bigqueryConfig can be updated
// through subscriptions.patch with an updateMask naming it.
func TestSDKPubSubBigQueryConfigPatch(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/bqp")
	mustSub(t, svc, "projects/demo/subscriptions/bqp-sub", &pubsubv1.Subscription{
		Topic: "projects/demo/topics/bqp",
		BigqueryConfig: &pubsubv1.BigQueryConfig{
			Table: "p.d.t1",
		},
	})

	_, err := svc.Projects.Subscriptions.Patch("projects/demo/subscriptions/bqp-sub",
		&pubsubv1.UpdateSubscriptionRequest{
			Subscription: &pubsubv1.Subscription{
				BigqueryConfig: &pubsubv1.BigQueryConfig{Table: "p.d.t2"},
			},
			UpdateMask: "bigqueryConfig",
		}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/bqp-sub").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.BigqueryConfig == nil || got.BigqueryConfig.Table != "p.d.t2" {
		t.Fatalf("bigqueryConfig.table after patch = %+v, want p.d.t2", got.BigqueryConfig)
	}
}
