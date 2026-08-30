package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"

	"github.com/stackshy/cloudemu/v2/config"
	realpostgres "github.com/stackshy/cloudemu/v2/contrib/realengine/postgres"
	realredis "github.com/stackshy/cloudemu/v2/contrib/realengine/redis"
	"github.com/stackshy/cloudemu/v2/server/serveflags"
)

// TestBatteriesServerE2E starts the batteries-included server in-process — now
// assembled through the shared server/serverkit package — with a real Postgres
// and Redis engine wired in, drives it with the real AWS SDK (RDS + ElastiCache,
// the two engine-backed services), connects real lib/pq and go-redis clients to
// the endpoints the SDK reports, does a real round-trip against each, then cancels
// the serve context and asserts serverkit's provider Close() freed the
// embedded-Postgres port.
//
// It builds its own Postgres engine on a picked free port so the release
// assertion is deterministic (rather than probing the shared default 5432).
func TestBatteriesServerE2E(t *testing.T) {
	pgPort := freePort(t)

	cfg := testConfig(t, engineSelection{db: dbPostgres, cache: cacheRedis})

	// Build the engines directly so the Postgres server binds a known free port,
	// making the post-shutdown release assertion deterministic.
	opts := []config.Option{
		config.WithAccountID(cfg.AccountID),
		config.WithRegion(cfg.Region),
		config.WithProjectID(cfg.ProjectID),
		config.WithDatabaseEngine(realpostgres.New(pgPort)),
		config.WithCacheEngine(realredis.New()),
	}

	awsURL, stop := startAWS(t, cfg, opts)

	client := rdsClient(t, awsURL)
	cacheClient := elasticacheClient(t, awsURL)
	ctx := context.Background()

	dsn := createAndDescribeDB(ctx, t, client)
	redisAddr := createAndDescribeCache(ctx, t, cacheClient)

	sqlRoundTrip(ctx, t, dsn)
	redisRoundTrip(ctx, t, redisAddr)

	// Cancel the serve context: serverkit stops the http servers and closes every
	// provider, cascading engine teardown (embedded Postgres, miniredis).
	stop()

	assertPortReleased(t, pgPort)
}

// TestBatteriesServerIdentityPreserved guards the swap from
// awsserver.NewFromProvider to serverkit (which builds via awsserver.DriversFrom):
// DriversFrom copies AccountID/Region/EnforceAuth verbatim, so a request routed
// through serverkit must observe the same identity, and — with enforce-auth off,
// the batteries default — accept arbitrary credentials exactly as before.
//
// It runs with all engines off, so it needs no Docker/Postgres and stays fast and
// deterministic. The RDS DBInstanceArn embeds the account id from the provider
// config (proving the DriversFrom copy) and the request region (proving it round
// trips); the whole flow succeeds under dummy static credentials, proving auth is
// not enforced.
func TestBatteriesServerIdentityPreserved(t *testing.T) {
	const (
		wantAccount = "210987654321"
		wantRegion  = "eu-west-2"
	)

	cfg := testConfig(t, allEnginesOff())
	cfg.AccountID = wantAccount
	cfg.Region = wantRegion

	opts, _, err := buildOptions(&cfg, dockerAvailable)
	if err != nil {
		t.Fatalf("buildOptions: %v", err)
	}

	awsURL, stop := startAWS(t, cfg, opts)
	defer stop()

	client := rdsClientRegion(t, awsURL, wantRegion)
	ctx := context.Background()

	if _, err := client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String("identity-db"),
		Engine:               aws.String("postgres"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		MasterUsername:       aws.String("u"),
		MasterUserPassword:   aws.String("password-123"),
	}); err != nil {
		t.Fatalf("CreateDBInstance (with dummy creds — enforce-auth must be off): %v", err)
	}

	desc, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String("identity-db"),
	})
	if err != nil {
		t.Fatalf("DescribeDBInstances: %v", err)
	}

	if len(desc.DBInstances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(desc.DBInstances))
	}

	arn := aws.ToString(desc.DBInstances[0].DBInstanceArn)

	// arn:aws:rds:<region>:<account>:db:<id>
	const (
		regionField  = 3
		accountField = 4
		minFields    = 7
	)

	parts := strings.Split(arn, ":")
	if len(parts) < minFields {
		t.Fatalf("unexpected ARN shape: %q", arn)
	}

	if got := parts[accountField]; got != wantAccount {
		t.Fatalf("account id in ARN = %q, want %q (DriversFrom must copy AccountID)", got, wantAccount)
	}

	if got := parts[regionField]; got != wantRegion {
		t.Fatalf("region in ARN = %q, want %q", got, wantRegion)
	}
}

