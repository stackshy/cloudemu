package bastion_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	testRG     = "rg-1"
	testSub    = "sub-1"
	hostName   = "bastion-1"
	provider   = "Microsoft.Network"
	subnetID   = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vnet/subnets/AzureBastionSubnet"
	publicIPID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/publicIPAddresses/pip"
	ipConfigNm = "ipconfig1"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// newServer stands up the full Azure wire server backed by a fresh in-memory
// provider. The VNet, LB, AppGateway and Firewall handlers are wired too so we
// prove the bastionHosts resource type is not shadowed by another
// Microsoft.Network handler.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		Bastion:    cloudP.Bastion,
		Network:    cloudP.VNet,
		LB:         cloudP.LB,
		AppGateway: cloudP.AppGateway,
		Firewall:   cloudP.Firewall,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func clientOpts(ts *httptest.Server) *arm.ClientOptions {
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}
}

func bastionClient(t *testing.T, ts *httptest.Server) *armnetwork.BastionHostsClient {
	t.Helper()

	c, err := armnetwork.NewBastionHostsClient(testSub, fakeCred{}, clientOpts(ts))
	if err != nil {
		t.Fatalf("NewBastionHostsClient: %v", err)
	}

	return c
}

func hostID() string {
	return "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/" + provider + "/bastionHosts/" + hostName
}

// sampleHost builds a Standard bastion with one ipConfiguration, an explicit
// scaleUnits, an explicit-false disableCopyPaste and an explicit-true
// enableTunneling.
func sampleHost() armnetwork.BastionHost {
	return armnetwork.BastionHost{
		Location: to.Ptr("eastus"),
		SKU:      &armnetwork.SKU{Name: to.Ptr(armnetwork.BastionHostSKUNameStandard)},
		Properties: &armnetwork.BastionHostPropertiesFormat{
			ScaleUnits:       to.Ptr(int32(3)),
			DisableCopyPaste: to.Ptr(false),
			EnableTunneling:  to.Ptr(true),
			IPConfigurations: []*armnetwork.BastionHostIPConfiguration{{
				Name: to.Ptr(ipConfigNm),
				Properties: &armnetwork.BastionHostIPConfigurationPropertiesFormat{
					Subnet:          &armnetwork.SubResource{ID: to.Ptr(subnetID)},
					PublicIPAddress: &armnetwork.SubResource{ID: to.Ptr(publicIPID)},
				},
			}},
		},
	}
}

