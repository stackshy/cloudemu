package frontdoor_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cdn/armcdn/v2"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	sdkSub      = "00000000-0000-0000-0000-00000000fd01"
	sdkRG       = "rg-afd"
	sdkProfile  = "afd-std"
	sdkEndpoint = "web"
	sdkOG       = "og-app"
	sdkOrigin   = "app-origin"
	sdkRoute    = "default-route"
	sdkHost     = "app.example.net"
)

// fakeCred is a static-token credential for the ARM clients.
type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// cdnClients are the real armcdn clients for the whole Front Door chain.
type cdnClients struct {
	profiles  *armcdn.ProfilesClient
	endpoints *armcdn.AFDEndpointsClient
	groups    *armcdn.AFDOriginGroupsClient
	origins   *armcdn.AFDOriginsClient
	routes    *armcdn.RoutesClient
}

// newCDNClients stands up the Azure wire server over TLS (the ARM SDK requires
// it) and returns armcdn clients pointed at it.
func newCDNClients(t *testing.T) *cdnClients {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{SubscriptionID: sdkSub, FrontDoor: cloudP.FrontDoor}))
	t.Cleanup(ts.Close)

	ensureRG(t, ts, sdkSub, sdkRG)

	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: cloud.Configuration{
			ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
			Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
				cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
			},
		},
		Transport: ts.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}

	factory, err := armcdn.NewClientFactory(sdkSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("armcdn.NewClientFactory: %v", err)
	}

	return &cdnClients{
		profiles:  factory.NewProfilesClient(),
		endpoints: factory.NewAFDEndpointsClient(),
		groups:    factory.NewAFDOriginGroupsClient(),
		origins:   factory.NewAFDOriginsClient(),
		routes:    factory.NewRoutesClient(),
	}
}

func ogID() string {
	return "/subscriptions/" + sdkSub + "/resourceGroups/" + sdkRG +
		"/providers/Microsoft.Cdn/profiles/" + sdkProfile + "/originGroups/" + sdkOG
}

// wantStatus asserts err is an ARM ResponseError with the given status code.
func wantStatus(t *testing.T, op string, err error, status int) {
	t.Helper()

	var re *azcore.ResponseError
	if !errors.As(err, &re) || re.StatusCode != status {
		t.Fatalf("%s: err = %v, want HTTP %d", op, err, status)
	}
}

// TestSDKFrontDoorOriginsRoutesChain drives the full Azure Front Door Standard
// chain with the real armcdn clients: profile -> endpoint -> origin group ->
// origin -> route, then read, list, update, the in-use conflict, and teardown in
// reverse order.
func TestSDKFrontDoorOriginsRoutesChain(t *testing.T) {
	c := newCDNClients(t)
	ctx := context.Background()

	createParents(ctx, t, c)
	createOrigin(ctx, t, c)
	createRoute(ctx, t, c)
	listChildren(ctx, t, c)
	updateOriginAndRoute(ctx, t, c)

	// The origin group is still referenced by the route: Azure refuses with 409.
	_, err := c.groups.BeginDelete(ctx, sdkRG, sdkProfile, sdkOG, nil)
	wantStatus(t, "delete in-use origin group", err, http.StatusConflict)

	teardownInReverse(ctx, t, c)
}

