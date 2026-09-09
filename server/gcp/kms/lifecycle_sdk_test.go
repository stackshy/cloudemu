package kms_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2/config"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	testLocationParent = "projects/demo/locations/us-central1"
	testKeyRingID      = "app-keys"
	testKeyRingName    = testLocationParent + "/keyRings/" + testKeyRingID
)

// newKMSService starts the GCP wire server (which always registers Cloud KMS)
// and returns a real cloudkms/v1 REST client pointed at it, plus the fixed
// clock its timestamps derive from.
func newKMSService(t *testing.T) (*cloudkms.Service, time.Time) {
	t.Helper()

	base := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	srv := gcpserver.New(gcpserver.Drivers{Clock: config.NewFakeClock(base)})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := cloudkms.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloudkms.NewService: %v", err)
	}

	return svc, base
}

func mustKeyRing(t *testing.T, svc *cloudkms.Service) {
	t.Helper()

	_, err := svc.Projects.Locations.KeyRings.
		Create(testLocationParent, &cloudkms.KeyRing{}).
		KeyRingId(testKeyRingID).Do()
	if err != nil {
		t.Fatalf("KeyRings.Create: %v", err)
	}
}

func TestKMSKeyRingLifecycle(t *testing.T) {
	svc, _ := newKMSService(t)

	kr, err := svc.Projects.Locations.KeyRings.
		Create(testLocationParent, &cloudkms.KeyRing{}).
		KeyRingId(testKeyRingID).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if kr.Name != testKeyRingName {
		t.Fatalf("name = %q, want %q", kr.Name, testKeyRingName)
	}

	if kr.CreateTime == "" {
		t.Fatal("createTime is empty")
	}

	got, err := svc.Projects.Locations.KeyRings.Get(testKeyRingName).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != testKeyRingName {
		t.Fatalf("Get name = %q", got.Name)
	}

	list, err := svc.Projects.Locations.KeyRings.List(testLocationParent).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.KeyRings) != 1 || list.KeyRings[0].Name != testKeyRingName {
		t.Fatalf("List = %+v", list.KeyRings)
	}

	// Duplicate create is 409 ALREADY_EXISTS.
	_, err = svc.Projects.Locations.KeyRings.
		Create(testLocationParent, &cloudkms.KeyRing{}).KeyRingId(testKeyRingID).Do()
	assertAPICode(t, err, 409)
}

func TestKMSCryptoKeyAutoPrimaryVersionAndRoundTrip(t *testing.T) {
	svc, base := newKMSService(t)
	mustKeyRing(t, svc)

	ck, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{
			Purpose:          "ENCRYPT_DECRYPT",
			RotationPeriod:   "7776000s",
			NextRotationTime: base.Add(90 * 24 * time.Hour).Format(time.RFC3339),
			Labels:           map[string]string{"env": "prod"},
			VersionTemplate: &cloudkms.CryptoKeyVersionTemplate{
				Algorithm:       "GOOGLE_SYMMETRIC_ENCRYPTION",
				ProtectionLevel: "SOFTWARE",
			},
		}).CryptoKeyId("data-key").Do()
	if err != nil {
		t.Fatalf("CryptoKeys.Create: %v", err)
	}

	wantName := testKeyRingName + "/cryptoKeys/data-key"
	if ck.Name != wantName {
		t.Fatalf("name = %q, want %q", ck.Name, wantName)
	}

	if ck.Purpose != "ENCRYPT_DECRYPT" {
		t.Fatalf("purpose = %q", ck.Purpose)
	}

	if ck.RotationPeriod != "7776000s" {
		t.Fatalf("rotationPeriod = %q", ck.RotationPeriod)
	}

	if ck.VersionTemplate == nil || ck.VersionTemplate.Algorithm != "GOOGLE_SYMMETRIC_ENCRYPTION" ||
		ck.VersionTemplate.ProtectionLevel != "SOFTWARE" {
		t.Fatalf("versionTemplate = %+v", ck.VersionTemplate)
	}

	// The #1 Terraform-drift guard: an initial primary version exists in ENABLED.
	if ck.Primary == nil {
		t.Fatal("primary version was not auto-created")
	}

	if ck.Primary.State != "ENABLED" {
		t.Fatalf("primary state = %q, want ENABLED", ck.Primary.State)
	}

	if ck.Primary.Name != wantName+"/cryptoKeyVersions/1" {
		t.Fatalf("primary name = %q", ck.Primary.Name)
	}

	if ck.DestroyScheduledDuration != "86400s" {
		t.Fatalf("destroyScheduledDuration = %q, want default 86400s", ck.DestroyScheduledDuration)
	}

	// Round-trip the whole resource through Get.
	got, err := svc.Projects.Locations.KeyRings.CryptoKeys.Get(wantName).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Labels["env"] != "prod" || got.NextRotationTime == "" || got.Primary == nil {
		t.Fatalf("round-trip lost fields: %+v", got)
	}
}

