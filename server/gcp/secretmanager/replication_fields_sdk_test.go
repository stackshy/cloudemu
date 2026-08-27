package secretmanager_test

import (
	"context"
	"errors"
	"hash/crc32"
	"testing"

	"google.golang.org/api/googleapi"
	sm "google.golang.org/api/secretmanager/v1"
)

// TestSDKUserManagedReplicationRoundTrip proves a create with user-managed
// replication is echoed back faithfully (not silently rewritten to automatic)
// on both the secret and its version's replicationStatus (audit: BUG1).
func TestSDKUserManagedReplicationRoundTrip(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()

	created, err := svc.Projects.Secrets.Create(testParent, &sm.Secret{
		Replication: &sm.Replication{UserManaged: &sm.UserManaged{Replicas: []*sm.Replica{
			{Location: "us-east1"},
			{Location: "us-west1", CustomerManagedEncryption: &sm.CustomerManagedEncryption{
				KmsKeyName: "projects/demo/locations/us-west1/keyRings/r/cryptoKeys/k",
			}},
		}}},
	}).SecretId("um-secret").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	assertUserManaged := func(where string, rep *sm.Replication) {
		if rep == nil || rep.UserManaged == nil {
			t.Fatalf("%s replication = %+v, want userManaged", where, rep)
		}

		if rep.Automatic != nil {
			t.Fatalf("%s replication rewritten to automatic: %+v", where, rep)
		}

		if len(rep.UserManaged.Replicas) != 2 {
			t.Fatalf("%s replicas = %d, want 2", where, len(rep.UserManaged.Replicas))
		}

		if rep.UserManaged.Replicas[0].Location != "us-east1" {
			t.Fatalf("%s replica[0] = %q, want us-east1", where, rep.UserManaged.Replicas[0].Location)
		}

		cmek := rep.UserManaged.Replicas[1].CustomerManagedEncryption
		if cmek == nil || cmek.KmsKeyName == "" {
			t.Fatalf("%s replica[1] CMEK not echoed: %+v", where, rep.UserManaged.Replicas[1])
		}
	}

	assertUserManaged("create", created.Replication)

	got, err := svc.Projects.Secrets.Get(created.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertUserManaged("get", got.Replication)

	// The version's replicationStatus must also mirror user-managed replication.
	v, err := svc.Projects.Secrets.AddVersion(created.Name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("payload")},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	if v.ReplicationStatus == nil || v.ReplicationStatus.UserManaged == nil {
		t.Fatalf("version replicationStatus = %+v, want userManaged", v.ReplicationStatus)
	}

	if v.ReplicationStatus.Automatic != nil {
		t.Fatalf("version replicationStatus rewritten to automatic: %+v", v.ReplicationStatus)
	}

	if len(v.ReplicationStatus.UserManaged.Replicas) != 2 {
		t.Fatalf("version replica statuses = %d, want 2", len(v.ReplicationStatus.UserManaged.Replicas))
	}
}

// TestSDKReplicationRequiredOnCreate proves a create without a replication
// policy is rejected with 400 INVALID_ARGUMENT (audit: BUG1).
func TestSDKReplicationRequiredOnCreate(t *testing.T) {
	svc := newSMService(t)

	_, err := svc.Projects.Secrets.Create(testParent, &sm.Secret{}).
		SecretId("no-rep").Context(context.Background()).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("Create without replication: got %v, want 400", err)
	}
}

// TestSDKAccessReturnsDataCrc32c proves accessSecretVersion returns the
// Castagnoli CRC32C of the payload so client-side verification passes
// (audit: BUG2).
func TestSDKAccessReturnsDataCrc32c(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()
	name := mustCreateSecret(t, svc, "crc-secret")

	data := []byte("verify-me")
	if _, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode(string(data))},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	got, err := svc.Projects.Secrets.Versions.Access(name + "/versions/latest").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Access: %v", err)
	}

	want := int64(crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli)))
	if got.Payload.DataCrc32c != want {
		t.Fatalf("dataCrc32c = %d, want %d", got.Payload.DataCrc32c, want)
	}
}

// TestSDKAddVersionCrc32cMismatchRejected proves a wrong client-supplied
// checksum is rejected with 400, and an empty payload is rejected (audit: BUG2).
func TestSDKAddVersionCrc32cMismatchRejected(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()
	name := mustCreateSecret(t, svc, "crc-reject")

	_, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("hello"), DataCrc32c: 12345},
	}).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("AddVersion(bad crc): got %v, want 400", err)
	}

	_, err = svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{},
	}).Context(ctx).Do()
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("AddVersion(empty): got %v, want 400", err)
	}

	// A correct checksum is accepted.
	good := int64(crc32.Checksum([]byte("hello"), crc32.MakeTable(crc32.Castagnoli)))
	if _, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("hello"), DataCrc32c: good},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("AddVersion(good crc): %v", err)
	}
}

