// This file exercises database-level shared provisioned throughput: the HIGH
// bug where CreateDatabase with ThroughputProperties never created an offer,
// so ReadThroughput always 404d even for a database explicitly provisioned
// with shared RU/s.

package cosmosdb_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
)

// TestSDKDatabaseThroughput drives CreateDatabase with shared manual
// throughput and asserts ReadThroughput round-trips it.
func TestSDKDatabaseThroughput(t *testing.T) {
	ctx := context.Background()
	client := newThroughputClient(t)

	manual := azcosmos.NewManualThroughputProperties(500)

	if _, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "shareddb"}, &azcosmos.CreateDatabaseOptions{
		ThroughputProperties: &manual,
	}); err != nil {
		t.Fatalf("CreateDatabase with throughput: %v", err)
	}

	dbClient, err := client.NewDatabase("shareddb")
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}

	readResp, err := dbClient.ReadThroughput(ctx, nil)
	if err != nil {
		t.Fatalf("ReadThroughput: %v", err)
	}

	got, ok := readResp.ThroughputProperties.ManualThroughput()
	if !ok || got != 500 {
		t.Errorf("database ReadThroughput manual=%d ok=%v want 500", got, ok)
	}
}

// TestSDKDatabaseThroughputAbsent asserts a database created without shared
// throughput has no offer, matching a container with no dedicated offer.
func TestSDKDatabaseThroughputAbsent(t *testing.T) {
	ctx := context.Background()
	client := newThroughputClient(t)

	if _, err := client.CreateDatabase(ctx, azcosmos.DatabaseProperties{ID: "unshareddb"}, nil); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}

	dbClient, err := client.NewDatabase("unshareddb")
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}

	if _, err := dbClient.ReadThroughput(ctx, nil); err == nil {
		t.Fatal("ReadThroughput on a database with no shared offer: expected error, got nil")
	}
}
