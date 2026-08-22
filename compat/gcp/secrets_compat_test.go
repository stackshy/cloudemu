package gcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"google.golang.org/api/option"
	sm "google.golang.org/api/secretmanager/v1"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGCPSecretsCompat drives a Secret Manager secret + version lifecycle
// through the real google.golang.org/api/secretmanager/v1 client. Secrets map
// onto the portable "secrets" driver, so operation names match AWS Secrets
// Manager's in docs/coverage/coverage.json.
func TestGCPSecretsCompat(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{SecretManager: provider.SecretManager})
	ctx := context.Background()

	svcClient, err := sm.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("secretmanager service: %v", err)
	}

	const (
		svc      = "secrets"
		secretID = "db-password"
		payload  = "s3cr3t-value"
	)

	parent := "projects/" + compat.GCPProject
	name := parent + "/secrets/" + secretID

	sess.Op(svc, "CreateSecret", func() error {
		_, err := svcClient.Projects.Secrets.Create(parent, &sm.Secret{
			Replication: &sm.Replication{Automatic: &sm.Automatic{}},
			Labels:      map[string]string{"env": "test"},
		}).SecretId(secretID).Context(ctx).Do()

		return err
	})

	sess.Op(svc, "GetSecret", func() error {
		got, err := svcClient.Projects.Secrets.Get(name).Context(ctx).Do()
		if err != nil {
			return err
		}

		if got.Name != name {
			return fmt.Errorf("GetSecret name = %q, want %q", got.Name, name)
		}

		return nil
	})

	sess.Op(svc, "ListSecrets", func() error {
		list, err := svcClient.Projects.Secrets.List(parent).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(list.Secrets) != 1 {
			return fmt.Errorf("ListSecrets = %d secrets, want 1", len(list.Secrets))
		}

		return nil
	})

	sess.Op(svc, "PutSecretValue", func() error {
		_, err := svcClient.Projects.Secrets.AddVersion(name, &sm.AddSecretVersionRequest{
			Payload: &sm.SecretPayload{Data: base64.StdEncoding.EncodeToString([]byte(payload))},
		}).Context(ctx).Do()

		return err
	})

	sess.Op(svc, "ListSecretVersions", func() error {
		versions, err := svcClient.Projects.Secrets.Versions.List(name).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(versions.Versions) == 0 {
			return fmt.Errorf("ListSecretVersions returned no versions")
		}

		return nil
	})

	sess.Op(svc, "GetSecretValue", func() error {
		got, err := svcClient.Projects.Secrets.Versions.Access(name + "/versions/latest").Context(ctx).Do()
		if err != nil {
			return err
		}

		want := base64.StdEncoding.EncodeToString([]byte(payload))
		if got.Payload.Data != want {
			return fmt.Errorf("GetSecretValue = %q, want %q", got.Payload.Data, want)
		}

		return nil
	})

	sess.Op(svc, "DeleteSecret", func() error {
		_, err := svcClient.Projects.Secrets.Delete(name).Context(ctx).Do()

		return err
	})
}
