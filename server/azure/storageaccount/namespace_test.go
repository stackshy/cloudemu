// Real-SDK tests for the storage-account namespace: an account is its own
// RG-scoped, globally named resource (not a blob container), and owns its ARM
// blob containers.

package storageaccount_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// nsEnv is one emulator shared by clients of several subscriptions.
type nsEnv struct {
	t     *testing.T
	ts    *httptest.Server
	cloud *azureprovider.Provider
}

func newNSEnv(t *testing.T) *nsEnv {
	t.Helper()

	return newNSEnvFor(t, cloudemu.NewAzure())
}

func newNSEnvFor(t *testing.T, p *azureprovider.Provider) *nsEnv {
	t.Helper()

	srv := azureserver.New(azureserver.Drivers{BlobStorage: p.BlobStorage})
	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return &nsEnv{t: t, ts: ts, cloud: p}
}

func (e *nsEnv) clientOpts() *arm.ClientOptions {
	return &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{
			ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: e.ts.URL, Audience: "https://management.azure.com"},
			},
		},
		Transport: e.ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}
}

func (e *nsEnv) accounts(sub string) *armstorage.AccountsClient {
	e.t.Helper()

	c, err := armstorage.NewAccountsClient(sub, fakeCred{}, e.clientOpts())
	require.NoError(e.t, err)

	return c
}

func (e *nsEnv) containers(sub string) *armstorage.BlobContainersClient {
	e.t.Helper()

	c, err := armstorage.NewBlobContainersClient(sub, fakeCred{}, e.clientOpts())
	require.NoError(e.t, err)

	return c
}

func (e *nsEnv) createAccount(sub, rg, name string) (armstorage.AccountsClientCreateResponse, error) {
	e.t.Helper()
	ensureRG(e.t, e.ts, sub, rg)

	ctx := context.Background()

	poller, err := e.accounts(sub).BeginCreate(ctx, rg, name, armstorage.AccountCreateParameters{
		Location: to.Ptr("westus2"),
		Kind:     to.Ptr(armstorage.KindStorageV2),
		SKU:      &armstorage.SKU{Name: to.Ptr(armstorage.SKUNameStandardGRS)},
	}, nil)
	if err != nil {
		return armstorage.AccountsClientCreateResponse{}, err
	}

	return poller.PollUntilDone(ctx, nil)
}

func (e *nsEnv) mustCreateAccount(sub, rg, name string) {
	e.t.Helper()

	_, err := e.createAccount(sub, rg, name)
	require.NoError(e.t, err)
}