func createParents(ctx context.Context, t *testing.T, c *cdnClients) {
	t.Helper()

	pp, err := c.profiles.BeginCreate(ctx, sdkRG, sdkProfile, armcdn.Profile{
		Location: to.Ptr("global"),
		SKU:      &armcdn.SKU{Name: to.Ptr(armcdn.SKUNameStandardAzureFrontDoor)},
	}, nil)
	if err != nil {
		t.Fatalf("profiles.BeginCreate: %v", err)
	}

	if _, err = pp.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("profile poll: %v", err)
	}

	ep, err := c.endpoints.BeginCreate(ctx, sdkRG, sdkProfile, sdkEndpoint, armcdn.AFDEndpoint{
		Location:   to.Ptr("global"),
		Properties: &armcdn.AFDEndpointProperties{EnabledState: to.Ptr(armcdn.EnabledStateEnabled)},
	}, nil)
	if err != nil {
		t.Fatalf("endpoints.BeginCreate: %v", err)
	}

	if _, err = ep.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("endpoint poll: %v", err)
	}

	gp, err := c.groups.BeginCreate(ctx, sdkRG, sdkProfile, sdkOG, armcdn.AFDOriginGroup{
		Properties: &armcdn.AFDOriginGroupProperties{
			LoadBalancingSettings: &armcdn.LoadBalancingSettingsParameters{
				SampleSize: to.Ptr[int32](4), SuccessfulSamplesRequired: to.Ptr[int32](3),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("groups.BeginCreate: %v", err)
	}

	if _, err = gp.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("origin group poll: %v", err)
	}
}

func createOrigin(ctx context.Context, t *testing.T, c *cdnClients) {
	t.Helper()

	// hostName is required.
	_, err := c.origins.BeginCreate(ctx, sdkRG, sdkProfile, sdkOG, sdkOrigin, armcdn.AFDOrigin{
		Properties: &armcdn.AFDOriginProperties{HTTPPort: to.Ptr[int32](80)},
	}, nil)
	wantStatus(t, "origin without hostName", err, http.StatusBadRequest)

	// An origin under a missing origin group is a 404.
	_, err = c.origins.BeginCreate(ctx, sdkRG, sdkProfile, "no-such-og", sdkOrigin, armcdn.AFDOrigin{
		Properties: &armcdn.AFDOriginProperties{HostName: to.Ptr(sdkHost)},
	}, nil)
	wantStatus(t, "origin under missing group", err, http.StatusNotFound)

	op, err := c.origins.BeginCreate(ctx, sdkRG, sdkProfile, sdkOG, sdkOrigin, armcdn.AFDOrigin{
		Properties: &armcdn.AFDOriginProperties{
			HostName:         to.Ptr(sdkHost),
			OriginHostHeader: to.Ptr(sdkHost),
			HTTPPort:         to.Ptr[int32](80),
			HTTPSPort:        to.Ptr[int32](443),
			Priority:         to.Ptr[int32](1),
			Weight:           to.Ptr[int32](1000),
			EnabledState:     to.Ptr(armcdn.EnabledStateEnabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("origins.BeginCreate: %v", err)
	}

	created, err := op.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("origin poll: %v", err)
	}

	p := created.Properties
	if p == nil || val(p.HostName, "") != sdkHost || val(p.Weight, 0) != 1000 ||
		val(p.OriginGroupName, "") != sdkOG ||
		val(p.ProvisioningState, "") != armcdn.AfdProvisioningStateSucceeded {
		t.Fatalf("created origin = %+v", p)
	}

	got, err := c.origins.Get(ctx, sdkRG, sdkProfile, sdkOG, sdkOrigin, nil)
	if err != nil {
		t.Fatalf("origins.Get: %v", err)
	}

	if val(got.Properties.OriginHostHeader, "") != sdkHost || val(got.Properties.HTTPSPort, 0) != 443 {
		t.Fatalf("got origin = %+v", got.Properties)
	}
}

func createRoute(ctx context.Context, t *testing.T, c *cdnClients) {
	t.Helper()

	// A route must reference an existing origin group in the same profile.
	missing := "/subscriptions/" + sdkSub + "/resourceGroups/" + sdkRG +
		"/providers/Microsoft.Cdn/profiles/" + sdkProfile + "/originGroups/no-such-og"
	_, err := c.routes.BeginCreate(ctx, sdkRG, sdkProfile, sdkEndpoint, sdkRoute, armcdn.Route{
		Properties: &armcdn.RouteProperties{OriginGroup: &armcdn.ResourceReference{ID: to.Ptr(missing)}},
	}, nil)
	wantStatus(t, "route to missing origin group", err, http.StatusBadRequest)

	rp, err := c.routes.BeginCreate(ctx, sdkRG, sdkProfile, sdkEndpoint, sdkRoute, armcdn.Route{
		Properties: &armcdn.RouteProperties{
			OriginGroup: &armcdn.ResourceReference{ID: to.Ptr(ogID())},
			SupportedProtocols: []*armcdn.AFDEndpointProtocols{
				to.Ptr(armcdn.AFDEndpointProtocolsHTTP), to.Ptr(armcdn.AFDEndpointProtocolsHTTPS),
			},
			PatternsToMatch:     []*string{to.Ptr("/*")},
			ForwardingProtocol:  to.Ptr(armcdn.ForwardingProtocolHTTPSOnly),
			LinkToDefaultDomain: to.Ptr(armcdn.LinkToDefaultDomainEnabled),
			HTTPSRedirect:       to.Ptr(armcdn.HTTPSRedirectEnabled),
		},
	}, nil)
	if err != nil {
		t.Fatalf("routes.BeginCreate: %v", err)
	}

	if _, err = rp.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("route poll: %v", err)
	}

	got, err := c.routes.Get(ctx, sdkRG, sdkProfile, sdkEndpoint, sdkRoute, nil)
	if err != nil {
		t.Fatalf("routes.Get: %v", err)
	}

	p := got.Properties
	if p == nil || p.OriginGroup == nil || val(p.OriginGroup.ID, "") != ogID() ||
		val(p.ForwardingProtocol, "") != armcdn.ForwardingProtocolHTTPSOnly ||
		val(p.HTTPSRedirect, "") != armcdn.HTTPSRedirectEnabled ||
		val(p.LinkToDefaultDomain, "") != armcdn.LinkToDefaultDomainEnabled ||
		len(p.SupportedProtocols) != 2 || len(p.PatternsToMatch) != 1 || *p.PatternsToMatch[0] != "/*" ||
		val(p.EndpointName, "") != sdkEndpoint {
		t.Fatalf("got route = %+v", p)
	}
}

func listChildren(ctx context.Context, t *testing.T, c *cdnClients) {
	t.Helper()

	var origins int

	op := c.origins.NewListByOriginGroupPager(sdkRG, sdkProfile, sdkOG, nil)
	for op.More() {
		page, err := op.NextPage(ctx)
		if err != nil {
			t.Fatalf("origins list: %v", err)
		}

		origins += len(page.Value)
	}

	var routes int

	rp := c.routes.NewListByEndpointPager(sdkRG, sdkProfile, sdkEndpoint, nil)
	for rp.More() {
		page, err := rp.NextPage(ctx)
		if err != nil {
			t.Fatalf("routes list: %v", err)
		}

		routes += len(page.Value)
	}

	if origins != 1 || routes != 1 {
		t.Fatalf("listed %d origins and %d routes, want 1 and 1", origins, routes)
	}
}

func updateOriginAndRoute(ctx context.Context, t *testing.T, c *cdnClients) {
	t.Helper()

	up, err := c.origins.BeginUpdate(ctx, sdkRG, sdkProfile, sdkOG, sdkOrigin, armcdn.AFDOriginUpdateParameters{
		Properties: &armcdn.AFDOriginUpdatePropertiesParameters{Weight: to.Ptr[int32](500)},
	}, nil)
	if err != nil {
		t.Fatalf("origins.BeginUpdate: %v", err)
	}

	updated, err := up.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("origin update poll: %v", err)
	}

	// PATCH merges: weight changes, hostName is kept.
	if val(updated.Properties.Weight, 0) != 500 || val(updated.Properties.HostName, "") != sdkHost {
		t.Fatalf("updated origin = %+v", updated.Properties)
	}

	// An out-of-range weight is refused.
	_, err = c.origins.BeginUpdate(ctx, sdkRG, sdkProfile, sdkOG, sdkOrigin, armcdn.AFDOriginUpdateParameters{
		Properties: &armcdn.AFDOriginUpdatePropertiesParameters{Weight: to.Ptr[int32](5000)},
	}, nil)
	wantStatus(t, "origin weight out of range", err, http.StatusBadRequest)

	ur, err := c.routes.BeginUpdate(ctx, sdkRG, sdkProfile, sdkEndpoint, sdkRoute, armcdn.RouteUpdateParameters{
		Properties: &armcdn.RouteUpdatePropertiesParameters{HTTPSRedirect: to.Ptr(armcdn.HTTPSRedirectDisabled)},
	}, nil)
	if err != nil {
		t.Fatalf("routes.BeginUpdate: %v", err)
	}

	route, err := ur.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("route update poll: %v", err)
	}

	if val(route.Properties.HTTPSRedirect, "") != armcdn.HTTPSRedirectDisabled ||
		route.Properties.OriginGroup == nil || val(route.Properties.OriginGroup.ID, "") != ogID() {
		t.Fatalf("updated route = %+v", route.Properties)
	}
}

