package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	_ "github.com/lib/pq"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine/postgres"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestRedshiftPostgresE2E performs the exact flow a real user runs against AWS
// Redshift: create a cluster with the AWS SDK, read its endpoint, connect to it
// with a real Postgres client using the master credentials, run SQL, then delete
// the cluster — except it all runs against CloudEmu backed by a real embedded
// Postgres (no Docker, no cloud account). Redshift speaks the Postgres wire
// protocol, so it reuses the shared Postgres DatabaseEngine.
func TestRedshiftPostgresE2E(t *testing.T) {
	eng := postgres.New(0)
	t.Cleanup(func() { _ = eng.Close() })

	// Boot the AWS wire server with the real Postgres engine wired in.
	cloud := cloudemu.NewAWS(config.WithDatabaseEngine(eng))
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := redshift.NewFromConfig(cfg, func(o *redshift.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	const (
		clusterID = "warehouse"
		dbName    = "analytics"
		user      = "admin"
		password  = "Sup3rSecret!"
	)

	// 1. Create the cluster — exactly like `aws redshift create-cluster`.
	_, err = client.CreateCluster(ctx, &redshift.CreateClusterInput{
		ClusterIdentifier:  aws.String(clusterID),
		NodeType:           aws.String("ra3.xlplus"),
		MasterUsername:     aws.String(user),
		MasterUserPassword: aws.String(password),
		DBName:             aws.String(dbName),
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	// 2. Read the endpoint the SDK reports — the real embedded Postgres address.
	desc, err := client.DescribeClusters(ctx, &redshift.DescribeClustersInput{
		ClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		t.Fatalf("DescribeClusters: %v", err)
	}

	if len(desc.Clusters) != 1 || desc.Clusters[0].Endpoint == nil {
		t.Fatalf("no endpoint reported: %+v", desc.Clusters)
	}

	ep := desc.Clusters[0].Endpoint
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		aws.ToString(ep.Address), aws.ToInt32(ep.Port), user, password, dbName)

	// 3. Connect with a real Postgres client and run real SQL.
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	db.SetConnMaxLifetime(time.Minute)

	if _, err := db.Exec("CREATE TABLE events (id serial primary key, name text, count int)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := db.Exec("INSERT INTO events (name, count) VALUES ($1, $2)", "click", 42); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var (
		name  string
		count int
	)

	if err := db.QueryRow("SELECT name, count FROM events WHERE id = 1").Scan(&name, &count); err != nil {
		t.Fatalf("select: %v", err)
	}

	if name != "click" || count != 42 {
		t.Fatalf("round-trip mismatch: got %q, %d", name, count)
	}

	_ = db.Close()

	// 4. Delete the cluster — the real database is torn down.
	if _, err := client.DeleteCluster(ctx, &redshift.DeleteClusterInput{
		ClusterIdentifier:        aws.String(clusterID),
		SkipFinalClusterSnapshot: aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}

	gone, _ := sql.Open("postgres", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deleted cluster's database to fail")
	}
}
