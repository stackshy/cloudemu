package vcn_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	vcnprovider "github.com/stackshy/cloudemu/v2/providers/oci/vcn"
	ocivcn "github.com/stackshy/cloudemu/v2/server/oci/vcn"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

const (
	compartment      = "ocid1.compartment.oc1..aaaaaaaatest"
	otherCompartment = "ocid1.compartment.oc1..aaaaaaaaother"
	vcnCIDR          = "10.0.0.0/16"
	subnetCIDR       = "10.0.1.0/24"
)

// Compile-time check that the OCI VCN mock carries the optional capabilities
// the handler discovers by type assertion.
var _ ocivcn.Extras = (*vcnprovider.Mock)(nil)

type fixture struct {
	t       *testing.T
	handler *ocivcn.Handler
	mock    *vcnprovider.Mock
	work    *workrequest.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	opts := config.NewOptions(config.WithRegion("us-ashburn-1"), config.WithCompartmentID(compartment))
	mock := vcnprovider.New(opts)
	work := workrequest.New(opts)

	return &fixture{t: t, handler: ocivcn.New(mock, work), mock: mock, work: work}
}

// do sends a request through the handler and returns the recorder.
func (f *fixture) do(method, target string, body any) *httptest.ResponseRecorder {
	f.t.Helper()

	var reader *bytes.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(f.t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}

	r := httptest.NewRequest(method, target, reader)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)

	return w
}

// decode reads a JSON response body into a map.
func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	out := map[string]any{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	return out
}

// decodeList reads a JSON array response body.
func decodeList(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()

	var out []map[string]any

	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))

	return out
}

// newVCN creates a VCN over the wire and returns its OCID.
func (f *fixture) newVCN() string {
	f.t.Helper()

	w := f.do(http.MethodPost, "/20160918/vcns", map[string]any{
		"compartmentId": compartment,
		"cidrBlock":     vcnCIDR,
		"displayName":   "test-vcn",
		"dnsLabel":      "testvcn",
	})
	require.Equal(f.t, http.StatusOK, w.Code, w.Body.String())

	id, _ := decode(f.t, w)["id"].(string)

	return id
}

// newSubnet creates a subnet over the wire and returns its OCID.
func (f *fixture) newSubnet(vcnID string) string {
	f.t.Helper()

	w := f.do(http.MethodPost, "/20160918/subnets", map[string]any{
		"compartmentId": compartment,
		"vcnId":         vcnID,
		"cidrBlock":     subnetCIDR,
		"displayName":   "test-subnet",
	})
	require.Equal(f.t, http.StatusOK, w.Code, w.Body.String())

	id, _ := decode(f.t, w)["id"].(string)

	return id
}

// primaryPrivateIP returns the OCID of the private IP a VNIC was created with.
func (f *fixture) primaryPrivateIP(vnicID string) string {
	f.t.Helper()

	w := f.do(http.MethodGet, "/20160918/privateIps?vnicId="+vnicID, nil)
	require.Equal(f.t, http.StatusOK, w.Code, w.Body.String())

	ips := decodeList(f.t, w)
	require.NotEmpty(f.t, ips)

	id, _ := ips[0]["id"].(string)

	return id
}

