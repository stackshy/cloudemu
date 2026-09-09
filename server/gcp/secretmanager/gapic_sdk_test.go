package secretmanager_test

import (
	"context"
	"hash/crc32"
	"net/http/httptest"
	"testing"

	gapic "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newGAPIC(t *testing.T) *gapic.Client {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{SecretManager: cloud.SecretManager})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	c, err := gapic.NewRESTClient(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	return c
}

// TestGAPICLifecycle drives a full secret lifecycle through the idiomatic
// cloud.google.com/go/secretmanager/apiv1 GAPIC REST client (protojson wire
// codec, oneof replication, int64-as-string crc32c) — the client most real Go
// users reach for, distinct from the google.golang.org/api discovery client the
// other tests use. cloudemu serves REST only, so NewRESTClient is used; the
// default NewClient (gRPC) cannot reach it.
func TestGAPICLifecycle(t *testing.T) {
	c := newGAPIC(t)
	ctx := context.Background()

	// CREATE with automatic replication (enum-ish oneof).
	created, err := c.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   "projects/demo",
		SecretId: "gapic-probe",
		Secret: &secretmanagerpb.Secret{
			Replication: &secretmanagerpb.Replication{
				Replication: &secretmanagerpb.Replication_Automatic_{
					Automatic: &secretmanagerpb.Replication_Automatic{},
				},
			},
			Labels: map[string]string{"env": "test"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if created.Etag == "" {
		t.Errorf("created secret Etag empty, want an opaque tag")
	}
	if created.GetReplication().GetAutomatic() == nil {
		t.Errorf("replication not automatic: %+v", created.GetReplication())
	}

	// ADD VERSION with crc32c.
	av, err := c.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent:  created.Name,
		Payload: &secretmanagerpb.SecretPayload{Data: []byte("s3cr3t")},
	})
	if err != nil {
		t.Fatalf("AddSecretVersion: %v", err)
	}
	if av.State != secretmanagerpb.SecretVersion_ENABLED {
		t.Errorf("version state = %v, want ENABLED", av.State)
	}

	// ACCESS with crc32c verification (GAPIC verifies crc client-side if present).
	acc, err := c.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: created.Name + "/versions/latest",
	})
	if err != nil {
		t.Fatalf("AccessSecretVersion: %v", err)
	}
	if string(acc.Payload.Data) != "s3cr3t" {
		t.Errorf("payload = %q, want s3cr3t", acc.Payload.Data)
	}
	wantCRC := int64(crc32.Checksum([]byte("s3cr3t"), crc32.MakeTable(crc32.Castagnoli)))
	if acc.Payload.DataCrc32C == nil || *acc.Payload.DataCrc32C != wantCRC {
		t.Errorf("dataCrc32c = %v, want %d", acc.Payload.DataCrc32C, wantCRC)
	}

	// GET.
	got, err := c.GetSecret(ctx, &secretmanagerpb.GetSecretRequest{Name: created.Name})
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got.CreateTime == nil {
		t.Errorf("createTime nil on GetSecret")
	}

	// LIST secrets (server-streaming iterator).
	it := c.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{Parent: "projects/demo"})
	n := 0
	for {
		_, e := it.Next()
		if e != nil {
			break
		}
		n++
	}
	if n != 1 {
		t.Errorf("ListSecrets returned %d, want 1", n)
	}

	// LIST versions.
	vit := c.ListSecretVersions(ctx, &secretmanagerpb.ListSecretVersionsRequest{Parent: created.Name})
	vn := 0
	for {
		_, e := vit.Next()
		if e != nil {
			break
		}
		vn++
	}
	if vn != 1 {
		t.Errorf("ListSecretVersions returned %d, want 1", vn)
	}

	// DISABLE / ENABLE / DESTROY.
	if _, err := c.DisableSecretVersion(ctx, &secretmanagerpb.DisableSecretVersionRequest{Name: av.Name}); err != nil {
		t.Fatalf("DisableSecretVersion: %v", err)
	}
	if _, err := c.EnableSecretVersion(ctx, &secretmanagerpb.EnableSecretVersionRequest{Name: av.Name}); err != nil {
		t.Fatalf("EnableSecretVersion: %v", err)
	}
	des, err := c.DestroySecretVersion(ctx, &secretmanagerpb.DestroySecretVersionRequest{Name: av.Name})
	if err != nil {
		t.Fatalf("DestroySecretVersion: %v", err)
	}
	if des.State != secretmanagerpb.SecretVersion_DESTROYED {
		t.Errorf("state after destroy = %v, want DESTROYED", des.State)
	}

	// DELETE.
	if err := c.DeleteSecret(ctx, &secretmanagerpb.DeleteSecretRequest{Name: created.Name}); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
}