// raw issues a plain HTTP request and returns the status and body.
func (e *nsEnv) raw(method, path, body string) (int, string) {
	e.t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, e.ts.URL+path, strings.NewReader(body))
	require.NoError(e.t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.ts.Client().Do(req)
	require.NoError(e.t, err)

	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

func armErrorCode(t *testing.T, err error) (string, int) {
	t.Helper()

	var re *azcore.ResponseError

	require.True(t, errors.As(err, &re), "want *azcore.ResponseError, got %v", err)

	return re.ErrorCode, re.StatusCode
}

// TestAccountNameIsGlobal is AZSTO-03: a second PUT of the same name in another
// resource group or subscription fails with the real codes instead of
// silently moving the account.
func TestAccountNameIsGlobal(t *testing.T) {
	e := newNSEnv(t)
	e.mustCreateAccount("sub-1", "rga", "acctbb")

	_, err := e.createAccount("sub-1", "rgb", "acctbb")
	code, status := armErrorCode(t, err)
	assert.Equal(t, "StorageAccountInAnotherResourceGroup", code)
	assert.Equal(t, http.StatusConflict, status)

	_, err = e.createAccount("sub-2", "rga", "acctbb")
	code, status = armErrorCode(t, err)
	assert.Equal(t, "StorageAccountAlreadyTaken", code)
	assert.Equal(t, http.StatusConflict, status)

	got, err := e.accounts("sub-1").GetProperties(context.Background(), "rga", "acctbb", nil)
	require.NoError(t, err)
	assert.Equal(t, "westus2", *got.Location)
	assert.Equal(t, armstorage.SKUNameStandardGRS, *got.SKU.Name)
	assert.Contains(t, *got.ID, "/resourceGroups/rga/")

	// Same group is a create-or-update.
	_, err = e.createAccount("sub-1", "rga", "acctbb")
	require.NoError(t, err)
}

// TestAccountIsResourceGroupScoped is AZSTO-04 and AZSTO-17.
func TestAccountIsResourceGroupScoped(t *testing.T) {
	ctx := context.Background()
	e := newNSEnv(t)
	e.mustCreateAccount("sub-1", "rga", "accta")
	e.mustCreateAccount("sub-1", "rgb", "acctb")
	require.NoError(t, e.cloud.BlobStorage.CreateBucket(ctx, "plaincontainer"))

	client := e.accounts("sub-1")

	_, err := client.GetProperties(ctx, "rgb", "accta", nil)
	code, status := armErrorCode(t, err)
	assert.Equal(t, "ResourceNotFound", code)
	assert.Equal(t, http.StatusNotFound, status)

	_, err = client.Update(ctx, "rgb", "accta", armstorage.AccountUpdateParameters{Tags: map[string]*string{}}, nil)
	_, status = armErrorCode(t, err)
	assert.Equal(t, http.StatusNotFound, status)

	status, _ = e.raw(http.MethodPost,
		"/subscriptions/sub-1/resourceGroups/rgb/providers/Microsoft.Storage/storageAccounts/accta/listKeys?api-version=2023-05-01", "")
	assert.Equal(t, http.StatusNotFound, status)

	var rgNames []string

	pager := client.NewListByResourceGroupPager("rga", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		require.NoError(t, err)

		for _, a := range page.Value {
			rgNames = append(rgNames, *a.Name)
		}
	}

	assert.Equal(t, []string{"accta"}, rgNames)

	ids := map[string]string{}

	subPager := client.NewListPager(nil)
	for subPager.More() {
		page, err := subPager.NextPage(ctx)
		require.NoError(t, err)

		for _, a := range page.Value {
			ids[*a.Name] = *a.ID
		}
	}

	assert.Len(t, ids, 2, "only accounts are listed, never containers: %v", ids)
	assert.Contains(t, ids["accta"], "/resourceGroups/rga/")
	assert.Contains(t, ids["acctb"], "/resourceGroups/rgb/")

	// Another subscription sees none of them.
	other := e.accounts("sub-2").NewListPager(nil)
	page, err := other.NextPage(ctx)
	require.NoError(t, err)
	assert.Empty(t, page.Value)

	// Delete from the wrong group is a no-op 204; the account survives.
	status, _ = e.raw(http.MethodDelete,
		"/subscriptions/sub-1/resourceGroups/rgb/providers/Microsoft.Storage/storageAccounts/accta?api-version=2023-05-01", "")
	assert.Equal(t, http.StatusNoContent, status)

	_, err = client.GetProperties(ctx, "rga", "accta", nil)
	require.NoError(t, err)

	status, _ = e.raw(http.MethodDelete,
		"/subscriptions/sub-1/resourceGroups/rga/providers/Microsoft.Storage/storageAccounts/accta?api-version=2023-05-01", "")
	assert.Equal(t, http.StatusOK, status)

	status, _ = e.raw(http.MethodDelete,
		"/subscriptions/sub-1/resourceGroups/rga/providers/Microsoft.Storage/storageAccounts/accta?api-version=2023-05-01", "")
	assert.Equal(t, http.StatusNoContent, status, "deleting a missing account is 204")
}

// TestDeleteAccountKeepsSameNamedContainer is AZSTO-01 over the wire: the
// account no longer is the container of the same name, so a delete neither
// removes the user's container nor fails because it holds blobs.
func TestDeleteAccountKeepsSameNamedContainer(t *testing.T) {
	ctx := context.Background()
	e := newNSEnv(t)
	e.mustCreateAccount("sub-1", "rg-1", "pg2")

	blob := e.cloud.BlobStorage
	require.NoError(t, blob.CreateBucket(ctx, "pg2"))
	require.NoError(t, blob.PutObject(ctx, "pg2", "keep.txt", []byte("x"), "text/plain", nil))

	_, err := e.accounts("sub-1").Delete(ctx, "rg-1", "pg2", nil)
	require.NoError(t, err)

	_, err = blob.GetObject(ctx, "pg2", "keep.txt")
	require.NoError(t, err, "the default-namespace container pg2 must survive its namesake account's delete")

	_, err = e.accounts("sub-1").GetProperties(ctx, "rg-1", "pg2", nil)
	_, status := armErrorCode(t, err)
	assert.Equal(t, http.StatusNotFound, status)
}

// TestPurgeRemovesAccountsAndContainers drives the resource-group cascade.
func TestPurgeRemovesAccountsAndContainers(t *testing.T) {
	ctx := context.Background()
	e := newNSEnv(t)
	e.mustCreateAccount("sub-1", "rg1", "acctin")
	e.mustCreateAccount("sub-1", "rg10", "acctout")

	cc := e.containers("sub-1")
	_, err := cc.Create(ctx, "rg1", "acctin", "data", armstorage.BlobContainer{}, nil)
	require.NoError(t, err)
	_, err = cc.Create(ctx, "rg10", "acctout", "data", armstorage.BlobContainer{}, nil)
	require.NoError(t, err)

	status, _ := e.raw(http.MethodDelete, "/subscriptions/sub-1/resourcegroups/rg1?api-version=2021-04-01", "")
	require.Less(t, status, http.StatusMultipleChoices)

	ensureRG(t, e.ts, "sub-1", "rg1")

	_, err = e.accounts("sub-1").GetProperties(ctx, "rg1", "acctin", nil)
	_, status = armErrorCode(t, err)
	assert.Equal(t, http.StatusNotFound, status)

	_, err = e.accounts("sub-1").GetProperties(ctx, "rg10", "acctout", nil)
	require.NoError(t, err)

	_, err = cc.Get(ctx, "rg10", "acctout", "data", nil)
	require.NoError(t, err)
}

// TestBlobContainersLifecycle is AZSTO-05: the ARM BlobContainers resource.
func TestBlobContainersLifecycle(t *testing.T) {
	ctx := context.Background()
	e := newNSEnv(t)
	e.mustCreateAccount("sub-1", "rg-1", "accta")
	e.mustCreateAccount("sub-1", "rg-1", "acctb")

	cc := e.containers("sub-1")

	created, err := cc.Create(ctx, "rg-1", "accta", "data", armstorage.BlobContainer{
		ContainerProperties: &armstorage.ContainerProperties{
			PublicAccess: to.Ptr(armstorage.PublicAccessNone),
			Metadata:     map[string]*string{"env": to.Ptr("dev")},
		},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "data", *created.Name)
	assert.Equal(t,
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/accta/blobServices/default/containers/data",
		*created.ID)
	assert.Equal(t, "Microsoft.Storage/storageAccounts/blobServices/containers", *created.Type)

	props := created.ContainerProperties
	require.NotNil(t, props)
	assert.Equal(t, "$account-encryption-key", *props.DefaultEncryptionScope)
	assert.False(t, *props.DenyEncryptionScopeOverride)
	assert.False(t, *props.HasImmutabilityPolicy)
	assert.False(t, *props.HasLegalHold)
	assert.Equal(t, armstorage.LeaseStateAvailable, *props.LeaseState)
	assert.Equal(t, "dev", *props.Metadata["env"])

	got, err := cc.Get(ctx, "rg-1", "accta", "data", nil)
	require.NoError(t, err)
	assert.Equal(t, *created.Etag, *got.Etag, "etag is stable across reads")

	updated, err := cc.Update(ctx, "rg-1", "accta", "data", armstorage.BlobContainer{
		ContainerProperties: &armstorage.ContainerProperties{
			PublicAccess: to.Ptr(armstorage.PublicAccessBlob),
		},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, armstorage.PublicAccessBlob, *updated.ContainerProperties.PublicAccess)
	assert.Equal(t, "dev", *updated.ContainerProperties.Metadata["env"], "PATCH keeps omitted metadata")
	assert.NotEqual(t, *created.Etag, *updated.Etag)

	// The same container name in another account is a different container.
	_, err = cc.Get(ctx, "rg-1", "acctb", "data", nil)
	code, status := armErrorCode(t, err)
	assert.Equal(t, "ContainerNotFound", code)
	assert.Equal(t, http.StatusNotFound, status)

	var names []string

	pager := cc.NewListPager("rg-1", "accta", nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		require.NoError(t, err)

		for _, c := range page.Value {
			names = append(names, *c.Name)
		}
	}

	assert.Equal(t, []string{"data"}, names)

	// The container is not a default-namespace bucket.
	buckets, err := e.cloud.BlobStorage.ListBuckets(ctx)
	require.NoError(t, err)
	assert.Empty(t, buckets)

	// A container with blobs is deleted together with them.
	require.NoError(t, e.cloud.BlobStorage.PutObject(ctx, "accta/data", "b.txt", []byte("x"), "", nil))

	_, err = cc.Delete(ctx, "rg-1", "accta", "data", nil)
	require.NoError(t, err)

	_, err = cc.Get(ctx, "rg-1", "accta", "data", nil)
	_, status = armErrorCode(t, err)
	assert.Equal(t, http.StatusNotFound, status)

	base := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/accta/blobServices/default/containers/"

	status, _ = e.raw(http.MethodDelete, base+"data?api-version=2023-05-01", "")
	assert.Equal(t, http.StatusNoContent, status, "deleting a missing container is 204")

	status, body := e.raw(http.MethodPut, base+"Bad_Name?api-version=2023-05-01", `{}`)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, body, "ContainerOperationFailure")
	assert.Contains(t, body, "contains invalid characters")

	status, body = e.raw(http.MethodPut, base+"c1?api-version=2023-05-01", `{}`)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, body, "length is not within the permissible limits")

	status, _ = e.raw(http.MethodPut, base+"again?api-version=2023-05-01", `{}`)
	assert.Equal(t, http.StatusCreated, status)

	status, _ = e.raw(http.MethodPut, base+"again?api-version=2023-05-01", `{}`)
	assert.Equal(t, http.StatusOK, status, "PUT of an existing container is an update")

	status, _ = e.raw(http.MethodPost, base+"again/lease?api-version=2023-05-01", `{}`)
	assert.Equal(t, http.StatusNotFound, status, "unmodelled container actions fail closed")

	// Containers of an account in another group are not reachable.
	_, err = cc.Get(ctx, "rg-other", "accta", "again", nil)
	_, status = armErrorCode(t, err)
	assert.Equal(t, http.StatusNotFound, status)
}

// TestLegacySnapshotKeepsWorking restores a snapshot written before accounts
// were their own resource: the account is readable from any subscription, and
// the legacy blob is still served through its old bare-host URL.
func TestLegacySnapshotKeepsWorking(t *testing.T) {
	ctx := context.Background()

	// The pre-change format: the account is the same-named container, its
	// attributes carry the resource group, and there is no "accounts" key.
	old := cloudemu.NewAzure().BlobStorage
	old.SetBucketAttributes("legacyacct", storagedriver.AccountAttributes{ResourceGroup: "r1"})
	require.NoError(t, old.CreateBucket(ctx, "legacyacct"))
	require.NoError(t, old.PutObject(ctx, "legacyacct", "c/blob.txt", []byte("legacy"), "text/plain", nil))

	raw, err := old.Snapshot(ctx, true)
	require.NoError(t, err)

	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &doc))
	delete(doc, "accounts")

	legacy, err := json.Marshal(doc)
	require.NoError(t, err)

	fresh := cloudemu.NewAzure()
	require.NoError(t, fresh.BlobStorage.Restore(ctx, legacy))

	e := newNSEnvFor(t, fresh)
	ensureRG(t, e.ts, "00000000-0000-0000-0000-0000000000ab", "r1")

	got, err := e.accounts("00000000-0000-0000-0000-0000000000ab").GetProperties(ctx, "r1", "legacyacct", nil)
	require.NoError(t, err)
	assert.Contains(t, *got.ID, "/resourceGroups/r1/")

	status, body := e.raw(http.MethodGet, "/legacyacct/c/blob.txt", "")
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "legacy", body)

	// The old same-named container is where a migrated account's data lives,
	// so deleting the account deletes it.
	_, err = e.accounts("00000000-0000-0000-0000-0000000000ab").Delete(ctx, "r1", "legacyacct", nil)
	require.NoError(t, err)

	status, _ = e.raw(http.MethodGet, "/legacyacct/c/blob.txt", "")
	assert.Equal(t, http.StatusNotFound, status)

	_, err = fresh.BlobStorage.GetObject(ctx, "legacyacct", "c/blob.txt")
	assert.Error(t, err)
}

