package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
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
)

// TestBatteriesServerE2E starts the batteries-included server in-process with a
// real Postgres and Redis engine wired in, drives it with the real AWS SDK
// (RDS + ElastiCache — the two services that are engine-backed), connects real
// lib/pq and go-redis clients to the endpoints the SDK reports, does a real
// round-trip against each, then shuts down and asserts Provider.Close() freed
// the embedded-Postgres port.
//
// It builds its own Postgres engine on a picked free port so the release
// assertion is deterministic (rather than probing the shared default 5432).
func TestBatteriesServerE2E(t *testing.T) {
	pgPort := freePort(t)

	cfg := appConfig{
		engines:           engineSelection{db: dbPostgres, cache: cacheRedis},
		host:              "127.0.0.1",
		awsPort:           "0",
		azurePort:         "0",
		gcpPort:           "0",
		accountID:         "000000000000",
		azureSubscription: "00000000-0000-0000-0000-000000000000",
		region:            "us-east-1",
		projectID:         "cloudemu-local",
		shutdownTimeout:   10 * time.Second,
	}

	// Build the engines directly so the Postgres server binds a known free port,
	// making the post-shutdown release assertion deterministic.
	opts := []config.Option{
		config.WithAccountID(cfg.accountID),
		config.WithRegion(cfg.region),
		config.WithProjectID(cfg.projectID),
		config.WithDatabaseEngine(realpostgres.New(pgPort)),
		config.WithCacheEngine(realredis.New()),
	}

	a, err := newAppFromOptions(&cfg, opts)
	if err != nil {
		t.Fatalf("newAppFromOptions: %v", err)
	}

	a.serve()

	awsURL := awsEndpoint(t, a)
	client := rdsClient(t, awsURL)
	cacheClient := elasticacheClient(t, awsURL)
	ctx := context.Background()

	dsn := createAndDescribeDB(ctx, t, client)
	redisAddr := createAndDescribeCache(ctx, t, cacheClient)

	sqlRoundTrip(ctx, t, dsn)
	redisRoundTrip(ctx, t, redisAddr)

	// Shut down: http servers stop and every Provider.Close() cascades to the
	// wired engines, stopping embedded Postgres and miniredis.
	shutdownCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := a.shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	assertPortReleased(t, pgPort)
}

// awsEndpoint returns the bound AWS listener URL.
func awsEndpoint(t *testing.T, a *app) string {
	t.Helper()

	for _, s := range a.servers {
		if s.name == "aws" {
			return s.url(a.displayHost)
		}
	}

	t.Fatal("no aws endpoint bound")

	return ""
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

	cfg := awsSDKConfig(t)

	return rds.NewFromConfig(cfg, func(o *rds.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

// elasticacheClient builds an ElastiCache SDK client pointed at the emulator.
func elasticacheClient(t *testing.T, endpoint string) *elasticache.Client {
	t.Helper()

	cfg := awsSDKConfig(t)

	return elasticache.NewFromConfig(cfg, func(o *elasticache.Options) { o.BaseEndpoint = aws.String(endpoint) })
}

// awsSDKConfig returns a static-credential AWS config for the emulator.
func awsSDKConfig(t *testing.T) aws.Config {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
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