func TestGAPICUserManagedReplication(t *testing.T) {
	c := newGAPIC(t)
	ctx := context.Background()

	created, err := c.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   "projects/demo",
		SecretId: "gapic-um",
		Secret: &secretmanagerpb.Secret{
			Replication: &secretmanagerpb.Replication{
				Replication: &secretmanagerpb.Replication_UserManaged_{
					UserManaged: &secretmanagerpb.Replication_UserManaged{
						Replicas: []*secretmanagerpb.Replication_UserManaged_Replica{
							{Location: "us-east1"},
							{Location: "us-west1"},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateSecret(um): %v", err)
	}
	um := created.GetReplication().GetUserManaged()
	if um == nil || len(um.Replicas) != 2 {
		t.Fatalf("user-managed replication not round-tripped: %+v", created.GetReplication())
	}
}

// TestGAPICEdgeCases probes divergences the discovery-client tests miss:
// access on a versionless secret, access on a disabled "latest", and a
// create with an empty replication oneof.
func TestGAPICEdgeCases(t *testing.T) {
	c := newGAPIC(t)
	ctx := context.Background()

	// A create whose replication oneof is present but empty (neither automatic
	// nor userManaged) is rejected INVALID_ARGUMENT by real Secret Manager.
	_, err := c.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   "projects/demo",
		SecretId: "empty-rep",
		Secret:   &secretmanagerpb.Secret{Replication: &secretmanagerpb.Replication{}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("CreateSecret(empty replication) = %v, want InvalidArgument", err)
	}

	// A secret with no versions: access(latest) must be NOT_FOUND.
	sec, err := c.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   "projects/demo",
		SecretId: "no-versions",
		Secret: &secretmanagerpb.Secret{Replication: &secretmanagerpb.Replication{
			Replication: &secretmanagerpb.Replication_Automatic_{Automatic: &secretmanagerpb.Replication_Automatic{}},
		}},
	})
	if err != nil {
		t.Fatalf("CreateSecret(no-versions): %v", err)
	}
	_, err = c.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: sec.Name + "/versions/latest"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("Access(latest) on versionless secret = %v, want NotFound", err)
	}

	// Disable the latest version, then access(latest) must be FAILED_PRECONDITION.
	av, err := c.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent:  sec.Name,
		Payload: &secretmanagerpb.SecretPayload{Data: []byte("x")},
	})
	if err != nil {
		t.Fatalf("AddSecretVersion: %v", err)
	}
	if _, err := c.DisableSecretVersion(ctx, &secretmanagerpb.DisableSecretVersionRequest{Name: av.Name}); err != nil {
		t.Fatalf("DisableSecretVersion: %v", err)
	}
	// Access on a disabled version fails. Real Secret Manager returns
	// FAILED_PRECONDITION, which over REST transport is HTTP 400 — the GAPIC
	// REST client (via gax-go apierror) maps any HTTP 400 to InvalidArgument
	// regardless of the body's canonical status string, so a real user on the
	// REST client observes InvalidArgument here too (a gRPC-transport user would
	// see FailedPrecondition, but cloudemu serves REST only).
	_, err = c.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: sec.Name + "/versions/latest"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("Access(latest) on disabled version = %v (code %v), want HTTP 400", err, status.Code(err))
	}
}

// TestGAPICAddVersionCrcMismatch proves a client-supplied wrong CRC32C is
// rejected INVALID_ARGUMENT through the GAPIC (protojson int64-as-string) path.
func TestGAPICAddVersionCrcMismatch(t *testing.T) {
	c := newGAPIC(t)
	ctx := context.Background()

	sec, err := c.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
		Parent:   "projects/demo",
		SecretId: "crc-gapic",
		Secret: &secretmanagerpb.Secret{Replication: &secretmanagerpb.Replication{
			Replication: &secretmanagerpb.Replication_Automatic_{Automatic: &secretmanagerpb.Replication_Automatic{}},
		}},
	})
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	bad := int64(999)
	_, err = c.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent:  sec.Name,
		Payload: &secretmanagerpb.SecretPayload{Data: []byte("hello"), DataCrc32C: &bad},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("AddSecretVersion(bad crc) = %v, want InvalidArgument", err)
	}

	good := int64(crc32.Checksum([]byte("hello"), crc32.MakeTable(crc32.Castagnoli)))
	if _, err := c.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent:  sec.Name,
		Payload: &secretmanagerpb.SecretPayload{Data: []byte("hello"), DataCrc32C: &good},
	}); err != nil {
		t.Errorf("AddSecretVersion(good crc): %v", err)
	}
}