// TestPathStyleBlobDataPlaneForARMAccount drives the azblob IP-endpoint style
// (https://host:port/{account}/...) against an ARM-created account: the
// leading segment is peeled as the account, and the containers it reaches are
// the account's ARM containers.
func TestPathStyleBlobDataPlaneForARMAccount(t *testing.T) {
	ctx := context.Background()
	e := newNSEnv(t)
	e.mustCreateAccount("sub-1", "pr1", "regacct")

	steps := []struct {
		method, path, body string
		want               int
		wantBody           string
	}{
		{http.MethodPut, "/regacct/ctr1?restype=container", "", http.StatusCreated, ""},
		{http.MethodGet, "/regacct/ctr1?restype=container", "", http.StatusOK, ""},
		{http.MethodPut, "/regacct/ctr1/b.txt", "hello", http.StatusCreated, ""},
		{http.MethodGet, "/regacct/ctr1/b.txt", "", http.StatusOK, "hello"},
		{http.MethodGet, "/regacct/?comp=list", "", http.StatusOK, "<Name>ctr1</Name>"},
		// azblob's List Containers sends no trailing slash.
		{http.MethodGet, "/regacct?comp=list", "", http.StatusOK, "<Name>ctr1</Name>"},
		{http.MethodGet, "/regacct/ctr1?restype=container&comp=list", "", http.StatusOK, "<Name>b.txt</Name>"},
		// The default namespace does not see the account's container.
		{http.MethodGet, "/ctr1?restype=container", "", http.StatusNotFound, ""},
	}

	for _, s := range steps {
		status, body := e.raw(s.method, s.path, s.body)
		require.Equal(t, s.want, status, "%s %s: %s", s.method, s.path, body)

		if s.wantBody != "" {
			assert.Contains(t, body, s.wantBody, "%s %s", s.method, s.path)
		}
	}

	cc := e.containers("sub-1")

	_, err := cc.Get(ctx, "pr1", "regacct", "ctr1", nil)
	require.NoError(t, err, "the path-style container is the account's ARM container")

	_, err = cc.Create(ctx, "pr1", "regacct", "armmade", armstorage.BlobContainer{}, nil)
	require.NoError(t, err)

	status, body := e.raw(http.MethodGet, "/regacct/?comp=list", "")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "<Name>armmade</Name>", "an ARM container is reachable path style")

	status, body = e.raw(http.MethodGet, "/?comp=list", "")
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, body, "<Name>ctr1</Name>")
}