func TestMatches(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{name: "vcns collection", method: http.MethodGet, path: "/20160918/vcns", want: true},
		{name: "one vcn", method: http.MethodGet, path: "/20160918/vcns/ocid1.vcn.oc1.iad.a", want: true},
		{name: "vcn action", method: http.MethodPost,
			path: "/20160918/vcns/ocid1.vcn.oc1.iad.a/actions/changeCompartment", want: true},
		{name: "subnets", method: http.MethodPost, path: "/20160918/subnets", want: true},
		{name: "network security groups", method: http.MethodGet, path: "/20160918/networkSecurityGroups", want: true},
		{name: "nsg security rules", method: http.MethodGet,
			path: "/20160918/networkSecurityGroups/ocid1.networksecuritygroup.oc1.iad.a/securityRules", want: true},
		{name: "security lists", method: http.MethodGet, path: "/20160918/securityLists", want: true},
		{name: "route tables", method: http.MethodGet, path: "/20160918/routeTables", want: true},
		{name: "internet gateways", method: http.MethodGet, path: "/20160918/internetGateways", want: true},
		{name: "nat gateways", method: http.MethodGet, path: "/20160918/natGateways", want: true},
		{name: "service gateways", method: http.MethodGet, path: "/20160918/serviceGateways", want: true},
		{name: "dhcp options", method: http.MethodGet, path: "/20160918/dhcps", want: true},
		{name: "vnics", method: http.MethodGet, path: "/20160918/vnics/ocid1.vnic.oc1.iad.a", want: true},
		{name: "private ips", method: http.MethodGet, path: "/20160918/privateIps", want: true},
		{name: "public ips", method: http.MethodGet, path: "/20160918/publicIps", want: true},

		{name: "compute instances", method: http.MethodGet, path: "/20160918/instances", want: false},
		{name: "compute vnic attachments", method: http.MethodGet, path: "/20160918/vnicAttachments", want: false},
		{name: "block volumes", method: http.MethodGet, path: "/20160918/volumes", want: false},
		{name: "work requests", method: http.MethodGet, path: "/20160918/workRequests/ocid1.workrequest.oc1.iad.a",
			want: false},
		{name: "another API version", method: http.MethodGet, path: "/20180828/vcns", want: false},
		{name: "object storage", method: http.MethodGet, path: "/n/tenancy/b/bucket/o/object", want: false},
		{name: "root", method: http.MethodGet, path: "/", want: false},
		{name: "version only", method: http.MethodGet, path: "/20160918", want: false},
		{name: "too deep", method: http.MethodGet, path: "/20160918/vcns/a/b/c/d", want: false},
	}

	h := ocivcn.New(vcnprovider.New(config.NewOptions()), nil)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			assert.Equal(t, tc.want, h.Matches(r))
		})
	}
}

