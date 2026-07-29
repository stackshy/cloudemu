package cloudsql_test

import (
	"context"
	"testing"

	sqladmin "google.golang.org/api/sqladmin/v1"
)

func mustCreateInstance(t *testing.T, svc *sqladmin.Service, project, name string) {
	t.Helper()

	_, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            name,
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192", DataDiskSizeGb: 50},
	}).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("Instances.Insert %q: %v", name, err)
	}
}

func TestSDKCloudSQLDatabases(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	mustCreateInstance(t, svc, project, "pg")

	if _, err := svc.Databases.Insert(project, "pg", &sqladmin.Database{
		Name: "appdb", Charset: "UTF8", Collation: "en_US.UTF8",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Databases.Insert: %v", err)
	}

	got, err := svc.Databases.Get(project, "pg", "appdb").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Databases.Get: %v", err)
	}

	if got.Charset != "UTF8" {
		t.Fatalf("charset: got %q, want UTF8", got.Charset)
	}

	list, err := svc.Databases.List(project, "pg").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Databases.List: %v", err)
	}

	if len(list.Items) != 1 {
		t.Fatalf("got %d databases, want 1", len(list.Items))
	}

	if _, err := svc.Databases.Delete(project, "pg", "appdb").Context(ctx).Do(); err != nil {
		t.Fatalf("Databases.Delete: %v", err)
	}

	if _, err := svc.Databases.Get(project, "pg", "appdb").Context(ctx).Do(); err == nil {
		t.Fatal("expected error after database delete")
	}
}

func TestSDKCloudSQLUsers(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	mustCreateInstance(t, svc, project, "pg")

	if _, err := svc.Users.Insert(project, "pg", &sqladmin.User{
		Name: "appuser", Host: "%",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Users.Insert: %v", err)
	}

	got, err := svc.Users.Get(project, "pg", "appuser").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Users.Get: %v", err)
	}

	if got.Name != "appuser" {
		t.Fatalf("name: got %q, want appuser", got.Name)
	}

	list, err := svc.Users.List(project, "pg").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Users.List: %v", err)
	}

	if len(list.Items) != 1 {
		t.Fatalf("got %d users, want 1", len(list.Items))
	}

	if _, err := svc.Users.Delete(project, "pg").Name("appuser").Context(ctx).Do(); err != nil {
		t.Fatalf("Users.Delete: %v", err)
	}

	if _, err := svc.Users.Get(project, "pg", "appuser").Context(ctx).Do(); err == nil {
		t.Fatal("expected error after user delete")
	}
}

func TestSDKCloudSQLSslCerts(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	mustCreateInstance(t, svc, project, "pg")

	resp, err := svc.SslCerts.Insert(project, "pg", &sqladmin.SslCertsInsertRequest{
		CommonName: "client-1",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SslCerts.Insert: %v", err)
	}

	if resp.ClientCert == nil || resp.ClientCert.CertInfo == nil || resp.ClientCert.CertInfo.Sha1Fingerprint == "" {
		t.Fatalf("expected client cert with fingerprint, got %+v", resp.ClientCert)
	}

	fp := resp.ClientCert.CertInfo.Sha1Fingerprint

	got, err := svc.SslCerts.Get(project, "pg", fp).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SslCerts.Get: %v", err)
	}

	if got.CommonName != "client-1" {
		t.Fatalf("commonName: got %q, want client-1", got.CommonName)
	}

	list, err := svc.SslCerts.List(project, "pg").Context(ctx).Do()
	if err != nil {
		t.Fatalf("SslCerts.List: %v", err)
	}

	if len(list.Items) != 1 {
		t.Fatalf("got %d certs, want 1", len(list.Items))
	}

	if _, err := svc.SslCerts.Delete(project, "pg", fp).Context(ctx).Do(); err != nil {
		t.Fatalf("SslCerts.Delete: %v", err)
	}
}

func TestSDKCloudSQLInstanceActions(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	mustCreateInstance(t, svc, project, "pg")

	// Clone.
	if _, err := svc.Instances.Clone(project, "pg", &sqladmin.InstancesCloneRequest{
		CloneContext: &sqladmin.CloneContext{DestinationInstanceName: "pg-clone"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Clone: %v", err)
	}

	if _, err := svc.Instances.Get(project, "pg-clone").Context(ctx).Do(); err != nil {
		t.Fatalf("Get clone: %v", err)
	}

	// Failover, stop/start replica, promote replica all succeed on a live instance.
	if _, err := svc.Instances.Failover(project, "pg", &sqladmin.InstancesFailoverRequest{}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Failover: %v", err)
	}

	if _, err := svc.Instances.StopReplica(project, "pg-clone").Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.StopReplica: %v", err)
	}

	if _, err := svc.Instances.StartReplica(project, "pg-clone").Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.StartReplica: %v", err)
	}

	if _, err := svc.Instances.PromoteReplica(project, "pg-clone").Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.PromoteReplica: %v", err)
	}
}