func createHost(
	t *testing.T, c *armnetwork.BastionHostsClient, name string, body armnetwork.BastionHost,
) armnetwork.BastionHost {
	t.Helper()

	ctx := context.Background()

	poller, err := c.BeginCreateOrUpdate(ctx, testRG, name, body, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	return resp.BastionHost
}

// TestBastionRoundTrip proves the modeled surface (top-level sku, computed
// dnsName, scaleUnits, ipConfigurations with stamped id + Dynamic allocation,
// provisioningState, the explicit-false/true feature toggles) survives
// create → get.
func TestBastionRoundTrip(t *testing.T) {
	ts := newServer(t)
	c := bastionClient(t, ts)

	created := createHost(t, c, hostName, sampleHost())
	assertHost(t, &created)

	got, err := c.Get(context.Background(), testRG, hostName, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	assertHost(t, &got.BastionHost)
}

//nolint:gocyclo,cyclop // a round-trip assertion legitimately checks many fields.
func assertHost(t *testing.T, h *armnetwork.BastionHost) {
	t.Helper()

	if got := deref(h.ID); got != hostID() {
		t.Errorf("id = %q, want %q", got, hostID())
	}

	if got := deref(h.Name); got != hostName {
		t.Errorf("name = %q, want %q", got, hostName)
	}

	if h.SKU == nil || h.SKU.Name == nil || string(*h.SKU.Name) != string(armnetwork.BastionHostSKUNameStandard) {
		t.Errorf("top-level sku = %v, want Standard", h.SKU)
	}

	if h.Properties == nil {
		t.Fatal("properties nil")
	}

	if deref(h.Properties.DNSName) == "" {
		t.Error("dnsName not computed")
	}

	if h.Properties.ScaleUnits == nil || *h.Properties.ScaleUnits != 3 {
		t.Errorf("scaleUnits = %v, want 3", h.Properties.ScaleUnits)
	}

	if h.Properties.DisableCopyPaste == nil || *h.Properties.DisableCopyPaste {
		t.Errorf("disableCopyPaste = %v, want explicit false", h.Properties.DisableCopyPaste)
	}

	if h.Properties.EnableTunneling == nil || !*h.Properties.EnableTunneling {
		t.Errorf("enableTunneling = %v, want true", h.Properties.EnableTunneling)
	}

	// Omitted toggles must still surface as explicit false, not nil.
	if h.Properties.EnableFileCopy == nil || h.Properties.EnableIPConnect == nil || h.Properties.EnableShareableLink == nil {
		t.Error("omitted feature toggles must surface as explicit false, got nil")
	}

	if h.Properties.ProvisioningState == nil || *h.Properties.ProvisioningState != armnetwork.ProvisioningStateSucceeded {
		t.Errorf("provisioningState = %v, want Succeeded", h.Properties.ProvisioningState)
	}

	assertIPConfig(t, h)
}

func assertIPConfig(t *testing.T, h *armnetwork.BastionHost) {
	t.Helper()

	if len(h.Properties.IPConfigurations) != 1 {
		t.Fatalf("ipConfigurations len = %d, want 1", len(h.Properties.IPConfigurations))
	}

	cfg := h.Properties.IPConfigurations[0]
	if deref(cfg.Name) != ipConfigNm {
		t.Errorf("ipconfig name = %q, want %q", deref(cfg.Name), ipConfigNm)
	}

	if deref(cfg.ID) != hostID()+"/bastionHostIpConfigurations/"+ipConfigNm {
		t.Errorf("ipconfig id = %q, unexpected", deref(cfg.ID))
	}

	if cfg.Properties == nil || cfg.Properties.Subnet == nil || cfg.Properties.PublicIPAddress == nil {
		t.Fatal("ipconfig subnet/publicIPAddress not round-tripped")
	}

	if cfg.Properties.PrivateIPAllocationMethod == nil ||
		*cfg.Properties.PrivateIPAllocationMethod != armnetwork.IPAllocationMethodDynamic {
		t.Errorf("privateIPAllocationMethod = %v, want Dynamic", cfg.Properties.PrivateIPAllocationMethod)
	}

	if cfg.Properties.ProvisioningState == nil || *cfg.Properties.ProvisioningState != armnetwork.ProvisioningStateSucceeded {
		t.Errorf("ipconfig provisioningState = %v, want Succeeded", cfg.Properties.ProvisioningState)
	}
}

// TestBastionDNSNameStable proves the computed dnsName is generated once and is
// identical across repeated GETs and an intervening update — a per-GET regen
// would drift Terraform's computed dns_name.
func TestBastionDNSNameStable(t *testing.T) {
	ts := newServer(t)
	c := bastionClient(t, ts)
	ctx := context.Background()

	created := createHost(t, c, hostName, sampleHost())
	dns := deref(created.Properties.DNSName)

	if dns == "" {
		t.Fatal("dnsName empty on create")
	}

	for i := 0; i < 3; i++ {
		got, err := c.Get(ctx, testRG, hostName, nil)
		if err != nil {
			t.Fatalf("Get #%d: %v", i, err)
		}

		if deref(got.Properties.DNSName) != dns {
			t.Fatalf("dnsName drifted on GET #%d: %q != %q", i, deref(got.Properties.DNSName), dns)
		}
	}

	// An update (scaleUnits change) must preserve the original dnsName.
	body := sampleHost()
	body.Properties.ScaleUnits = to.Ptr(int32(4))
	updated := createHost(t, c, hostName, body)

	if deref(updated.Properties.DNSName) != dns {
		t.Errorf("dnsName drifted on update: %q != %q", deref(updated.Properties.DNSName), dns)
	}
}

// TestBastionDefaults proves an omitted sku defaults to Standard and an omitted
// scaleUnits defaults to 2, while every omitted feature toggle surfaces as
// explicit false.
func TestBastionDefaults(t *testing.T) {
	ts := newServer(t)
	c := bastionClient(t, ts)

	created := createHost(t, c, "bastion-min", armnetwork.BastionHost{
		Location:   to.Ptr("eastus"),
		Properties: &armnetwork.BastionHostPropertiesFormat{},
	})

	if created.SKU == nil || created.SKU.Name == nil ||
		string(*created.SKU.Name) != string(armnetwork.BastionHostSKUNameStandard) {
		t.Errorf("default sku = %v, want Standard", created.SKU)
	}

	if created.Properties.ScaleUnits == nil || *created.Properties.ScaleUnits != 2 {
		t.Errorf("default scaleUnits = %v, want 2", created.Properties.ScaleUnits)
	}

	if created.Properties.DisableCopyPaste == nil || *created.Properties.DisableCopyPaste {
		t.Errorf("disableCopyPaste = %v, want explicit false", created.Properties.DisableCopyPaste)
	}

	if created.Properties.EnableTunneling == nil || *created.Properties.EnableTunneling {
		t.Errorf("enableTunneling = %v, want explicit false", created.Properties.EnableTunneling)
	}
}

// TestBastionUpdateTagsReplaces proves PATCH UpdateTags replaces the tag set
// wholesale while preserving the host's other modeled state (sku, dnsName).
func TestBastionUpdateTagsReplaces(t *testing.T) {
	ts := newServer(t)
	c := bastionClient(t, ts)
	ctx := context.Background()

	body := sampleHost()
	body.Tags = map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("net")}
	created := createHost(t, c, hostName, body)
	dns := deref(created.Properties.DNSName)

	poller, err := c.BeginUpdateTags(ctx, testRG, hostName,
		armnetwork.TagsObject{Tags: map[string]*string{"env": to.Ptr("dev")}}, nil)
	if err != nil {
		t.Fatalf("BeginUpdateTags: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("UpdateTags PollUntilDone: %v", err)
	}

	got, err := c.Get(ctx, testRG, hostName, nil)
	if err != nil {
		t.Fatalf("Get after UpdateTags: %v", err)
	}

	if len(got.Tags) != 1 || deref(got.Tags["env"]) != "dev" {
		t.Errorf("tags = %v, want only {env:dev}", got.Tags)
	}

	if got.SKU == nil || got.SKU.Name == nil {
		t.Error("UpdateTags dropped sku")
	}

	if deref(got.Properties.DNSName) != dns {
		t.Errorf("UpdateTags drifted dnsName: %q != %q", deref(got.Properties.DNSName), dns)
	}
}

// TestBastionDelete proves delete removes the host (subsequent Get 404s).
func TestBastionDelete(t *testing.T) {
	ts := newServer(t)
	c := bastionClient(t, ts)
	ctx := context.Background()

	createHost(t, c, hostName, sampleHost())

	poller, err := c.BeginDelete(ctx, testRG, hostName, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	if _, err = c.Get(ctx, testRG, hostName, nil); err == nil {
		t.Fatal("Get after delete: want error, got nil")
	}
}

// TestBastionListDeterministic proves ListByResourceGroup returns hosts in a
// stable order.
func TestBastionListDeterministic(t *testing.T) {
	ts := newServer(t)
	c := bastionClient(t, ts)
	ctx := context.Background()

	for _, name := range []string{"bastion-c", "bastion-a", "bastion-b"} {
		createHost(t, c, name, armnetwork.BastionHost{Location: to.Ptr("eastus")})
	}

	pager := c.NewListByResourceGroupPager(testRG, nil)

	var names []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NextPage: %v", err)
		}

		for _, h := range page.Value {
			names = append(names, deref(h.Name))
		}
	}

	want := []string{"bastion-a", "bastion-b", "bastion-c"}
	if len(names) != len(want) {
		t.Fatalf("list = %v, want %v", names, want)
	}

	for i := range want {
		if names[i] != want[i] {
			t.Errorf("list[%d] = %q, want %q (list=%v)", i, names[i], want[i], names)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
