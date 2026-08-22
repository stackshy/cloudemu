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
	"github.com/aws/aws-sdk-go-v2/service/rds"
	_ "github.com/lib/pq"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine/postgres"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAuroraPostgresE2E runs the real-user flow against Amazon Aurora
// (PostgreSQL-compatible): create a DB cluster with master credentials, add a
// db.r6g instance to it, read the CLUSTER endpoint + port the SDK reports,
// connect a real Postgres client to the cluster endpoint using the cluster's
// master credentials, run SQL, then delete — all against CloudEmu backed by a
// real embedded Postgres (no Docker, no cloud account). The client connects
// using ONLY the SDK-reported cluster endpoint + port.
//
// All members of an Aurora cluster share ONE engine database named after the
// cluster (a deterministic choice), so the client connects with dbname set to
// the cluster identifier.
func TestAuroraPostgresE2E(t *testing.T) {
	eng := postgres.New(55451)
	t.Cleanup(func() { _ = eng.Close() })

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

	client := rds.NewFromConfig(cfg, func(o *rds.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	const (
		clusterID  = "aurora-pg"
		instanceID = "aurora-pg-1"
		user       = "clusteradmin"
		password   = "Aurora-Secret-Pw"
	)

	// 1. Create the cluster — like `aws rds create-db-cluster`. The master creds
	//    live on the CLUSTER, not the instance.
	if _, err := client.CreateDBCluster(ctx, &rds.CreateDBClusterInput{
		DBClusterIdentifier: aws.String(clusterID),
		Engine:              aws.String("aurora-postgresql"),
		MasterUsername:      aws.String(user),
		MasterUserPassword:  aws.String(password),
	}); err != nil {
		t.Fatalf("CreateDBCluster: %v", err)
	}

	// 2. Add a db.r6g instance to the cluster — it carries no master creds.
	if _, err := client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String(instanceID),
		DBClusterIdentifier:  aws.String(clusterID),
		Engine:               aws.String("aurora-postgresql"),
		DBInstanceClass:      aws.String("db.r6g.large"),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	// 3. Read the CLUSTER endpoint + port the SDK reports — the real embedded
	//    Postgres address. Connect using ONLY these SDK-reported values.
	desc, err := client.DescribeDBClusters(ctx, &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(clusterID),
	})
	if err != nil {
		t.Fatalf("DescribeDBClusters: %v", err)
	}

	if len(desc.DBClusters) != 1 || desc.DBClusters[0].Endpoint == nil {
		t.Fatalf("no cluster endpoint reported: %+v", desc.DBClusters)
	}

	cl := desc.DBClusters[0]
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		aws.ToString(cl.Endpoint), aws.ToInt32(cl.Port), user, password, clusterID)

	// 4. Connect with a real Postgres client and run real SQL.
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	db.SetConnMaxLifetime(time.Minute)

	if _, err := db.Exec("CREATE TABLE orders (id serial primary key, item text, qty int)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := db.Exec("INSERT INTO orders (item, qty) VALUES ($1, $2)", "widget", 7); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var (
		item string
		qty  int
	)

	if err := db.QueryRow("SELECT item, qty FROM orders WHERE id = 1").Scan(&item, &qty); err != nil {
		t.Fatalf("select: %v", err)
	}

	if item != "widget" || qty != 7 {
		t.Fatalf("round-trip mismatch: got %q, %d", item, qty)
	}

	_ = db.Close()

	// 5. Delete the member, then the cluster — the shared database is torn down.
	if _, err := client.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String(instanceID),
		SkipFinalSnapshot:    aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBInstance: %v", err)
	}

	if _, err := client.DeleteDBCluster(ctx, &rds.DeleteDBClusterInput{
		DBClusterIdentifier: aws.String(clusterID),
		SkipFinalSnapshot:   aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBCluster: %v", err)
	}

	gone, _ := sql.Open("postgres", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deleted cluster's database to fail")
	}
}