func TestKMSCryptoKeyDefaultsSymmetricVersionTemplate(t *testing.T) {
	svc, _ := newKMSService(t)
	mustKeyRing(t, svc)

	// A symmetric ENCRYPT_DECRYPT key created without a versionTemplate must not
	// be rejected: real Cloud KMS (and the google_kms_crypto_key resource) default
	// the algorithm to GOOGLE_SYMMETRIC_ENCRYPTION and protectionLevel to SOFTWARE.
	ck, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{
			Purpose: "ENCRYPT_DECRYPT",
		}).CryptoKeyId("default-sym").Do()
	if err != nil {
		t.Fatalf("CryptoKeys.Create without versionTemplate: %v", err)
	}

	if ck.VersionTemplate == nil ||
		ck.VersionTemplate.Algorithm != "GOOGLE_SYMMETRIC_ENCRYPTION" ||
		ck.VersionTemplate.ProtectionLevel != "SOFTWARE" {
		t.Fatalf("defaulted versionTemplate = %+v", ck.VersionTemplate)
	}

	// The auto-created primary version uses the defaulted algorithm.
	if ck.Primary == nil || ck.Primary.Algorithm != "GOOGLE_SYMMETRIC_ENCRYPTION" {
		t.Fatalf("primary version = %+v", ck.Primary)
	}

	// A non-symmetric purpose still requires an explicit algorithm.
	_, err = svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{
			Purpose: "ASYMMETRIC_SIGN",
		}).CryptoKeyId("needs-algo").Do()
	if err == nil {
		t.Fatal("ASYMMETRIC_SIGN without versionTemplate = nil error, want required-algorithm")
	}
}

func TestKMSListVersionsToleratesDoubledV1Prefix(t *testing.T) {
	// terraform-provider-google's crypto-key delete lists versions at a doubled
	// ".../v1/v1/..." path when the KMS endpoint is overridden. Model that raw
	// request and assert the handler resolves it (200) rather than 501/404, so a
	// real `terraform destroy` of a crypto key completes.
	base := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	srv := gcpserver.New(gcpserver.Drivers{Clock: config.NewFakeClock(base)})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := cloudkms.NewService(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("cloudkms.NewService: %v", err)
	}

	if _, err := svc.Projects.Locations.KeyRings.
		Create(testLocationParent, &cloudkms.KeyRing{}).KeyRingId(testKeyRingID).Do(); err != nil {
		t.Fatalf("KeyRings.Create: %v", err)
	}

	if _, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{Purpose: "ENCRYPT_DECRYPT"}).
		CryptoKeyId("dk").Do(); err != nil {
		t.Fatalf("CryptoKeys.Create: %v", err)
	}

	doubled := ts.URL + "/v1/v1/" + testKeyRingName + "/cryptoKeys/dk/cryptoKeyVersions"

	resp, err := http.Get(doubled) //nolint:noctx // test request
	if err != nil {
		t.Fatalf("GET doubled path: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("doubled-prefix list status = %d, want 200", resp.StatusCode)
	}
}

func TestKMSCryptoKeyComputesNextRotationTime(t *testing.T) {
	svc, base := newKMSService(t)
	mustKeyRing(t, svc)

	// No nextRotationTime supplied — the handler derives it from createTime +
	// rotationPeriod.
	ck, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{
			Purpose:        "ENCRYPT_DECRYPT",
			RotationPeriod: "86400s",
			VersionTemplate: &cloudkms.CryptoKeyVersionTemplate{
				Algorithm: "GOOGLE_SYMMETRIC_ENCRYPTION",
			},
		}).CryptoKeyId("rot-key").Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := base.Add(24 * time.Hour)

	got, err := time.Parse(time.RFC3339, ck.NextRotationTime)
	if err != nil {
		t.Fatalf("parse nextRotationTime %q: %v", ck.NextRotationTime, err)
	}

	if !got.Equal(want) {
		t.Fatalf("nextRotationTime = %v, want %v", got, want)
	}
}