func TestVCNOperations(t *testing.T) {
	f := newFixture(t)
	id := f.newVCN()

	t.Run("create returns the OCI shape", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/vcns/"+id, nil)
		require.Equal(t, http.StatusOK, w.Code)

		body := decode(t, w)
		assert.Equal(t, compartment, body["compartmentId"])
		assert.Equal(t, vcnCIDR, body["cidrBlock"])
		assert.Equal(t, "test-vcn", body["displayName"])
		assert.Equal(t, "testvcn.oraclevcn.com", body["vcnDomainName"])
		assert.Equal(t, "AVAILABLE", body["lifecycleState"])
		assert.NotEmpty(t, body["defaultRouteTableId"])
		assert.NotEmpty(t, body["defaultSecurityListId"])
		assert.NotEmpty(t, body["defaultDhcpOptionsId"])
	})

	t.Run("create without a compartment is rejected", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/vcns", map[string]any{"cidrBlock": vcnCIDR})
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "InvalidParameter", decode(t, w)["code"])
	})

	t.Run("create with a bad CIDR is rejected", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/vcns",
			map[string]any{"compartmentId": compartment, "cidrBlock": "nonsense"})
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "InvalidParameter", decode(t, w)["code"])
	})

	t.Run("get of an unknown OCID is not found", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/vcns/ocid1.vcn.oc1.iad.missing", nil)
		require.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "NotAuthorizedOrNotFound", decode(t, w)["code"])
	})

	t.Run("update replaces the display name and tags", func(t *testing.T) {
		w := f.do(http.MethodPut, "/20160918/vcns/"+id, map[string]any{
			"displayName":  "renamed",
			"freeformTags": map[string]string{"env": "prod"},
		})
		require.Equal(t, http.StatusOK, w.Code)

		body := decode(t, w)
		assert.Equal(t, "renamed", body["displayName"])
		assert.Equal(t, map[string]any{"env": "prod"}, body["freeformTags"])
		assert.Equal(t, "testvcn", body["dnsLabel"], "an attribute the update omits survives")
	})

	t.Run("list requires a compartment", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/vcns", nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "InvalidParameter", decode(t, w)["code"])
	})

	t.Run("list is confined to the compartment", func(t *testing.T) {
		mine := f.do(http.MethodGet, "/20160918/vcns?compartmentId="+compartment, nil)
		require.Equal(t, http.StatusOK, mine.Code)
		assert.Len(t, decodeList(t, mine), 1)

		theirs := f.do(http.MethodGet, "/20160918/vcns?compartmentId="+otherCompartment, nil)
		require.Equal(t, http.StatusOK, theirs.Code)
		assert.Empty(t, decodeList(t, theirs))
		assert.JSONEq(t, "[]", theirs.Body.String(), "an empty page is [] rather than null")
	})

	t.Run("delete", func(t *testing.T) {
		w := f.do(http.MethodDelete, "/20160918/vcns/"+id, nil)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("unsupported method", func(t *testing.T) {
		w := f.do(http.MethodPatch, "/20160918/vcns/"+id, nil)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

func TestSubnetOperations(t *testing.T) {
	f := newFixture(t)
	vcnID := f.newVCN()
	subnetID := f.newSubnet(vcnID)

	t.Run("get carries the VCN's defaults", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/subnets/"+subnetID, nil)
		require.Equal(t, http.StatusOK, w.Code)

		body := decode(t, w)
		assert.Equal(t, vcnID, body["vcnId"])
		assert.Equal(t, subnetCIDR, body["cidrBlock"])
		assert.Equal(t, "10.0.1.1", body["virtualRouterIp"])
		assert.NotEmpty(t, body["routeTableId"])
		assert.Len(t, body["securityListIds"], 1)
	})

	t.Run("create outside the VCN block is rejected", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/subnets", map[string]any{
			"compartmentId": compartment,
			"vcnId":         vcnID,
			"cidrBlock":     "192.168.5.0/24",
		})
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "InvalidParameter", decode(t, w)["code"])
	})

	t.Run("list filters by VCN", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/subnets?compartmentId="+compartment+"&vcnId="+vcnID, nil)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Len(t, decodeList(t, w), 1)

		other := f.do(http.MethodGet, "/20160918/subnets?compartmentId="+compartment+"&vcnId=ocid1.vcn.oc1.iad.other", nil)
		require.Equal(t, http.StatusOK, other.Code)
		assert.Empty(t, decodeList(t, other))
	})

	t.Run("update attaches a route table", func(t *testing.T) {
		rt := f.do(http.MethodPost, "/20160918/routeTables", map[string]any{
			"compartmentId": compartment,
			"vcnId":         vcnID,
			"displayName":   "public",
		})
		require.Equal(t, http.StatusOK, rt.Code)

		tableID, _ := decode(t, rt)["id"].(string)

		w := f.do(http.MethodPut, "/20160918/subnets/"+subnetID, map[string]any{"routeTableId": tableID})
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, tableID, decode(t, w)["routeTableId"])
	})

	t.Run("delete of an unknown subnet is not found", func(t *testing.T) {
		w := f.do(http.MethodDelete, "/20160918/subnets/ocid1.subnet.oc1.iad.missing", nil)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestNSGOperations(t *testing.T) {
	f := newFixture(t)
	vcnID := f.newVCN()

	w := f.do(http.MethodPost, "/20160918/networkSecurityGroups", map[string]any{
		"compartmentId": compartment,
		"vcnId":         vcnID,
		"displayName":   "web",
	})
	require.Equal(t, http.StatusOK, w.Code)

	nsgID, _ := decode(t, w)["id"].(string)
	base := "/20160918/networkSecurityGroups/" + nsgID

	var ruleID string

	t.Run("add rules", func(t *testing.T) {
		added := f.do(http.MethodPost, base+"/actions/addSecurityRules", map[string]any{
			"securityRules": []map[string]any{{
				"direction":  "INGRESS",
				"protocol":   "6",
				"source":     "0.0.0.0/0",
				"sourceType": "CIDR_BLOCK",
				"tcpOptions": map[string]any{"destinationPortRange": map[string]int{"min": 443, "max": 443}},
			}},
		})
		require.Equal(t, http.StatusOK, added.Code)

		rules, ok := decode(t, added)["securityRules"].([]any)
		require.True(t, ok)
		require.Len(t, rules, 1)

		rule, ok := rules[0].(map[string]any)
		require.True(t, ok)
		ruleID, _ = rule["id"].(string)
		assert.NotEmpty(t, ruleID)
		assert.Equal(t, "6", rule["protocol"])
	})

	t.Run("list rules", func(t *testing.T) {
		listed := f.do(http.MethodGet, base+"/securityRules", nil)
		require.Equal(t, http.StatusOK, listed.Code)
		assert.Len(t, decodeList(t, listed), 1)

		filtered := f.do(http.MethodGet, base+"/securityRules?direction=EGRESS", nil)
		require.Equal(t, http.StatusOK, filtered.Code)
		assert.Empty(t, decodeList(t, filtered))
	})

	t.Run("remove rules", func(t *testing.T) {
		removed := f.do(http.MethodPost, base+"/actions/removeSecurityRules",
			map[string]any{"securityRuleIds": []string{ruleID}})
		require.Equal(t, http.StatusNoContent, removed.Code)

		missing := f.do(http.MethodPost, base+"/actions/removeSecurityRules",
			map[string]any{"securityRuleIds": []string{"deadbeef"}})
		require.Equal(t, http.StatusNotFound, missing.Code)
	})

	t.Run("unknown action", func(t *testing.T) {
		unknown := f.do(http.MethodPost, base+"/actions/explode", map[string]any{})
		assert.Equal(t, http.StatusNotFound, unknown.Code)
	})

	t.Run("vnics sub-collection", func(t *testing.T) {
		vnics := f.do(http.MethodGet, base+"/vnics", nil)
		require.Equal(t, http.StatusOK, vnics.Code)
		assert.Empty(t, decodeList(t, vnics))

		missing := f.do(http.MethodGet, "/20160918/networkSecurityGroups/ocid1.networksecuritygroup.oc1.iad.x/vnics", nil)
		assert.Equal(t, http.StatusNotFound, missing.Code)
	})

	t.Run("create against an unknown VCN", func(t *testing.T) {
		bad := f.do(http.MethodPost, "/20160918/networkSecurityGroups", map[string]any{
			"compartmentId": compartment,
			"vcnId":         "ocid1.vcn.oc1.iad.missing",
		})
		assert.Equal(t, http.StatusNotFound, bad.Code)
	})
}

func TestSecurityListOperations(t *testing.T) {
	f := newFixture(t)
	vcnID := f.newVCN()

	created := f.do(http.MethodPost, "/20160918/securityLists", map[string]any{
		"compartmentId": compartment,
		"vcnId":         vcnID,
		"displayName":   "web-list",
		"ingressSecurityRules": []map[string]any{{
			"protocol":   "6",
			"source":     "10.0.0.0/16",
			"tcpOptions": map[string]any{"destinationPortRange": map[string]int{"min": 80, "max": 80}},
		}},
		"egressSecurityRules": []map[string]any{{"protocol": "all", "destination": "0.0.0.0/0"}},
	})
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	body := decode(t, created)
	listID, _ := body["id"].(string)
	assert.Len(t, body["ingressSecurityRules"], 1)
	assert.Len(t, body["egressSecurityRules"], 1)

	t.Run("update replaces the rule set", func(t *testing.T) {
		w := f.do(http.MethodPut, "/20160918/securityLists/"+listID, map[string]any{
			"ingressSecurityRules": []map[string]any{},
			"egressSecurityRules":  []map[string]any{{"protocol": "all", "destination": "0.0.0.0/0"}},
		})
		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, decode(t, w)["ingressSecurityRules"])
	})

	t.Run("the VCN's default list cannot be deleted", func(t *testing.T) {
		defaults := f.mock.Defaults(vcnID)

		w := f.do(http.MethodDelete, "/20160918/securityLists/"+defaults.SecurityListID, nil)
		require.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, "IncorrectState", decode(t, w)["code"])
	})

	t.Run("list includes the default and the new list", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/securityLists?compartmentId="+compartment, nil)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Len(t, decodeList(t, w), 2)
	})

	t.Run("delete", func(t *testing.T) {
		w := f.do(http.MethodDelete, "/20160918/securityLists/"+listID, nil)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})
}

