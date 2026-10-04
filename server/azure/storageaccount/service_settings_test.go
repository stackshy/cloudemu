package storageaccount_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const settingsAcct = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/stset1"

type settingsServer struct {
	t  *testing.T
	ts *httptest.Server
}

func newSettingsServer(t *testing.T) *settingsServer {
	t.Helper()

	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{BlobStorage: cloudemu.NewAzure().BlobStorage}))
	t.Cleanup(ts.Close)

	ensureRG(t, ts, "sub-1", "rg-1")

	s := &settingsServer{t: t, ts: ts}
	if code, out := s.do(http.MethodPut, settingsAcct, `{"location":"westus","kind":"StorageV2",`+
		`"sku":{"name":"Standard_GRS"},"tags":{"env":"prod"},"properties":{}}`); code != http.StatusOK {
		t.Fatalf("create account: %d %s", code, out)
	}

	return s
}

func (s *settingsServer) do(method, path, body string) (int, []byte) {
	s.t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method,
		s.ts.URL+path+"?api-version=2023-05-01", strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.ts.Client().Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()

	out, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, out
}

func (s *settingsServer) armOptions() *arm.ClientOptions {
	return &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: s.ts.URL, Audience: "https://management.azure.com"},
		}},
		Transport: s.ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}
}

// TestAccountChildPathsNeverReachTheAccount is the AZSTO-02 regression: a
// write to a service settings, management policy or unknown child path used
// to fall through to the account handler and reset its location, sku and tags,
// and a DELETE removed the account.
func TestAccountChildPathsNeverReachTheAccount(t *testing.T) {
	s := newSettingsServer(t)
	_, before := s.do(http.MethodGet, settingsAcct, "")

	probes := []struct {
		method, suffix, body string
		want                 int
		contains             string
	}{
		{http.MethodGet, "/fileServices/default", "", http.StatusOK, `"shareDeleteRetentionPolicy":{"days":7,"enabled":true}`},
		{http.MethodGet, "/queueServices/default", "", http.StatusOK, `"type":"Microsoft.Storage/storageAccounts/queueServices"`},
		{http.MethodGet, "/tableServices", "", http.StatusOK, `"value":[{`},
		{http.MethodPut, "/queueServices/default", `{"location":"centralus","properties":{"cors":{"corsRules":[]}}}`,
			http.StatusOK, `"name":"default"`},
		{http.MethodDelete, "/fileServices/default", "", http.StatusMethodNotAllowed, ""},
		{http.MethodGet, "/fileServices/other", "", http.StatusNotFound, "ResourceNotFound"},
		{http.MethodGet, "/fileServices/default/shares", "", http.StatusOK, `"value":[]`},
		{http.MethodGet, "/queueServices/default/queues/q1", "", http.StatusNotFound, "ResourceNotFound"},
		{http.MethodPut, "/tableServices/default/tables/t1", "{}", http.StatusNotImplemented, "NotImplemented"},
		{http.MethodGet, "/fileServices/default/queues", "", http.StatusNotFound, "InvalidResourceType"},
		{http.MethodGet, "/managementPolicies/default", "", http.StatusNotFound, "ManagementPolicyNotFound"},
		{http.MethodDelete, "/managementPolicies/default", "", http.StatusNoContent, ""},
		{http.MethodGet, "/privateEndpointConnections", "", http.StatusOK, `"value":[]`},
		{http.MethodPut, "/encryptionScopes/e1", `{"location":"centralus"}`, http.StatusNotImplemented, ""},
		{http.MethodDelete, "/inventoryPolicies/default", "", http.StatusNotImplemented, ""},
		{http.MethodPost, "/failover", "", http.StatusNotImplemented, ""},
		{http.MethodPut, "/zzunknown", `{"location":"centralus"}`, http.StatusNotFound, "InvalidResourceType"},
		{http.MethodDelete, "/zzunknown/x", "", http.StatusNotFound, "InvalidResourceType"},
		{http.MethodPut, "/providers/Microsoft.Security/advancedThreatProtectionSettings/current",
			`{"location":"centralus"}`, http.StatusNotImplemented, "extension resource"},
	}

	for _, p := range probes {
		code, out := s.do(p.method, settingsAcct+p.suffix, p.body)
		if code != p.want || !bytes.Contains(out, []byte(p.contains)) {
			t.Errorf("%s %s: %d %s, want %d containing %q", p.method, p.suffix, code, out, p.want, p.contains)
		}
	}

	if code, after := s.do(http.MethodGet, settingsAcct, ""); code != http.StatusOK || !bytes.Equal(before, after) {
		t.Errorf("account changed: %d\nbefore %s\nafter  %s", code, before, after)
	}
}