// startAWS boots the batteries server through serverkit on cfg's ports, waits for
// the AWS listener to answer, and returns the AWS endpoint URL plus a stop func
// that cancels the serve context and waits for a clean shutdown (which closes the
// providers and tears down their engines).
func startAWS(t *testing.T, cfg appConfig, opts []config.Option) (string, func()) {
	t.Helper()

	a, err := newAppFromOptions(&cfg, opts)
	if err != nil {
		t.Fatalf("newAppFromOptions: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- a.Serve(ctx) }()

	awsURL := "http://" + net.JoinHostPort(cfg.Host, cfg.AWSPort)
	waitListening(t, cfg.Host, cfg.AWSPort)

	var stopped bool

	stop := func() {
		if stopped {
			return
		}

		stopped = true

		cancel()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned error: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("Serve did not return within 30s of cancel")
		}
	}

	return awsURL, stop
}

// testConfig builds an appConfig bound to freshly-picked free ports with a
// discarded banner sink, so tests never collide on the default 4566/4568/4569.
func testConfig(t *testing.T, engines engineSelection) appConfig {
	t.Helper()

	return appConfig{
		CommonConfig: serveflags.CommonConfig{
			Host:              "127.0.0.1",
			AWSPort:           freePortStr(t),
			AzurePort:         freePortStr(t),
			GCPPort:           freePortStr(t),
			AccountID:         "000000000000",
			AzureSubscription: "00000000-0000-0000-0000-000000000000",
			Region:            "us-east-1",
			ProjectID:         "cloudemu-local",
			ShutdownTimeout:   10 * time.Second,
		},
		engines:   engines,
		providers: []string{"aws", "azure", "gcp"},
		out:       io.Discard,
	}
}

// waitListening blocks until host:port accepts a TCP connection or the deadline
// passes, so a test drives the server only once serverkit has bound it.
func waitListening(t *testing.T, host, port string) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()

			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("server did not start listening on %s:%s", host, port)
}

// createAndDescribeDB creates an RDS Postgres instance and returns the lib/pq DSN
// for the endpoint the SDK reports.
func createAndDescribeDB(ctx context.Context, t *testing.T, client *rds.Client) string {
	t.Helper()

	const (
		instanceID = "app-db"
		dbName     = "appdb"
		user       = "appuser"
		password   = "app-secret-pw"
	)

	if _, err := client.CreateDBInstance(ctx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String(instanceID),
		Engine:               aws.String("postgres"),
		DBInstanceClass:      aws.String("db.t3.micro"),
		AllocatedStorage:     aws.Int32(20),
		MasterUsername:       aws.String(user),
		MasterUserPassword:   aws.String(password),
		DBName:               aws.String(dbName),
	}); err != nil {
		t.Fatalf("CreateDBInstance: %v", err)
	}

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

	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		aws.ToString(ep.Address), aws.ToInt32(ep.Port), user, password, dbName)
}

// createAndDescribeCache creates an ElastiCache Redis cluster and returns the
// node address the SDK reports.
func createAndDescribeCache(ctx context.Context, t *testing.T, client *elasticache.Client) string {
	t.Helper()

	const clusterID = "app-cache"

	if _, err := client.CreateCacheCluster(ctx, &elasticache.CreateCacheClusterInput{
		CacheClusterId: aws.String(clusterID),
		Engine:         aws.String("redis"),
		CacheNodeType:  aws.String("cache.t3.micro"),
		NumCacheNodes:  aws.Int32(1),
	}); err != nil {
		t.Fatalf("CreateCacheCluster: %v", err)
	}

	desc, err := client.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
		CacheClusterId:    aws.String(clusterID),
		ShowCacheNodeInfo: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("DescribeCacheClusters: %v", err)
	}

	if len(desc.CacheClusters) != 1 || len(desc.CacheClusters[0].CacheNodes) != 1 {
		t.Fatalf("expected 1 cluster with 1 node, got %+v", desc.CacheClusters)
	}

	ep := desc.CacheClusters[0].CacheNodes[0].Endpoint
	if ep == nil {
		t.Fatal("no node endpoint reported")
	}

	return fmt.Sprintf("%s:%d", aws.ToString(ep.Address), aws.ToInt32(ep.Port))
}