// TestPathStylePeelRule pins the conditions under which the leading segment
// is not an account: a segment that names no account, a path that ends at the
// segment, and an account shadowed by a default container of the same name.
func TestPathStylePeelRule(t *testing.T) {
	ctx := context.Background()
	e := newNSEnv(t)
	e.mustCreateAccount("sub-1", "pr1", "peelacct")

	status, body := e.raw(http.MethodPut, "/zz9/c?restype=container", "")
	require.Equal(t, http.StatusNotFound, status, "zz9 is no account: a blob op on missing container zz9: %s", body)

	status, _ = e.raw(http.MethodPut, "/peelacct?restype=container", "")
	require.Equal(t, http.StatusCreated, status, "no trailing segment: a default-namespace container create")

	status, _ = e.raw(http.MethodPut, "/peelacct/c/b.txt", "legacy")
	require.Equal(t, http.StatusCreated, status)

	_, err := e.cloud.BlobStorage.GetObject(ctx, "peelacct", "c/b.txt")
	require.NoError(t, err, "a same-named default container wins over the account")
}

// TestAccountTailWithStorageAccountsResourceGroup: a resource group literally
// named "storageaccounts" must not be read as the resource type.
func TestAccountTailWithStorageAccountsResourceGroup(t *testing.T) {
	ctx := context.Background()
	e := newNSEnv(t)
	e.mustCreateAccount("sub-1", "storageaccounts", "rgnacct")

	status, body := e.raw(http.MethodGet, "/subscriptions/sub-1/resourceGroups/storageaccounts/providers/"+
		"Microsoft.Storage/storageAccounts/rgnacct/blobServices/default?api-version=2023-05-01", "")
	require.Equal(t, http.StatusOK, status, body)

	_, err := e.containers("sub-1").Create(ctx, "storageaccounts", "rgnacct", "data", armstorage.BlobContainer{}, nil)
	require.NoError(t, err)
}

// TestDefaultAccountNameReserved: the default account owns the default
// namespace, so an ARM account cannot take its name.
func TestDefaultAccountNameReserved(t *testing.T) {
	e := newNSEnv(t)

	_, err := e.createAccount("sub-1", "x1", storagedriver.AzureDefaultStorageAccount)
	code, status := armErrorCode(t, err)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "StorageAccountAlreadyTaken", code)
}