func TestServiceSettingsRoundTripVerbatim(t *testing.T) {
	s := newSettingsServer(t)

	body := `{"properties":{"cors":{"corsRules":[{"allowedOrigins":["https://a.example"],` +
		`"allowedMethods":["GET"],"allowedHeaders":["*"],"exposedHeaders":["*"],"maxAgeInSeconds":60}]},` +
		`"shareDeleteRetentionPolicy":{"enabled":false},"protocolSettings":{"smb":{"versions":"SMB3.1.1;"}}}}`

	if code, out := s.do(http.MethodPut, settingsAcct+"/fileServices/default", body); code != http.StatusOK {
		t.Fatalf("PUT fileServices: %d %s", code, out)
	}

	_, out := s.do(http.MethodGet, settingsAcct+"/fileServices/default", "")

	var got struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}

	require.NoError(t, json.Unmarshal(out, &got))
	assert.JSONEq(t, `{"enabled":false}`, string(got.Properties["shareDeleteRetentionPolicy"]))
	assert.JSONEq(t, `{"smb":{"versions":"SMB3.1.1;"}}`, string(got.Properties["protocolSettings"]))
	assert.Contains(t, string(got.Properties["cors"]), "https://a.example")

	// The queue service is a separate document and keeps its defaults.
	_, q := s.do(http.MethodGet, settingsAcct+"/queueServices/default", "")
	assert.Contains(t, string(q), `"cors":{"corsRules":[]}`)
	assert.NotContains(t, string(q), "a.example")
}

func TestSDKManagementPolicyLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newSettingsServer(t)

	client, err := armstorage.NewManagementPoliciesClient("sub-1", fakeCred{}, s.armOptions())
	require.NoError(t, err)

	rule := func(days float32) armstorage.ManagementPolicy {
		return armstorage.ManagementPolicy{Properties: &armstorage.ManagementPolicyProperties{
			Policy: &armstorage.ManagementPolicySchema{Rules: []*armstorage.ManagementPolicyRule{{
				Name: to.Ptr("r1"), Enabled: to.Ptr(true), Type: to.Ptr(armstorage.RuleTypeLifecycle),
				Definition: &armstorage.ManagementPolicyDefinition{
					Filters: &armstorage.ManagementPolicyFilter{BlobTypes: []*string{to.Ptr("blockBlob")}},
					Actions: &armstorage.ManagementPolicyAction{BaseBlob: &armstorage.ManagementPolicyBaseBlob{
						Delete: &armstorage.DateAfterModification{DaysAfterModificationGreaterThan: to.Ptr(days)},
					}},
				},
			}}},
		}}
	}

	_, err = client.Get(ctx, "rg-1", "stset1", armstorage.ManagementPolicyNameDefault, nil)
	require.Error(t, err, "GET before PUT must be 404")

	_, err = client.CreateOrUpdate(ctx, "rg-1", "stset1", armstorage.ManagementPolicyNameDefault, rule(30), nil)
	require.NoError(t, err)

	_, err = client.CreateOrUpdate(ctx, "rg-1", "stset1", armstorage.ManagementPolicyNameDefault, rule(45), nil)
	require.NoError(t, err)

	got, err := client.Get(ctx, "rg-1", "stset1", armstorage.ManagementPolicyNameDefault, nil)
	require.NoError(t, err)
	require.NotNil(t, got.Properties.LastModifiedTime)
	require.Len(t, got.Properties.Policy.Rules, 1)
	assert.InDelta(t, 45, *got.Properties.Policy.Rules[0].Definition.Actions.BaseBlob.Delete.DaysAfterModificationGreaterThan, 0)

	_, err = client.Delete(ctx, "rg-1", "stset1", armstorage.ManagementPolicyNameDefault, nil)
	require.NoError(t, err)

	_, err = client.Get(ctx, "rg-1", "stset1", armstorage.ManagementPolicyNameDefault, nil)
	require.Error(t, err, "GET after DELETE must be 404")
}

func TestSDKFileServicesDefaults(t *testing.T) {
	s := newSettingsServer(t)

	client, err := armstorage.NewFileServicesClient("sub-1", fakeCred{}, s.armOptions())
	require.NoError(t, err)

	got, err := client.GetServiceProperties(context.Background(), "rg-1", "stset1", nil)
	require.NoError(t, err)
	require.NotNil(t, got.FileServiceProperties.FileServiceProperties)
	assert.Equal(t, "default", *got.Name)
	assert.True(t, *got.FileServiceProperties.FileServiceProperties.ShareDeleteRetentionPolicy.Enabled)
	assert.Equal(t, int32(7), *got.FileServiceProperties.FileServiceProperties.ShareDeleteRetentionPolicy.Days)
}

// TestAccountDeletePurgesSettings checks that a recreated account starts from
// defaults, not the deleted account's settings.
func TestAccountDeletePurgesSettings(t *testing.T) {
	s := newSettingsServer(t)

	s.do(http.MethodPut, settingsAcct+"/managementPolicies/default", `{"properties":{"policy":{"rules":[]}}}`)
	s.do(http.MethodPut, settingsAcct+"/queueServices/default", `{"properties":{"cors":{"corsRules":[{"x":1}]}}}`)

	if code, _ := s.do(http.MethodDelete, settingsAcct, ""); code != http.StatusOK {
		t.Fatalf("delete account: %d", code)
	}

	s.do(http.MethodPut, settingsAcct, `{"location":"westus","kind":"StorageV2","sku":{"name":"Standard_LRS"}}`)

	if code, _ := s.do(http.MethodGet, settingsAcct+"/managementPolicies/default", ""); code != http.StatusNotFound {
		t.Errorf("management policy survived account delete: %d", code)
	}

	if _, q := s.do(http.MethodGet, settingsAcct+"/queueServices/default", ""); !bytes.Contains(q, []byte(`"corsRules":[]`)) {
		t.Errorf("queue settings survived account delete: %s", q)
	}
}