func TestKMSSkipInitialVersionCreation(t *testing.T) {
	svc, _ := newKMSService(t)
	mustKeyRing(t, svc)

	ck, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{
			Purpose: "ASYMMETRIC_SIGN",
			VersionTemplate: &cloudkms.CryptoKeyVersionTemplate{
				Algorithm: "EC_SIGN_P256_SHA256",
			},
		}).CryptoKeyId("no-initial").SkipInitialVersionCreation(true).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if ck.Primary != nil {
		t.Fatalf("primary = %+v, want nil with skipInitialVersionCreation", ck.Primary)
	}

	vers, err := svc.Projects.Locations.KeyRings.CryptoKeys.CryptoKeyVersions.
		List(ck.Name).Do()
	if err != nil {
		t.Fatalf("Versions.List: %v", err)
	}

	if len(vers.CryptoKeyVersions) != 0 {
		t.Fatalf("versions = %+v, want none", vers.CryptoKeyVersions)
	}
}

func TestKMSVersionCreateDestroyRestore(t *testing.T) {
	svc, _ := newKMSService(t)
	mustKeyRing(t, svc)

	ckName := testKeyRingName + "/cryptoKeys/rotating"

	_, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{
			Purpose:         "ENCRYPT_DECRYPT",
			VersionTemplate: &cloudkms.CryptoKeyVersionTemplate{Algorithm: "GOOGLE_SYMMETRIC_ENCRYPTION"},
		}).CryptoKeyId("rotating").Do()
	if err != nil {
		t.Fatalf("CryptoKeys.Create: %v", err)
	}

	v2, err := svc.Projects.Locations.KeyRings.CryptoKeys.CryptoKeyVersions.
		Create(ckName, &cloudkms.CryptoKeyVersion{}).Do()
	if err != nil {
		t.Fatalf("Versions.Create: %v", err)
	}

	if v2.State != "ENABLED" || v2.Name != ckName+"/cryptoKeyVersions/2" {
		t.Fatalf("v2 = %+v", v2)
	}

	// Promote v2 to primary.
	ck, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		UpdatePrimaryVersion(ckName, &cloudkms.UpdateCryptoKeyPrimaryVersionRequest{
			CryptoKeyVersionId: "2",
		}).Do()
	if err != nil {
		t.Fatalf("UpdatePrimaryVersion: %v", err)
	}

	if ck.Primary == nil || ck.Primary.Name != v2.Name {
		t.Fatalf("primary = %+v, want v2", ck.Primary)
	}

	// Terraform destroy path: :destroy schedules destruction, does not delete.
	destroyed, err := svc.Projects.Locations.KeyRings.CryptoKeys.CryptoKeyVersions.
		Destroy(v2.Name, &cloudkms.DestroyCryptoKeyVersionRequest{}).Do()
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if destroyed.State != "DESTROY_SCHEDULED" || destroyed.DestroyTime == "" {
		t.Fatalf("destroyed = %+v, want DESTROY_SCHEDULED with destroyTime", destroyed)
	}

	// The version (and its key) still exist after :destroy.
	if _, err := svc.Projects.Locations.KeyRings.CryptoKeys.CryptoKeyVersions.Get(v2.Name).Do(); err != nil {
		t.Fatalf("Get after destroy: %v", err)
	}

	restored, err := svc.Projects.Locations.KeyRings.CryptoKeys.CryptoKeyVersions.
		Restore(v2.Name, &cloudkms.RestoreCryptoKeyVersionRequest{}).Do()
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if restored.State != "DISABLED" || restored.DestroyTime != "" {
		t.Fatalf("restored = %+v, want DISABLED with no destroyTime", restored)
	}
}

func TestKMSVersionPatchEnableDisable(t *testing.T) {
	svc, _ := newKMSService(t)
	mustKeyRing(t, svc)

	ck, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{
			Purpose:         "ENCRYPT_DECRYPT",
			VersionTemplate: &cloudkms.CryptoKeyVersionTemplate{Algorithm: "GOOGLE_SYMMETRIC_ENCRYPTION"},
		}).CryptoKeyId("togglable").Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	vName := ck.Primary.Name

	disabled, err := svc.Projects.Locations.KeyRings.CryptoKeys.CryptoKeyVersions.
		Patch(vName, &cloudkms.CryptoKeyVersion{State: "DISABLED"}).UpdateMask("state").Do()
	if err != nil {
		t.Fatalf("Patch disable: %v", err)
	}

	if disabled.State != "DISABLED" {
		t.Fatalf("state = %q, want DISABLED", disabled.State)
	}

	enabled, err := svc.Projects.Locations.KeyRings.CryptoKeys.CryptoKeyVersions.
		Patch(vName, &cloudkms.CryptoKeyVersion{State: "ENABLED"}).UpdateMask("state").Do()
	if err != nil {
		t.Fatalf("Patch enable: %v", err)
	}

	if enabled.State != "ENABLED" {
		t.Fatalf("state = %q, want ENABLED", enabled.State)
	}
}