func TestRouteTableOperations(t *testing.T) {
	f := newFixture(t)
	vcnID := f.newVCN()

	igw := f.do(http.MethodPost, "/20160918/internetGateways", map[string]any{
		"compartmentId": compartment,
		"vcnId":         vcnID,
		"displayName":   "igw",
		"isEnabled":     true,
	})
	require.Equal(t, http.StatusOK, igw.Code, igw.Body.String())

	igwID, _ := decode(t, igw)["id"].(string)

	created := f.do(http.MethodPost, "/20160918/routeTables", map[string]any{
		"compartmentId": compartment,
		"vcnId":         vcnID,
		"displayName":   "public",
		"routeRules": []map[string]any{
			{"destination": "0.0.0.0/0", "destinationType": "CIDR_BLOCK", "networkEntityId": igwID},
		},
	})
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	tableID, _ := decode(t, created)["id"].(string)

	t.Run("rules round-trip", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/routeTables/"+tableID, nil)
		require.Equal(t, http.StatusOK, w.Code)

		rules, ok := decode(t, w)["routeRules"].([]any)
		require.True(t, ok)
		require.Len(t, rules, 1)

		rule, ok := rules[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "0.0.0.0/0", rule["destination"])
		assert.Equal(t, igwID, rule["networkEntityId"])
	})

	t.Run("update replaces the rules", func(t *testing.T) {
		w := f.do(http.MethodPut, "/20160918/routeTables/"+tableID, map[string]any{"routeRules": []map[string]any{}})
		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, decode(t, w)["routeRules"])
	})

	t.Run("update of an unknown table is not found", func(t *testing.T) {
		w := f.do(http.MethodPut, "/20160918/routeTables/ocid1.routetable.oc1.iad.missing", map[string]any{})
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestGatewayOperations(t *testing.T) {
	f := newFixture(t)
	vcnID := f.newVCN()

	t.Run("internet gateway attaches to its VCN", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/internetGateways", map[string]any{
			"compartmentId": compartment,
			"vcnId":         vcnID,
			"displayName":   "igw",
			"isEnabled":     true,
		})
		require.Equal(t, http.StatusOK, w.Code)

		body := decode(t, w)
		assert.Equal(t, vcnID, body["vcnId"])
		assert.Equal(t, true, body["isEnabled"])

		id, _ := body["id"].(string)
		deleted := f.do(http.MethodDelete, "/20160918/internetGateways/"+id, nil)
		assert.Equal(t, http.StatusNoContent, deleted.Code)
	})

	t.Run("internet gateway against an unknown VCN", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/internetGateways", map[string]any{
			"compartmentId": compartment,
			"vcnId":         "ocid1.vcn.oc1.iad.missing",
		})
		assert.Equal(t, http.StatusNotFound, w.Code)

		// Create and attach are one call in OCI, so the half that succeeded
		// must not leave a detached gateway behind.
		listed := f.do(http.MethodGet, "/20160918/internetGateways?compartmentId="+compartment, nil)
		require.Equal(t, http.StatusOK, listed.Code)
		assert.Empty(t, decodeList(t, listed), "a failed attach leaves no gateway")
	})

	t.Run("nat gateway", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/natGateways", map[string]any{
			"compartmentId": compartment,
			"vcnId":         vcnID,
			"displayName":   "nat",
		})
		require.Equal(t, http.StatusOK, w.Code)

		body := decode(t, w)
		assert.Equal(t, vcnID, body["vcnId"])
		assert.NotEmpty(t, body["natIp"])
		assert.Equal(t, false, body["blockTraffic"])

		id, _ := body["id"].(string)
		blocked := f.do(http.MethodPut, "/20160918/natGateways/"+id, map[string]any{"blockTraffic": true})
		require.Equal(t, http.StatusOK, blocked.Code)
		assert.Equal(t, true, decode(t, blocked)["blockTraffic"])
	})

	t.Run("service gateway needs a service", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/serviceGateways", map[string]any{
			"compartmentId": compartment,
			"vcnId":         vcnID,
		})
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "InvalidParameter", decode(t, w)["code"])
	})

	t.Run("service gateway", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/serviceGateways", map[string]any{
			"compartmentId": compartment,
			"vcnId":         vcnID,
			"displayName":   "sgw",
			"services": []map[string]string{
				{"serviceId": "ocid1.service.oc1..oss", "serviceName": "OCI Object Storage"},
			},
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		services, ok := decode(t, w)["services"].([]any)
		require.True(t, ok)
		require.Len(t, services, 1)

		service, ok := services[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "ocid1.service.oc1..oss", service["serviceId"])
		assert.Equal(t, "OCI Object Storage", service["serviceName"])
	})

	t.Run("gateway lists are compartment scoped", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/natGateways?compartmentId="+otherCompartment, nil)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, decodeList(t, w))
	})
}