func teardownInReverse(ctx context.Context, t *testing.T, c *cdnClients) {
	t.Helper()

	rp, err := c.routes.BeginDelete(ctx, sdkRG, sdkProfile, sdkEndpoint, sdkRoute, nil)
	if err != nil {
		t.Fatalf("routes.BeginDelete: %v", err)
	}

	if _, err = rp.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("route delete poll: %v", err)
	}

	op, err := c.origins.BeginDelete(ctx, sdkRG, sdkProfile, sdkOG, sdkOrigin, nil)
	if err != nil {
		t.Fatalf("origins.BeginDelete: %v", err)
	}

	if _, err = op.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("origin delete poll: %v", err)
	}

	_, err = c.origins.Get(ctx, sdkRG, sdkProfile, sdkOG, sdkOrigin, nil)
	wantStatus(t, "get deleted origin", err, http.StatusNotFound)

	// With the route gone, the origin group is no longer in use.
	gp, err := c.groups.BeginDelete(ctx, sdkRG, sdkProfile, sdkOG, nil)
	if err != nil {
		t.Fatalf("groups.BeginDelete after route removal: %v", err)
	}

	if _, err = gp.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("origin group delete poll: %v", err)
	}

	pp, err := c.profiles.BeginDelete(ctx, sdkRG, sdkProfile, nil)
	if err != nil {
		t.Fatalf("profiles.BeginDelete: %v", err)
	}

	if _, err = pp.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("profile delete poll: %v", err)
	}
}

// TestSDKFrontDoorProfileDeleteCascades proves deleting a profile removes the
// endpoint, origin group, origin and route under it, so recreating the profile
// starts empty.
func TestSDKFrontDoorProfileDeleteCascades(t *testing.T) {
	c := newCDNClients(t)
	ctx := context.Background()

	createParents(ctx, t, c)
	createOrigin(ctx, t, c)
	createRoute(ctx, t, c)

	pp, err := c.profiles.BeginDelete(ctx, sdkRG, sdkProfile, nil)
	if err != nil {
		t.Fatalf("profiles.BeginDelete: %v", err)
	}

	if _, err = pp.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("profile delete poll: %v", err)
	}

	createParents(ctx, t, c)

	_, err = c.origins.Get(ctx, sdkRG, sdkProfile, sdkOG, sdkOrigin, nil)
	wantStatus(t, "origin after profile cascade", err, http.StatusNotFound)

	_, err = c.routes.Get(ctx, sdkRG, sdkProfile, sdkEndpoint, sdkRoute, nil)
	wantStatus(t, "route after profile cascade", err, http.StatusNotFound)
}

// val dereferences p, or returns def when p is nil.
func val[T any](p *T, def T) T {
	if p == nil {
		return def
	}

	return *p
}