func TestKMSCryptoKeyPatchLabelsAndRotation(t *testing.T) {
	svc, _ := newKMSService(t)
	mustKeyRing(t, svc)

	ckName := testKeyRingName + "/cryptoKeys/patchme"

	_, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{
			Purpose:         "ENCRYPT_DECRYPT",
			RotationPeriod:  "7776000s",
			VersionTemplate: &cloudkms.CryptoKeyVersionTemplate{Algorithm: "GOOGLE_SYMMETRIC_ENCRYPTION"},
		}).CryptoKeyId("patchme").Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	patched, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		Patch(ckName, &cloudkms.CryptoKey{
			Labels:         map[string]string{"team": "sec"},
			RotationPeriod: "2592000s",
		}).UpdateMask("labels,rotationPeriod").Do()
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if patched.Labels["team"] != "sec" {
		t.Fatalf("labels = %+v", patched.Labels)
	}

	if patched.RotationPeriod != "2592000s" {
		t.Fatalf("rotationPeriod = %q, want 2592000s", patched.RotationPeriod)
	}
}

func TestKMSCryptoKeyValidation(t *testing.T) {
	svc, _ := newKMSService(t)
	mustKeyRing(t, svc)

	// Missing purpose -> 400.
	_, err := svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{
			VersionTemplate: &cloudkms.CryptoKeyVersionTemplate{Algorithm: "GOOGLE_SYMMETRIC_ENCRYPTION"},
		}).CryptoKeyId("no-purpose").Do()
	assertAPICode(t, err, 400)

	// A non-symmetric purpose without versionTemplate.algorithm -> 400. (A
	// symmetric ENCRYPT_DECRYPT key defaults the algorithm instead; see
	// TestKMSCryptoKeyDefaultsSymmetricVersionTemplate.)
	_, err = svc.Projects.Locations.KeyRings.CryptoKeys.
		Create(testKeyRingName, &cloudkms.CryptoKey{Purpose: "ASYMMETRIC_SIGN"}).
		CryptoKeyId("no-algo").Do()
	assertAPICode(t, err, 400)

	// Get on a missing key ring -> 404.
	_, err = svc.Projects.Locations.KeyRings.Get(testLocationParent + "/keyRings/nope").Do()
	assertAPICode(t, err, 404)
}

func TestKMSIAMPolicyRoundTrip(t *testing.T) {
	svc, _ := newKMSService(t)
	mustKeyRing(t, svc)

	// getIamPolicy on an unset resource returns an empty, etagged policy.
	pol, err := svc.Projects.Locations.KeyRings.GetIamPolicy(testKeyRingName).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if pol.Etag == "" {
		t.Fatal("initial policy has no etag")
	}

	set, err := svc.Projects.Locations.KeyRings.SetIamPolicy(testKeyRingName, &cloudkms.SetIamPolicyRequest{
		Policy: &cloudkms.Policy{
			Etag: pol.Etag,
			Bindings: []*cloudkms.Binding{{
				Role:    "roles/cloudkms.cryptoKeyEncrypterDecrypter",
				Members: []string{"user:alice@example.com"},
			}},
		},
	}).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if len(set.Bindings) != 1 || set.Bindings[0].Members[0] != "user:alice@example.com" {
		t.Fatalf("set policy = %+v", set.Bindings)
	}

	// Stale etag is rejected (read-modify-write contract).
	_, err = svc.Projects.Locations.KeyRings.SetIamPolicy(testKeyRingName, &cloudkms.SetIamPolicyRequest{
		Policy: &cloudkms.Policy{Etag: pol.Etag},
	}).Do()
	assertAPICode(t, err, 409)

	test, err := svc.Projects.Locations.KeyRings.TestIamPermissions(testKeyRingName,
		&cloudkms.TestIamPermissionsRequest{Permissions: []string{"cloudkms.cryptoKeyVersions.useToEncrypt"}}).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(test.Permissions) != 1 {
		t.Fatalf("permissions = %+v", test.Permissions)
	}
}

// assertAPICode asserts err is a googleapi.Error with the given HTTP code.
func assertAPICode(t *testing.T, err error, want int) {
	t.Helper()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("error = %v, want *googleapi.Error with code %d", err, want)
	}

	if gerr.Code != want {
		t.Fatalf("code = %d, want %d (err %v)", gerr.Code, want, err)
	}
}
