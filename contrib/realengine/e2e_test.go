package realengine_test

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
	"github.com/stackshy/cloudemu/v2/contrib/realengine"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestRDSPostgresE2E performs the exact flow a real user runs against AWS:
// create an RDS Postgres instance with the AWS SDK, read its endpoint, connect
// to it with a real Postgres client using the master credentials, run SQL, then
// delete the instance — except it all runs against CloudEmu backed by a real
// embedded Postgres (no Docker, no cloud account).
func TestRDSPostgresE2E(t *testing.T) {
	eng := realengine.NewPostgres(55450)
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

	client := rds.NewFromConfig(cfg, func(o *rds.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	const (
		instanceID = "app-db"
		dbName     = "appdb"
		user       = "appuser"
		password   = "app-secret-pw"
	)

	// 1. Create the instance — exactly like `aws rds create-db-instance`.
	_, err = client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String(instanceID),
		Engine:               aws.String("postgres"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		MasterUsername:       aws.String(user),
		MasterUserPassword:   aws.String(password),
		DBName:               aws.String(dbName),
	})
	if err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	// 2. Read the endpoint the SDK reports — the real embedded Postgres address.
	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(instanceID),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if len(desc.DBInstances) != 1 || desc.DBInstances[0].Endpoint == nil {
		t.Fatalf("no endpoint reported: %+v", desc.DBInstances)
	}

	ep := desc.DBInstances[0].Endpoint
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		aws.ToString(ep.Address), aws.ToInt32(ep.Port), user, password, dbName)

	// 3. Connect with a real Postgres client and run real SQL.
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

	// 4. Delete the instance — the real database is torn down.
	if _, err := client.DeleteDBInstance(ctx, &rds.DeleteDBInstanceInput{
		DBInstanceIdentifier: aws.String(instanceID),
		SkipFinalSnapshot:    aws.Bool(true),
	}); err != nil {
		t.Fatalf("DeleteDBInstance: %v", err)
	}

	gone, _ := sql.Open("postgres", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deleted instance's database to fail")
	}
}
