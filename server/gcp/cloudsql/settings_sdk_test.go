package cloudsql_test

import (
	"context"
	"testing"

	sqladmin "google.golang.org/api/sqladmin/v1"
)

// TestSDKCloudSQLSettingsRoundTrip reproduces the Terraform perpetual-drift bug:
// an Insert carrying availabilityType + databaseFlags + backupConfiguration +
// ipConfiguration must return all four unchanged on the following Get.
func TestSDKCloudSQLSettingsRoundTrip(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "settings",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings: &sqladmin.Settings{
			Tier:             "db-custom-2-8192",
			AvailabilityType: "REGIONAL",
			DatabaseFlags: []*sqladmin.DatabaseFlags{
				{Name: "max_connections", Value: "100"},
			},
			BackupConfiguration: &sqladmin.BackupConfiguration{
				Enabled:                    true,
				StartTime:                  "03:00",
				PointInTimeRecoveryEnabled: true,
			},
			IpConfiguration: &sqladmin.IpConfiguration{
				Ipv4Enabled:    true,
				RequireSsl:     true,
				PrivateNetwork: "projects/p/global/networks/default",
			},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Instances.Insert: %v", err)
	}

	got, err := svc.Instances.Get(project, "settings").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}

	if got.Settings == nil {
		t.Fatal("expected settings on Get")
	}

	if got.Settings.AvailabilityType != "REGIONAL" {
		t.Fatalf("availabilityType = %q, want REGIONAL", got.Settings.AvailabilityType)
	}

	if len(got.Settings.DatabaseFlags) != 1 ||
		got.Settings.DatabaseFlags[0].Name != "max_connections" ||
		got.Settings.DatabaseFlags[0].Value != "100" {
		t.Fatalf("databaseFlags = %+v, want max_connections=100", got.Settings.DatabaseFlags)
	}

	if got.Settings.BackupConfiguration == nil || !got.Settings.BackupConfiguration.Enabled ||
		got.Settings.BackupConfiguration.StartTime != "03:00" ||
		!got.Settings.BackupConfiguration.PointInTimeRecoveryEnabled {
		t.Fatalf("backupConfiguration = %+v, want enabled/03:00/PITR", got.Settings.BackupConfiguration)
	}

	if got.Settings.IpConfiguration == nil || !got.Settings.IpConfiguration.Ipv4Enabled ||
		!got.Settings.IpConfiguration.RequireSsl ||
		got.Settings.IpConfiguration.PrivateNetwork != "projects/p/global/networks/default" {
		t.Fatalf("ipConfiguration = %+v, want ipv4/requireSsl/privateNetwork", got.Settings.IpConfiguration)
	}
}

// TestSDKCloudSQLPatchSettings verifies that a Patch of availabilityType and
// databaseFlags is reflected on the following Get (default is ZONAL).
func TestSDKCloudSQLPatchSettings(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "patchset",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	base, err := svc.Instances.Get(project, "patchset").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get (base): %v", err)
	}

	if base.Settings.AvailabilityType != "ZONAL" {
		t.Fatalf("default availabilityType = %q, want ZONAL", base.Settings.AvailabilityType)
	}

	if _, err := svc.Instances.Patch(project, "patchset", &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{
			AvailabilityType: "REGIONAL",
			DatabaseFlags:    []*sqladmin.DatabaseFlags{{Name: "log_min_duration_statement", Value: "500"}},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Instances.Get(project, "patchset").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get (after patch): %v", err)
	}

	if got.Settings.AvailabilityType != "REGIONAL" {
		t.Fatalf("after patch availabilityType = %q, want REGIONAL", got.Settings.AvailabilityType)
	}

	if len(got.Settings.DatabaseFlags) != 1 || got.Settings.DatabaseFlags[0].Value != "500" {
		t.Fatalf("after patch databaseFlags = %+v, want log_min_duration_statement=500", got.Settings.DatabaseFlags)
	}
}

// TestSDKCloudSQLPatchDatabaseVersion verifies a patched databaseVersion is
// visible on Get (previously dropped: patch wrote EngineVersion, Get read Engine).
func TestSDKCloudSQLPatchDatabaseVersion(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "verpatch",
		DatabaseVersion: "POSTGRES_14",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if _, err := svc.Instances.Patch(project, "verpatch", &sqladmin.DatabaseInstance{
		DatabaseVersion: "POSTGRES_15",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Instances.Get(project, "verpatch").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.DatabaseVersion != "POSTGRES_15" {
		t.Fatalf("databaseVersion = %q, want POSTGRES_15", got.DatabaseVersion)
	}
}

// TestSDKCloudSQLDatabaseUpdate verifies databases.update applies charset and
// collation (previously 405 Method Not Allowed).
func TestSDKCloudSQLDatabaseUpdate(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "dbupd",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert instance: %v", err)
	}

	if _, err := svc.Databases.Insert(project, "dbupd", &sqladmin.Database{
		Name:      "app",
		Charset:   "UTF8",
		Collation: "en_US.UTF8",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Databases.Insert: %v", err)
	}

	if _, err := svc.Databases.Update(project, "dbupd", "app", &sqladmin.Database{
		Name:      "app",
		Charset:   "LATIN1",
		Collation: "en_US.ISO8859-1",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Databases.Update: %v", err)
	}

	got, err := svc.Databases.Get(project, "dbupd", "app").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Databases.Get: %v", err)
	}

	if got.Charset != "LATIN1" || got.Collation != "en_US.ISO8859-1" {
		t.Fatalf("database after update = charset %q collation %q, want LATIN1 / en_US.ISO8859-1",
			got.Charset, got.Collation)
	}
}

// TestSDKCloudSQLServerCaCert verifies serverCaCert is populated on Get so
// Terraform's computed server_ca_cert attribute is never empty.
func TestSDKCloudSQLServerCaCert(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	if _, err := svc.Instances.Insert(project, &sqladmin.DatabaseInstance{
		Name:            "cacert",
		DatabaseVersion: "POSTGRES_15",
		Region:          "us-central1",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-8192"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := svc.Instances.Get(project, "cacert").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ServerCaCert == nil || got.ServerCaCert.Cert == "" || got.ServerCaCert.Sha1Fingerprint == "" {
		t.Fatalf("serverCaCert = %+v, want populated cert + fingerprint", got.ServerCaCert)
	}
}