// sqlRoundTrip proves the RDS endpoint is a real Postgres via a DDL/DML/select.
func sqlRoundTrip(ctx context.Context, t *testing.T, dsn string) {
	t.Helper()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "CREATE TABLE orders (id serial primary key, item text, qty int)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if _, err := db.ExecContext(ctx, "INSERT INTO orders (item, qty) VALUES ($1, $2)", "widget", 7); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var (
		item string
		qty  int
	)

	if err := db.QueryRowContext(ctx, "SELECT item, qty FROM orders WHERE id = 1").Scan(&item, &qty); err != nil {
		t.Fatalf("select: %v", err)
	}

	if item != "widget" || qty != 7 {
		t.Fatalf("sql round-trip mismatch: got %q, %d", item, qty)
	}
}

// redisRoundTrip proves the ElastiCache endpoint is a real Redis via SET/GET.
func redisRoundTrip(ctx context.Context, t *testing.T, addr string) {
	t.Helper()

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	if err := rdb.Set(ctx, "session:42", "active", 0).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}

	got, err := rdb.Get(ctx, "session:42").Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}

	if got != "active" {
		t.Fatalf("redis round-trip mismatch: got %q", got)
	}
}

// assertPortReleased fails if anything is still listening on port after
// shutdown — proving Provider.Close() stopped the embedded Postgres server.
func assertPortReleased(t *testing.T, port int) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
		if err != nil {
			return
		}

		_ = conn.Close()
		time.Sleep(200 * time.Millisecond)
	}

	t.Fatalf("embedded Postgres port %d still accepting connections after Close()", port)
}

// rdsClient builds an RDS SDK client pointed at the emulator endpoint.
func rdsClient(t *testing.T, endpoint string) *rds.Client {
	t.Helper()

	return rdsClientRegion(t, endpoint, "us-east-1")
}

// rdsClientRegion builds an RDS SDK client for a specific region, so a test can
// assert the region the emulator reports back.
func rdsClientRegion(t *testing.T, endpoint, region string) *rds.Client {
	t.Helper()

	cfg := awsSDKConfig(t, region)

	return rds.NewFromConfig(cfg, func(o *rds.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

// elasticacheClient builds an ElastiCache SDK client pointed at the emulator.
func elasticacheClient(t *testing.T, endpoint string) *elasticache.Client {
	t.Helper()

	cfg := awsSDKConfig(t, "us-east-1")

	return elasticache.NewFromConfig(cfg, func(o *elasticache.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

// awsSDKConfig returns a static-credential AWS config for the emulator. The
// credentials are deliberately arbitrary: the batteries server does not enforce
// auth, and a successful call is itself the proof.
func awsSDKConfig(t *testing.T, region string) aws.Config {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return cfg
}

// freePort reserves and immediately releases an ephemeral TCP port, returning
// the number for a component that binds a fixed port.
func freePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}

	port := ln.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // always a TCP listener
	_ = ln.Close()

	return port
}

// allEnginesOff is the fully in-memory selection (every capability "off"), the
// engine equivalent of a no-flag run — what buildOptions validates against.
func allEnginesOff() engineSelection {
	return engineSelection{
		db:         engineOff,
		cache:      engineOff,
		functions:  engineOff,
		compute:    engineOff,
		containers: engineOff,
		storage:    engineOff,
	}
}

// freePortStr is freePort as the string serverkit.Config.Ports wants.
func freePortStr(t *testing.T) string {
	t.Helper()

	return strconv.Itoa(freePort(t))
}