func TestDHCPOptionsOperations(t *testing.T) {
	f := newFixture(t)
	vcnID := f.newVCN()

	created := f.do(http.MethodPost, "/20160918/dhcps", map[string]any{
		"compartmentId": compartment,
		"vcnId":         vcnID,
		"displayName":   "custom",
		"options": []map[string]any{
			{"type": "DomainNameServer", "serverType": "CustomDnsServer", "customDnsServers": []string{"8.8.8.8"}},
			{"type": "SearchDomain", "searchDomainNames": []string{"corp.example"}},
		},
	})
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	body := decode(t, created)
	id, _ := body["id"].(string)
	assert.Len(t, body["options"], 2)

	t.Run("custom DNS with no servers is rejected", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/dhcps", map[string]any{
			"compartmentId": compartment,
			"vcnId":         vcnID,
			"options":       []map[string]any{{"type": "DomainNameServer", "serverType": "CustomDnsServer"}},
		})
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "InvalidParameter", decode(t, w)["code"])
	})

	t.Run("list includes the VCN's default set", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/dhcps?compartmentId="+compartment+"&vcnId="+vcnID, nil)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Len(t, decodeList(t, w), 2)
	})

	t.Run("delete", func(t *testing.T) {
		w := f.do(http.MethodDelete, "/20160918/dhcps/"+id, nil)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})
}

