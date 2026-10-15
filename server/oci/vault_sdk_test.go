package oci_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/identity"
	"github.com/oracle/oci-go-sdk/v65/keymanagement"
	"github.com/oracle/oci-go-sdk/v65/secrets"
	ocivault "github.com/oracle/oci-go-sdk/v65/vault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	ociprovider "github.com/stackshy/cloudemu/v2/providers/oci"
	ociserver "github.com/stackshy/cloudemu/v2/server/oci"
)

// sdkConfig is an oci-go-sdk configuration provider with a throwaway key; the
// emulator does not verify request signatures.
func sdkConfig(t *testing.T) common.ConfigurationProvider {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	return common.NewRawConfigurationProvider(
		config.DefaultTenancyOCID, "ocid1.user.oc1..sdk", "us-ashburn-1", "aa:bb", string(keyPEM), nil)
}

// loopbackClient dials the test server whatever host a request names, so a
// vault's labeled endpoint reaches it without depending on how the host
// resolves. Only the label-domain test uses it.
func loopbackClient(ts *httptest.Server) *http.Client {
	target := ts.Listener.Addr().String()
	dialer := &net.Dialer{}

	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, target)
		},
	}}
}

// noRetry keeps the SDK from retrying the 409s these tests expect.
var noRetry = common.NoRetryPolicy()

// createSDKVault creates a vault in the tenancy through the SDK's
// KmsVaultClient.
func createSDKVault(t *testing.T, cp common.ConfigurationProvider, host string) keymanagement.Vault {
	t.Helper()

	return createSDKVaultIn(t, cp, host, config.DefaultTenancyOCID)
}