// TestSDKSecretFieldsRoundTrip proves annotations, ttl->expireTime, rotation,
// topics and versionAliases round-trip on create (audit: field round-trip).
func TestSDKSecretFieldsRoundTrip(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()

	created, err := svc.Projects.Secrets.Create(testParent, &sm.Secret{
		Replication:    &sm.Replication{Automatic: &sm.Automatic{}},
		Annotations:    map[string]string{"team": "core"},
		Ttl:            "3600s",
		Rotation:       &sm.Rotation{RotationPeriod: "86400s", NextRotationTime: "2099-01-01T00:00:00Z"},
		Topics:         []*sm.Topic{{Name: "projects/demo/topics/rotate"}},
		VersionAliases: map[string]string{"prod": "1"},
	}).SecretId("fields").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Secrets.Get(created.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Annotations["team"] != "core" {
		t.Fatalf("annotations = %v", got.Annotations)
	}

	if got.ExpireTime == "" {
		t.Fatalf("expireTime not derived from ttl")
	}

	if got.Rotation == nil || got.Rotation.NextRotationTime != "2099-01-01T00:00:00Z" {
		t.Fatalf("rotation = %+v", got.Rotation)
	}

	if len(got.Topics) != 1 || got.Topics[0].Name != "projects/demo/topics/rotate" {
		t.Fatalf("topics = %+v", got.Topics)
	}

	if got.VersionAliases["prod"] != "1" {
		t.Fatalf("versionAliases = %v", got.VersionAliases)
	}
}

// TestSDKSecretFieldsPatch proves the same fields patch by update mask
// (audit: field round-trip on patch).
func TestSDKSecretFieldsPatch(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()
	name := mustCreateSecret(t, svc, "patch-fields")

	updated, err := svc.Projects.Secrets.Patch(name, &sm.Secret{
		Annotations:    map[string]string{"owner": "sre"},
		Topics:         []*sm.Topic{{Name: "projects/demo/topics/t"}},
		VersionAliases: map[string]string{"stable": "1"},
		Rotation:       &sm.Rotation{NextRotationTime: "2098-01-01T00:00:00Z", RotationPeriod: "7200s"},
	}).UpdateMask("annotations,topics,versionAliases,rotation").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if updated.Annotations["owner"] != "sre" {
		t.Fatalf("annotations = %v", updated.Annotations)
	}

	got, err := svc.Projects.Secrets.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Annotations["owner"] != "sre" || len(got.Topics) != 1 ||
		got.VersionAliases["stable"] != "1" || got.Rotation == nil {
		t.Fatalf("patched fields not persisted: %+v", got)
	}
}

// TestSDKAccessByVersionAlias proves GetSecretVersion and AccessSecretVersion
// resolve a version by its alias (audit: versionAliases).
func TestSDKAccessByVersionAlias(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()

	created, err := svc.Projects.Secrets.Create(testParent, &sm.Secret{
		Replication:    &sm.Replication{Automatic: &sm.Automatic{}},
		VersionAliases: map[string]string{"prod": "1"},
	}).SecretId("alias-secret").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Secrets.AddVersion(created.Name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("v1-data")},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	accessed, err := svc.Projects.Secrets.Versions.Access(created.Name + "/versions/prod").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Access(by alias): %v", err)
	}

	if accessed.Payload.Data != encode("v1-data") {
		t.Fatalf("alias access data = %q, want %q", accessed.Payload.Data, encode("v1-data"))
	}

	meta, err := svc.Projects.Secrets.Versions.Get(created.Name + "/versions/prod").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get(by alias): %v", err)
	}

	if meta.Name != created.Name+"/versions/1" {
		t.Fatalf("alias resolved to %q, want .../versions/1", meta.Name)
	}
}

// TestSDKListFilters proves secrets.list and versions.list honor ?filter
// (audit: BUG3).
func TestSDKListFilters(t *testing.T) {
	svc := newSMService(t)
	ctx := context.Background()

	for _, s := range []struct {
		id  string
		env string
	}{{"f-a", "prod"}, {"f-b", "dev"}, {"f-c", "prod"}} {
		if _, err := svc.Projects.Secrets.Create(testParent, &sm.Secret{
			Replication: &sm.Replication{Automatic: &sm.Automatic{}},
			Labels:      map[string]string{"env": s.env},
		}).SecretId(s.id).Context(ctx).Do(); err != nil {
			t.Fatalf("Create(%s): %v", s.id, err)
		}
	}

	list, err := svc.Projects.Secrets.List(testParent).Filter("labels.env=prod").Context(ctx).Do()
	if err != nil {
		t.Fatalf("List(filter): %v", err)
	}

	if len(list.Secrets) != 2 {
		t.Fatalf("filtered secrets = %d, want 2", len(list.Secrets))
	}

	// versions.list with a state filter.
	name := testParent + "/secrets/f-a"
	if _, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("v1")},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	v2, err := svc.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
		Payload: &sm.SecretPayload{Data: encode("v2")},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	if _, err := svc.Projects.Secrets.Versions.Disable(v2.Name, &sm.DisableSecretVersionRequest{}).
		Context(ctx).Do(); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	enabled, err := svc.Projects.Secrets.Versions.List(name).Filter("state:ENABLED").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Versions.List(filter): %v", err)
	}

	if len(enabled.Versions) != 1 {
		t.Fatalf("enabled versions = %d, want 1", len(enabled.Versions))
	}
}