func TestVNICAndIPOperations(t *testing.T) {
	f := newFixture(t)
	vcnID := f.newVCN()
	subnetID := f.newSubnet(vcnID)

	// VNICs come from Compute's attachments, so the fixture uses the driver's
	// creation capability directly.
	vnic, err := f.mock.CreateNetworkInterface(t.Context(), subnetID, "primary", nil)
	require.NoError(t, err)

	t.Run("get vnic", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/vnics/"+vnic.ID, nil)
		require.Equal(t, http.StatusOK, w.Code)

		body := decode(t, w)
		assert.Equal(t, subnetID, body["subnetId"])
		assert.Equal(t, "10.0.1.2", body["privateIp"])
		assert.Regexp(t, `^00:00:17(:[0-9a-f]{2}){3}$`, body["macAddress"])
	})

	t.Run("get unknown vnic", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/vnics/ocid1.vnic.oc1.iad.missing", nil)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("vnic collection has no create", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/vnics", map[string]any{})
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})

	t.Run("secondary private ip", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/privateIps", map[string]any{
			"vnicId":      vnic.ID,
			"displayName": "second",
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		body := decode(t, w)
		assert.Equal(t, "10.0.1.3", body["ipAddress"])
		assert.Equal(t, false, body["isPrimary"])
	})

	t.Run("private ip create needs a vnic", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/privateIps", map[string]any{})
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "InvalidParameter", decode(t, w)["code"])
	})

	t.Run("private ip list needs a narrowing parameter", func(t *testing.T) {
		w := f.do(http.MethodGet, "/20160918/privateIps", nil)
		require.Equal(t, http.StatusBadRequest, w.Code)

		byVNIC := f.do(http.MethodGet, "/20160918/privateIps?vnicId="+vnic.ID, nil)
		require.Equal(t, http.StatusOK, byVNIC.Code)
		assert.Len(t, decodeList(t, byVNIC), 2)

		bySubnet := f.do(http.MethodGet, "/20160918/privateIps?subnetId="+subnetID, nil)
		require.Equal(t, http.StatusOK, bySubnet.Code)
		assert.Len(t, decodeList(t, bySubnet), 2)
	})

	t.Run("public ip assignment", func(t *testing.T) {
		ips := f.do(http.MethodGet, "/20160918/privateIps?vnicId="+vnic.ID, nil)
		require.Equal(t, http.StatusOK, ips.Code)

		privateIPs := decodeList(t, ips)
		require.NotEmpty(t, privateIPs)

		privateIPID, _ := privateIPs[0]["id"].(string)

		created := f.do(http.MethodPost, "/20160918/publicIps", map[string]any{
			"compartmentId": compartment,
			"displayName":   "reserved",
			"lifetime":      "RESERVED",
			"privateIpId":   privateIPID,
		})
		require.Equal(t, http.StatusOK, created.Code, created.Body.String())

		body := decode(t, created)
		assert.Equal(t, "PRIVATE_IP", body["assignedEntityType"])
		assert.Equal(t, privateIPID, body["privateIpId"])
		assert.Equal(t, "REGION", body["scope"])

		id, _ := body["id"].(string)

		blocked := f.do(http.MethodDelete, "/20160918/publicIps/"+id, nil)
		require.Equal(t, http.StatusConflict, blocked.Code, "an assigned address cannot be released")

		unassigned := f.do(http.MethodPut, "/20160918/publicIps/"+id, map[string]any{"privateIpId": ""})
		require.Equal(t, http.StatusOK, unassigned.Code)

		released := f.do(http.MethodDelete, "/20160918/publicIps/"+id, nil)
		assert.Equal(t, http.StatusNoContent, released.Code)
	})
}

