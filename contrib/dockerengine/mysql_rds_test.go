package dockerengine_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	_ "github.com/go-sql-driver/mysql"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// dockerUp reports whether the docker CLI is present AND its daemon answers, so
// the real-MySQL e2es skip cleanly on a host without a running daemon.
func dockerUp() bool {
	if !dockerengine.Available() {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return exec.CommandContext(ctx, "docker", "info").Run() == nil
}

// TestRDSMySQLE2E runs the exact flow a real user runs against AWS: create an RDS
// MySQL instance with the AWS SDK, read its endpoint, connect with a real MySQL
// client using the master credentials, run SQL, then delete — all against
// CloudEmu backed by a real MySQL container (no cloud account).
func TestRDSMySQLE2E(t *testing.T) {
	if !dockerUp() {
		t.Skip("docker daemon not available")
	}

	// RDS surfaces the port explicitly, so this engine can bind a non-default host
	// port and the client learns it from the SDK response.
	eng := dockerengine.NewMySQL(3308)
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
		instanceID = "app-db"
		dbName     = "appdb"
		user       = "appuser"
		password   = "app-secret-pw"
	)

	// 1. Create the instance — exactly like `aws rds create-db-instance`.
	if _, err = client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String(instanceID),
		Engine:               aws.String("mysql"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		MasterUsername:       aws.String(user),
		MasterUserPassword:   aws.String(password),
		DBName:               aws.String(dbName),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	// 2. Read the endpoint the SDK reports — the real MySQL container address.
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

	// Connect using ONLY the SDK-reported endpoint + port — no out-of-band knowledge.
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		user, password, aws.ToString(ep.Address), aws.ToInt32(ep.Port), dbName)

	// 3. Connect with a real MySQL client and run real SQL.
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	db.SetConnMaxLifetime(time.Minute)

	if _, err := db.Exec("CREATE TABLE orders (id int primary key auto_increment, item varchar(64), qty int)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := db.Exec("INSERT INTO orders (item, qty) VALUES (?, ?)", "widget", 7); err != nil {
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

	gone, _ := sql.Open("mysql", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deleted instance's database to fail")
	}
}