// createSDKVaultIn creates a vault in a compartment through the SDK's
// KmsVaultClient.
func createSDKVaultIn(t *testing.T, cp common.ConfigurationProvider, host, compartment string) keymanagement.Vault {
	t.Helper()

	kmsVaults, err := keymanagement.NewKmsVaultClientWithConfigurationProvider(cp)
	require.NoError(t, err)
	kmsVaults.Host = host

	created, err := kmsVaults.CreateVault(context.Background(), keymanagement.CreateVaultRequest{
		CreateVaultDetails: keymanagement.CreateVaultDetails{
			CompartmentId: common.String(compartment),
			DisplayName:   common.String("sdk-vault"),
			VaultType:     keymanagement.CreateVaultDetailsVaultTypeDefault,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, created.WrappingkeyId)

	return created.Vault
}

func createSDKKey(
	t *testing.T, kms keymanagement.KmsManagementClient,
) (keymanagement.CreateKeyResponse, error) {
	t.Helper()

	return createSDKKeyIn(t, kms, config.DefaultTenancyOCID)
}

func createSDKKeyIn(
	t *testing.T, kms keymanagement.KmsManagementClient, compartment string,
) (keymanagement.CreateKeyResponse, error) {
	t.Helper()

	return kms.CreateKey(context.Background(), keymanagement.CreateKeyRequest{
		CreateKeyDetails: keymanagement.CreateKeyDetails{
			CompartmentId: common.String(compartment),
			DisplayName:   common.String("sdk-key"),
			KeyShape:      &keymanagement.KeyShape{Algorithm: keymanagement.KeyShapeAlgorithmAes, Length: common.Int(32)},
		},
		RequestMetadata: common.RequestMetadata{RetryPolicy: &noRetry},
	})
}

// sdkKMSClient is a stock KMS management client on a vault's own endpoint.
func sdkKMSClient(t *testing.T, cp common.ConfigurationProvider, v keymanagement.Vault) keymanagement.KmsManagementClient {
	t.Helper()

	kms, err := keymanagement.NewKmsManagementClientWithConfigurationProvider(cp, *v.ManagementEndpoint)
	require.NoError(t, err)

	return kms
}

func listSDKKeys(kms keymanagement.KmsManagementClient, compartment string) (keymanagement.ListKeysResponse, error) {
	return kms.ListKeys(context.Background(), keymanagement.ListKeysRequest{
		CompartmentId:   common.String(compartment),
		RequestMetadata: common.RequestMetadata{RetryPolicy: &noRetry},
	})
}

func scheduleSDKVaultDeletion(t *testing.T, cp common.ConfigurationProvider, host, vaultID string) {
	t.Helper()

	kmsVaults, err := keymanagement.NewKmsVaultClientWithConfigurationProvider(cp)
	require.NoError(t, err)
	kmsVaults.Host = host

	_, err = kmsVaults.ScheduleVaultDeletion(context.Background(), keymanagement.ScheduleVaultDeletionRequest{
		VaultId: common.String(vaultID),
	})
	require.NoError(t, err)
}

func requireSDKStatus(t *testing.T, err error, status int, code string) {
	t.Helper()

	require.Error(t, err)

	svcErr, ok := common.IsServiceError(err)
	require.True(t, ok, "not a service error: %v", err)
	assert.Equal(t, status, svcErr.GetHTTPStatusCode(), svcErr.GetMessage())
	assert.Equal(t, code, svcErr.GetCode())
}

// TestVaultSDKRoundTrip drives the emulator with the real oci-go-sdk Vault,
// KMS and Secrets clients: create a vault, a key through the vault's own
// management endpoint with a stock HTTP client, and a secret, then read the
// secret's versions and its bundle by name.
func TestVaultSDKRoundTrip(t *testing.T) {
	ctx := context.Background()
	ts := httptest.NewServer(ociserver.New(ociserver.DriversFrom(ociprovider.New())))
	t.Cleanup(ts.Close)

	cp := sdkConfig(t)
	compartment := common.String(config.DefaultTenancyOCID)

	created := createSDKVault(t, cp, ts.URL)

	// By default the management endpoint is the emulator's own address.
	assert.Equal(t, ts.URL, *created.ManagementEndpoint)
	assert.Equal(t, ts.URL, *created.CryptoEndpoint)

	kms, err := keymanagement.NewKmsManagementClientWithConfigurationProvider(cp, *created.ManagementEndpoint)
	require.NoError(t, err)

	// The SDK sends no vaultId: with one vault in the compartment it is that one.
	key, err := createSDKKey(t, kms)
	require.NoError(t, err)
	assert.Equal(t, *created.Id, *key.VaultId)

	keys, err := kms.ListKeys(ctx, keymanagement.ListKeysRequest{CompartmentId: compartment})
	require.NoError(t, err)
	require.Len(t, keys.Items, 1)

	vaults, err := ocivault.NewVaultsClientWithConfigurationProvider(cp)
	require.NoError(t, err)
	vaults.Host = ts.URL

	secret, err := vaults.CreateSecret(ctx, ocivault.CreateSecretRequest{
		CreateSecretDetails: ocivault.CreateSecretDetails{
			CompartmentId: compartment,
			SecretName:    common.String("db-password"),
			VaultId:       created.Id,
			KeyId:         key.Id,
			SecretContent: ocivault.Base64SecretContentDetails{
				Content: common.String(base64.StdEncoding.EncodeToString([]byte("hunter2"))),
			},
		},
	})
	require.NoError(t, err)

	got, err := vaults.GetSecret(ctx, ocivault.GetSecretRequest{SecretId: secret.Id})
	require.NoError(t, err)
	assert.Equal(t, "db-password", *got.SecretName)

	versions, err := vaults.ListSecretVersions(ctx, ocivault.ListSecretVersionsRequest{SecretId: secret.Id})
	require.NoError(t, err)
	require.Len(t, versions.Items, 1)

	version, err := vaults.GetSecretVersion(ctx, ocivault.GetSecretVersionRequest{
		SecretId: secret.Id, SecretVersionNumber: common.Int64(1),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), *version.VersionNumber)
	assert.Equal(t, ocivault.SecretVersionContentTypeBase64, version.ContentType)

	reader, err := secrets.NewSecretsClientWithConfigurationProvider(cp)
	require.NoError(t, err)
	reader.Host = ts.URL

	bundle, err := reader.GetSecretBundleByName(ctx, secrets.GetSecretBundleByNameRequest{
		SecretName: common.String("db-password"), VaultId: created.Id,
	})
	require.NoError(t, err)
	assert.Equal(t, *secret.Id, *bundle.SecretId)

	content, ok := bundle.SecretBundleContent.(secrets.Base64SecretBundleContentDetails)
	require.True(t, ok)

	raw, err := base64.StdEncoding.DecodeString(*content.Content)
	require.NoError(t, err)
	assert.Equal(t, "hunter2", string(raw))
}

// TestVaultSDKLabelDomain covers the opt-in label hosts: with an endpoint
// domain set, each vault's management endpoint names it, so a KMS client
// reaches the right vault with several in one compartment.
func TestVaultSDKLabelDomain(t *testing.T) {
	d := ociserver.DriversFrom(ociprovider.New())
	d.KMSEndpointDomain = "kms.localhost"

	ts := httptest.NewServer(ociserver.New(d))
	t.Cleanup(ts.Close)

	cp := sdkConfig(t)

	createSDKVault(t, cp, ts.URL)
	created := createSDKVault(t, cp, ts.URL)

	endpoint, err := url.Parse(*created.ManagementEndpoint)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(endpoint.Hostname(), "-management.kms.localhost"), endpoint.Host)
	assert.Equal(t, ts.Listener.Addr().(*net.TCPAddr).Port, mustPort(t, endpoint))

	kms, err := keymanagement.NewKmsManagementClientWithConfigurationProvider(cp, *created.ManagementEndpoint)
	require.NoError(t, err)
	kms.HTTPClient = loopbackClient(ts)

	key, err := createSDKKey(t, kms)
	require.NoError(t, err)
	assert.Equal(t, *created.Id, *key.VaultId)
}

// TestVaultSDKSeveralVaultsOnTheDefaultAddress drives stock KMS clients, each
// on its own vault's managementEndpoint, while the tenancy has two active
// vaults, a pending one and one in another compartment. Every vault shares the
// default address, so each key request is refused rather than landing in a
// vault the client did not address.
func TestVaultSDKSeveralVaultsOnTheDefaultAddress(t *testing.T) {
	ts := httptest.NewServer(ociserver.New(ociserver.DriversFrom(ociprovider.New())))
	t.Cleanup(ts.Close)

	cp := sdkConfig(t)
	tenancy := config.DefaultTenancyOCID

	iam, err := identity.NewIdentityClientWithConfigurationProvider(cp)
	require.NoError(t, err)
	iam.Host = ts.URL

	comp, err := iam.CreateCompartment(context.Background(), identity.CreateCompartmentRequest{
		CreateCompartmentDetails: identity.CreateCompartmentDetails{
			CompartmentId: common.String(tenancy),
			Name:          common.String("apps"),
			Description:   common.String("apps"),
		},
	})
	require.NoError(t, err)

	activeA := createSDKVault(t, cp, ts.URL)
	activeB := createSDKVault(t, cp, ts.URL)
	pending := createSDKVault(t, cp, ts.URL)
	scheduleSDKVaultDeletion(t, cp, ts.URL, *pending.Id)
	elsewhere := createSDKVaultIn(t, cp, ts.URL, *comp.Id)

	for _, v := range []keymanagement.Vault{activeA, activeB, pending, elsewhere} {
		assert.Equal(t, ts.URL, *v.ManagementEndpoint)

		kms := sdkKMSClient(t, cp, v)

		for _, compartment := range []string{tenancy, *comp.Id} {
			_, err := createSDKKeyIn(t, kms, compartment)
			requireSDKStatus(t, err, http.StatusBadRequest, "InvalidParameter")

			_, err = listSDKKeys(kms, compartment)
			requireSDKStatus(t, err, http.StatusBadRequest, "InvalidParameter")
		}
	}

	// Nothing landed anywhere.
	for _, v := range []keymanagement.Vault{activeA, activeB, pending, elsewhere} {
		for _, compartment := range []string{tenancy, *comp.Id} {
			resp, err := http.Get(ts.URL + "/20180608/keys?compartmentId=" + compartment + "&vaultId=" + *v.Id)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			var keys []map[string]any
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&keys))
			require.NoError(t, resp.Body.Close())
			assert.Empty(t, keys)
		}
	}
}

// TestVaultSDKSolePendingVault covers a tenancy whose only vault is pending
// deletion: a new key is refused with 409, and its keys still list.
func TestVaultSDKSolePendingVault(t *testing.T) {
	ts := httptest.NewServer(ociserver.New(ociserver.DriversFrom(ociprovider.New())))
	t.Cleanup(ts.Close)

	cp := sdkConfig(t)

	v := createSDKVault(t, cp, ts.URL)
	kms := sdkKMSClient(t, cp, v)

	key, err := createSDKKey(t, kms)
	require.NoError(t, err)

	scheduleSDKVaultDeletion(t, cp, ts.URL, *v.Id)

	_, err = createSDKKey(t, kms)
	requireSDKStatus(t, err, http.StatusConflict, "IncorrectState")

	keys, err := listSDKKeys(kms, config.DefaultTenancyOCID)
	require.NoError(t, err)
	require.Len(t, keys.Items, 1)
	assert.Equal(t, *key.Id, *keys.Items[0].Id)
}

func mustPort(t *testing.T, u *url.URL) int {
	t.Helper()

	addr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort("127.0.0.1", u.Port()))
	require.NoError(t, err)

	return addr.Port
}