// TestPublicIPRollsBackOnFailedAssign covers the two paths that mutate before
// AssociateAddress can refuse: a reassign detaches first, a create allocates
// first. Neither may leave the address stranded when the target is taken.
func TestPublicIPRollsBackOnFailedAssign(t *testing.T) {
	f := newFixture(t)
	subnetID := f.newSubnet(f.newVCN())

	// Two VNICs, so their primary private IPs give an occupied target and a
	// separate address to move around.
	firstVNIC, err := f.mock.CreateNetworkInterface(t.Context(), subnetID, "first", nil)
	require.NoError(t, err)

	secondVNIC, err := f.mock.CreateNetworkInterface(t.Context(), subnetID, "second", nil)
	require.NoError(t, err)

	first := f.primaryPrivateIP(firstVNIC.ID)
	second := f.primaryPrivateIP(secondVNIC.ID)

	occupier := f.do(http.MethodPost, "/20160918/publicIps", map[string]any{
		"compartmentId": compartment,
		"displayName":   "occupier",
		"lifetime":      "RESERVED",
		"privateIpId":   second,
	})
	require.Equal(t, http.StatusOK, occupier.Code, occupier.Body.String())

	t.Run("a rejected reassign keeps the original binding", func(t *testing.T) {
		created := f.do(http.MethodPost, "/20160918/publicIps", map[string]any{
			"compartmentId": compartment,
			"displayName":   "mover",
			"lifetime":      "RESERVED",
			"privateIpId":   first,
		})
		require.Equal(t, http.StatusOK, created.Code, created.Body.String())

		id, _ := decode(t, created)["id"].(string)

		moved := f.do(http.MethodPut, "/20160918/publicIps/"+id, map[string]any{"privateIpId": second})
		require.Equal(t, http.StatusConflict, moved.Code, "the target already holds a public IP")

		got := f.do(http.MethodGet, "/20160918/publicIps/"+id, nil)
		require.Equal(t, http.StatusOK, got.Code)
		assert.Equal(t, first, decode(t, got)["privateIpId"],
			"a rejected move must not leave the address detached")
	})

	t.Run("a rejected create releases the allocated address", func(t *testing.T) {
		listed := "/20160918/publicIps?compartmentId=" + compartment
		before := decodeList(t, f.do(http.MethodGet, listed, nil))

		created := f.do(http.MethodPost, "/20160918/publicIps", map[string]any{
			"compartmentId": compartment,
			"displayName":   "doomed",
			"lifetime":      "RESERVED",
			"privateIpId":   second,
		})
		require.Equal(t, http.StatusConflict, created.Code, "the target already holds a public IP")

		after := decodeList(t, f.do(http.MethodGet, listed, nil))
		assert.Len(t, after, len(before), "a rejected create must not leave an orphan address")
	})
}

