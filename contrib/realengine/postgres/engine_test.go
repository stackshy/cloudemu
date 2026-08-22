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
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	_ "github.com/lib/pq"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/realengine/postgres"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestPostgresProvisionRoundTrip provisions a database through the engine,
// connects to it with the instance's master credentials, runs real SQL, then
// deprovisions and confirms the database is gone — the engine's own contract.
func TestPostgresProvisionRoundTrip(t *testing.T) {
	eng := postgres.New(55440)
	t.Cleanup(func() { _ = eng.Close() })

	ctx := context.Background()

	res, err := eng.Provision(ctx, config.ProvisionRequest{
		InstanceID: "db1", Engine: "postgres", DBName: "app", Username: "admin", Password: "secret",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}

	dsn := fmt.Sprintf("host=%s port=%d user=admin password=secret dbname=app sslmode=disable", res.Host, res.Port)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if _, err := db.Exec("CREATE TABLE widgets (id int primary key, name text)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := db.Exec("INSERT INTO widgets VALUES (1, 'cloudemu')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var name string
	if err := db.QueryRow("SELECT name FROM widgets WHERE id = 1").Scan(&name); err != nil {
		t.Fatalf("select: %v", err)
	}

	if name != "cloudemu" {
		t.Fatalf("round-trip mismatch: got %q", name)
	}

	_ = db.Close()

	if err := eng.Deprovision(ctx, "db1"); err != nil {
		t.Fatalf("Deprovision: %v", err)
	}

	// The database is gone: a fresh connection to it must fail.
	gone, _ := sql.Open("postgres", dsn)
	defer gone.Close()

	if err := gone.Ping(); err == nil {
		t.Fatal("expected connection to the deprovisioned database to fail")
	}
}

// TestGCPCloudSQLCloneIsIsolated proves a Cloud SQL clone is backed by its OWN
// physical database, not the source's: the clone starts with an empty schema,
// writes to it never reach the source, and deleting the clone does NOT drop the
// source's database. Before the fix the clone aliased the source's database.
func TestGCPCloudSQLCloneIsIsolated(t *testing.T) {
	// Cloud SQL clients always connect on 5432 (the SDK surfaces no port), so the
	// engine must listen there.
	eng := postgres.New(0)
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewGCP(config.WithDatabaseEngine(eng))
	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{CloudSQL: cloud.CloudSQL}))
	t.Cleanup(ts.Close)

	ctx := context.Background()

	svc, err := sqladmin.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("sqladmin.NewService: %v", err)
	}

	const (
		project = "my-project"
		source  = "src-db"
		clone   = "clone-db"
		user    = "postgres"
		pass    = "R00t-Passw0rd"
	)

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name: source, DatabaseVersion: "POSTGRES_15", Region: "us-central1", RootPassword: pass,
		Settings: &sqladmin.Settings{Tier: "db-custom-1-3840", DataDiskSizeGb: 10},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Insert: %v", err)
	}

	srcHost := instancePrimaryIP(ctx, t, svc, project, source)
	srcDB := openPG(t, srcHost, user, pass, source)

	if _, err := srcDB.Exec("CREATE TABLE only_in_source (id int)"); err != nil {
		t.Fatalf("create source table: %v", err)
	}

	if _, err := srcDB.Exec("INSERT INTO only_in_source VALUES (1)"); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	_ = srcDB.Close()

	// Clone — like `gcloud sql instances clone`.
	if _, err := svc.Instances.Clone(project, source, &sqladmin.InstancesCloneRequest{
		CloneContext: &sqladmin.CloneContext{DestinationInstanceName: clone},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Clone: %v", err)
	}

	cloneHost := instancePrimaryIP(ctx, t, svc, project, clone)
	cloneDB := openPG(t, cloneHost, user, pass, clone)

	// The clone's database is independent: the source's table is absent.
	var present bool
	if err := cloneDB.QueryRow(
		"SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'only_in_source')",
	).Scan(&present); err != nil {
		t.Fatalf("inspect clone schema: %v", err)
	}

	if present {
		t.Fatal("clone shares the source's database — it must be schema-isolated")
	}

	// A write to the clone must not appear in the source.
	if _, err := cloneDB.Exec("CREATE TABLE only_in_clone (id int)"); err != nil {
		t.Fatalf("write to clone: %v", err)
	}

	_ = cloneDB.Close()

	// Delete the clone — this drops the clone's database only.
	if _, err := svc.Instances.Delete(project, clone).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Delete clone: %v", err)
	}

	// The source's database and its data survive the clone deletion.
	srcAgain := openPG(t, srcHost, user, pass, source)
	defer srcAgain.Close()

	var count int
	if err := srcAgain.QueryRow("SELECT count(*) FROM only_in_source").Scan(&count); err != nil {
		t.Fatalf("source database was harmed by the clone lifecycle: %v", err)
	}

	if count != 1 {
		t.Fatalf("source row count = %d, want 1", count)
	}
}