func TestChangeCompartment(t *testing.T) {
	f := newFixture(t)
	id := f.newVCN()
	target := "/20160918/vcns/" + id + "/actions/changeCompartment"

	t.Run("moves the resource and records a work request", func(t *testing.T) {
		w := f.do(http.MethodPost, target, map[string]any{"compartmentId": otherCompartment})
		require.Equal(t, http.StatusAccepted, w.Code)

		wrID := w.Header().Get(ocirest.HeaderWorkRequestID)
		require.NotEmpty(t, wrID)

		wr, ok := f.work.Get(wrID)
		require.True(t, ok)
		assert.Equal(t, "CHANGE_VCN_COMPARTMENT", wr.OperationType)
		assert.Equal(t, otherCompartment, wr.CompartmentID)
		require.Len(t, wr.Resources, 1)
		assert.Equal(t, id, wr.Resources[0].Identifier)
		assert.Equal(t, "vcn", wr.Resources[0].EntityType)

		gone := f.do(http.MethodGet, "/20160918/vcns?compartmentId="+compartment, nil)
		assert.Empty(t, decodeList(t, gone))

		moved := f.do(http.MethodGet, "/20160918/vcns?compartmentId="+otherCompartment, nil)
		assert.Len(t, decodeList(t, moved), 1)
	})

	t.Run("needs a target compartment", func(t *testing.T) {
		w := f.do(http.MethodPost, target, map[string]any{})
		require.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "InvalidParameter", decode(t, w)["code"])
	})

	t.Run("unknown resource", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/vcns/ocid1.vcn.oc1.iad.missing/actions/changeCompartment",
			map[string]any{"compartmentId": otherCompartment})
		require.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("only POST", func(t *testing.T) {
		w := f.do(http.MethodGet, target, nil)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

func TestRequestIDIsEchoed(t *testing.T) {
	f := newFixture(t)

	r := httptest.NewRequest(http.MethodGet, "/20160918/vcns?compartmentId="+compartment, nil)
	r.Header.Set(ocirest.HeaderRequestID, "caller-supplied-id")

	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)

	assert.Equal(t, "caller-supplied-id", w.Header().Get(ocirest.HeaderRequestID))
}

func TestPagination(t *testing.T) {
	f := newFixture(t)

	for _, cidr := range []string{"10.0.0.0/16", "10.1.0.0/16", "10.2.0.0/16"} {
		w := f.do(http.MethodPost, "/20160918/vcns",
			map[string]any{"compartmentId": compartment, "cidrBlock": cidr})
		require.Equal(t, http.StatusOK, w.Code)
	}

	first := f.do(http.MethodGet, "/20160918/vcns?compartmentId="+compartment+"&limit=2", nil)
	require.Equal(t, http.StatusOK, first.Code)
	assert.Len(t, decodeList(t, first), 2)

	next := first.Header().Get(ocirest.HeaderNextPage)
	require.NotEmpty(t, next)

	second := f.do(http.MethodGet, "/20160918/vcns?compartmentId="+compartment+"&limit=2&page="+next, nil)
	require.Equal(t, http.StatusOK, second.Code)
	assert.Len(t, decodeList(t, second), 1)
	assert.Empty(t, second.Header().Get(ocirest.HeaderNextPage), "the last page sets no cursor")
}

func TestMalformedBody(t *testing.T) {
	f := newFixture(t)

	r := httptest.NewRequest(http.MethodPost, "/20160918/vcns", strings.NewReader("{not json"))
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "InvalidParameter", decode(t, w)["code"])
}

func TestDriverWithoutOCICapabilitiesIsNotImplemented(t *testing.T) {
	// A networking driver that cannot record compartments cannot serve OCI's
	// compartment-scoped API at all, rather than serving it wrongly.
	h := ocivcn.New(bareDriver{}, nil)

	r := httptest.NewRequest(http.MethodGet, "/20160918/vcns?compartmentId="+compartment, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusNotImplemented, w.Code)
	assert.Equal(t, "NotImplemented", decode(t, w)["code"])
}

// bareDriver is a networking driver carrying none of OCI's optional
// capabilities. The embedded interface is nil: the handler must refuse before
// it reaches any method.
type bareDriver struct{ netdriver.Networking }