// TestRDSRestoreFromSnapshotIsReachable proves a restore-from-snapshot backs the
// restored instance with a real database: the reported endpoint accepts a real
// connection with the inherited master credentials. Before the fix the restored
// endpoint resolved to nothing.
func TestRDSRestoreFromSnapshotIsReachable(t *testing.T) {
	eng := postgres.New(55462)
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewAWS(config.WithDatabaseEngine(eng))
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	client := newRDSClient(t, ts)
	ctx := context.Background()

	const (
		source   = "src-db"
		restored = "restored-db"
		user     = "appuser"
		password = "app-secret-pw"
	)

	if _, err := client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String(source),
		Engine:               aws.String("postgres"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		MasterUsername:       aws.String(user),
		MasterUserPassword:   aws.String(password),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	if _, err := client.CreateDBSnapshot(ctx, &rds.CreateDBSnapshotInput{
		DBInstanceIdentifier: aws.String(source),
		DBSnapshotIdentifier: aws.String("snap-1"),
	}); err != nil {
		t.Fatalf("CreateDBSnapshot: %v", err)
	}

	if _, err := client.RestoreDBInstanceFromDBSnapshot(ctx, &rds.RestoreDBInstanceFromDBSnapshotInput{
		DBInstanceIdentifier: aws.String(restored),
		DBSnapshotIdentifier: aws.String("snap-1"),
	}); err != nil {
		t.Fatalf("RestoreDBInstanceFromDBSnapshot: %v", err)
	}

	ep := endpointOf(ctx, t, client, restored)

	// Connect to the RESTORED endpoint with the inherited master credentials. The
	// restored database defaults to the new instance id.
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		aws.ToString(ep.Address), aws.ToInt32(ep.Port), user, password, restored)

	db := openDSN(t, dsn)
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("restored instance endpoint is not reachable: %v", err)
	}

	if _, err := db.Exec("CREATE TABLE t (id int)"); err != nil {
		t.Fatalf("restored database is not writable: %v", err)
	}
}

// TestRDSModifyRotatesPassword proves ModifyDBInstance with a new
// MasterUserPassword actually rotates the engine credential: the new password
// authenticates and the old one no longer does. Before the fix the password
// change was silently dropped.
func TestRDSModifyRotatesPassword(t *testing.T) {
	eng := postgres.New(55463)
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewAWS(config.WithDatabaseEngine(eng))
	ts := httptest.NewServer(awsserver.New(awsserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	client := newRDSClient(t, ts)
	ctx := context.Background()

	const (
		id     = "rotate-db"
		user   = "appuser"
		oldPw  = "old-secret-pw"
		newPw  = "new-secret-pw"
		dbName = "appdb"
	)

	if _, err := client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String(id),
		Engine:               aws.String("postgres"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		MasterUsername:       aws.String(user),
		MasterUserPassword:   aws.String(oldPw),
		DBName:               aws.String(dbName),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

	ep := endpointOf(ctx, t, client, id)
	host, port := aws.ToString(ep.Address), aws.ToInt32(ep.Port)

	if _, err := client.ModifyDBInstance(ctx, &rds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String(id),
		MasterUserPassword:   aws.String(newPw),
		ApplyImmediately:     aws.Bool(true),
	}); err != nil {
		t.Fatalf("ModifyDBInstance: %v", err)
	}

	fresh := openDSN(t, fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, newPw, dbName))
	defer fresh.Close()

	if err := fresh.Ping(); err != nil {
		t.Fatalf("rotated (new) password must authenticate: %v", err)
	}

	stale := openDSN(t, fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, oldPw, dbName))
	defer stale.Close()

	if err := stale.Ping(); err == nil {
		t.Fatal("superseded (old) password must no longer authenticate")
	}
}

func newRDSClient(t *testing.T, ts *httptest.Server) *rds.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return rds.NewFromConfig(cfg, func(o *rds.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func endpointOf(ctx context.Context, t *testing.T, client *rds.Client, id string) *rdstypes.Endpoint {
	t.Helper()

	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{DBInstanceIdentifier: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if len(desc.DBInstances) != 1 || desc.DBInstances[0].Endpoint == nil {
		t.Fatalf("no endpoint reported for %q: %+v", id, desc.DBInstances)
	}

	return desc.DBInstances[0].Endpoint
}

func instancePrimaryIP(ctx context.Context, t *testing.T, svc *sqladmin.Service, project, id string) string {
	t.Helper()

	got, err := svc.Instances.Get(project, id).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get %q: %v", id, err)
	}

	for _, ip := range got.IpAddresses {
		if ip.Type == "PRIMARY" {
			return ip.IpAddress
		}
	}

	t.Fatalf("no PRIMARY ipAddress reported for %q: %+v", id, got.IpAddresses)

	return ""
}

func openPG(t *testing.T, host, user, pass, dbName string) *sql.DB {
	t.Helper()

	return openDSN(t, fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=%s sslmode=disable",
		host, user, pass, dbName))
}

func openDSN(t *testing.T, dsn string) *sql.DB {
	t.Helper()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	db.SetConnMaxLifetime(time.Minute)

	return db
}
