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
		{name: "local peering gateways", method: http.MethodGet, path: "/20160918/localPeeringGateways", want: true},
		{name: "local peering gateway connect", method: http.MethodPost,
			path: "/20160918/localPeeringGateways/ocid1.localpeeringgateway.oc1.iad.a/actions/connect", want: true},
		{name: "drgs, claimed to be refused", method: http.MethodGet, path: "/20160918/drgs", want: true},
		{name: "remote peerings, claimed to be refused", method: http.MethodGet,
			path: "/20160918/remotePeeringConnections", want: true},

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

	t.Run("a rule keeps the kind of entity it names", func(t *testing.T) {
		const service = "all-iad-services-in-oracle-services-network"

		added := f.do(http.MethodPost, base+"/actions/addSecurityRules", map[string]any{
			"securityRules": []map[string]any{
				{
					"direction":       "EGRESS",
					"protocol":        "6",
					"destination":     service,
					"destinationType": "SERVICE_CIDR_BLOCK",
				},
				{
					"direction":  "INGRESS",
					"protocol":   "6",
					"source":     nsgID,
					"sourceType": "NETWORK_SECURITY_GROUP",
				},
				{
					"direction":  "INGRESS",
					"protocol":   "6",
					"source":     "10.0.0.0/8",
					"sourceType": "CIDR_BLOCK",
				},
			},
		})
		require.Equal(t, http.StatusOK, added.Code, added.Body.String())

		listed := decodeList(t, f.do(http.MethodGet, base+"/securityRules", nil))
		byValue := map[string]map[string]any{}

		for _, rule := range listed {
			if v, ok := rule["source"].(string); ok && v != "" {
				byValue[v] = rule
			}

			if v, ok := rule["destination"].(string); ok && v != "" {
				byValue[v] = rule
			}
		}

		require.Contains(t, byValue, service)
		assert.Equal(t, "SERVICE_CIDR_BLOCK", byValue[service]["destinationType"])

		require.Contains(t, byValue, nsgID)
		assert.Equal(t, "NETWORK_SECURITY_GROUP", byValue[nsgID]["sourceType"])

		require.Contains(t, byValue, "10.0.0.0/8")
		assert.Equal(t, "CIDR_BLOCK", byValue["10.0.0.0/8"]["sourceType"])
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

		renamed := f.do(http.MethodPut, "/20160918/publicIps/"+id, map[string]any{"displayName": "renamed"})
		require.Equal(t, http.StatusOK, renamed.Code)
		assert.Equal(t, privateIPID, decode(t, renamed)["privateIpId"],
			"an update naming no privateIpId leaves the assignment alone")

		unassigned := f.do(http.MethodPut, "/20160918/publicIps/"+id, map[string]any{"privateIpId": ""})
		require.Equal(t, http.StatusOK, unassigned.Code)
		assert.Empty(t, decode(t, unassigned)["privateIpId"])

		released := f.do(http.MethodDelete, "/20160918/publicIps/"+id, nil)
		assert.Equal(t, http.StatusNoContent, released.Code)
	})
}

// TestEphemeralPublicIPLifetime covers the three ways OCI treats an ephemeral
// address differently from a reserved one: it reports the availability domain
// as its scope, it stays pinned to one private IP for its whole life, and
// deleting it is what unassigns it.
func TestEphemeralPublicIPLifetime(t *testing.T) {
	f := newFixture(t)
	subnetID := f.newSubnet(f.newVCN())

	vnic, err := f.mock.CreateNetworkInterface(t.Context(), subnetID, "host", nil)
	require.NoError(t, err)

	other, err := f.mock.CreateNetworkInterface(t.Context(), subnetID, "other", nil)
	require.NoError(t, err)

	target := f.primaryPrivateIP(vnic.ID)
	spare := f.primaryPrivateIP(other.ID)

	newEphemeral := func(t *testing.T, privateIPID string) string {
		t.Helper()

		w := f.do(http.MethodPost, "/20160918/publicIps", map[string]any{
			"compartmentId": compartment,
			"lifetime":      "EPHEMERAL",
			"privateIpId":   privateIPID,
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		id, _ := decode(t, w)["id"].(string)

		return id
	}

	drop := func(t *testing.T, id string) {
		t.Helper()

		w := f.do(http.MethodDelete, "/20160918/publicIps/"+id, nil)
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	}

	t.Run("scope follows the lifetime", func(t *testing.T) {
		id := newEphemeral(t, target)

		got := f.do(http.MethodGet, "/20160918/publicIps/"+id, nil)
		require.Equal(t, http.StatusOK, got.Code)
		assert.Equal(t, "AVAILABILITY_DOMAIN", decode(t, got)["scope"])

		reserved := f.do(http.MethodPost, "/20160918/publicIps", map[string]any{
			"compartmentId": compartment,
			"lifetime":      "RESERVED",
		})
		require.Equal(t, http.StatusOK, reserved.Code, reserved.Body.String())
		assert.Equal(t, "REGION", decode(t, reserved)["scope"])

		reservedID, _ := decode(t, reserved)["id"].(string)

		drop(t, id)
		drop(t, reservedID)
	})

	t.Run("an ephemeral cannot be created unassigned", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/publicIps", map[string]any{
			"compartmentId": compartment,
			"lifetime":      "EPHEMERAL",
		})
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

		listed := decodeList(t, f.do(http.MethodGet, "/20160918/publicIps?compartmentId="+compartment, nil))
		assert.Empty(t, listed, "the refused create must not leave an orphan address")
	})

	t.Run("an ephemeral cannot be unassigned or moved", func(t *testing.T) {
		id := newEphemeral(t, target)

		unassign := f.do(http.MethodPut, "/20160918/publicIps/"+id, map[string]any{"privateIpId": ""})
		require.Equal(t, http.StatusBadRequest, unassign.Code, unassign.Body.String())

		moved := f.do(http.MethodPut, "/20160918/publicIps/"+id, map[string]any{"privateIpId": spare})
		require.Equal(t, http.StatusBadRequest, moved.Code, moved.Body.String())

		got := f.do(http.MethodGet, "/20160918/publicIps/"+id, nil)
		require.Equal(t, http.StatusOK, got.Code)
		assert.Equal(t, target, decode(t, got)["privateIpId"], "a refused update must leave the binding intact")

		drop(t, id)
	})

	t.Run("delete releases an assigned ephemeral", func(t *testing.T) {
		id := newEphemeral(t, target)

		drop(t, id)

		assert.Equal(t, http.StatusNotFound, f.do(http.MethodGet, "/20160918/publicIps/"+id, nil).Code)

		listed := decodeList(t, f.do(http.MethodGet, "/20160918/publicIps?compartmentId="+compartment, nil))
		assert.Empty(t, listed, "the deleted address must not survive as a record")

		// The private IP has to come back free, or the 1:1 guard refuses every
		// later address on it.
		again := f.do(http.MethodPost, "/20160918/publicIps", map[string]any{
			"compartmentId": compartment,
			"lifetime":      "EPHEMERAL",
			"privateIpId":   target,
		})
		require.Equal(t, http.StatusOK, again.Code, again.Body.String())

		reused, _ := decode(t, again)["id"].(string)
		drop(t, reused)
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

// TestProhibitPublicIPOnVnicIsEnforced covers the subnet flag being honoured
// rather than only round-tripped: OCI refuses a public IP on a VNIC in a
// private subnet, on create and on reassign alike.
func TestProhibitPublicIPOnVnicIsEnforced(t *testing.T) {
	f := newFixture(t)
	vcnID := f.newVCN()

	private := f.do(http.MethodPost, "/20160918/subnets", map[string]any{
		"compartmentId":          compartment,
		"vcnId":                  vcnID,
		"cidrBlock":              "10.0.2.0/24",
		"displayName":            "private",
		"prohibitPublicIpOnVnic": true,
	})
	require.Equal(t, http.StatusOK, private.Code, private.Body.String())

	privateSubnetID, _ := decode(t, private)["id"].(string)
	publicSubnetID := f.newSubnet(vcnID)

	privateVNIC, err := f.mock.CreateNetworkInterface(t.Context(), privateSubnetID, "private", nil)
	require.NoError(t, err)

	publicVNIC, err := f.mock.CreateNetworkInterface(t.Context(), publicSubnetID, "public", nil)
	require.NoError(t, err)

	blockedTarget := f.primaryPrivateIP(privateVNIC.ID)
	allowedTarget := f.primaryPrivateIP(publicVNIC.ID)

	t.Run("create refuses the assignment", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/publicIps", map[string]any{
			"compartmentId": compartment,
			"lifetime":      "RESERVED",
			"privateIpId":   blockedTarget,
		})
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

		listed := decodeList(t, f.do(http.MethodGet, "/20160918/publicIps?compartmentId="+compartment, nil))
		assert.Empty(t, listed, "the refused create must not leave an orphan address")
	})

	t.Run("reassign refuses and keeps the original binding", func(t *testing.T) {
		created := f.do(http.MethodPost, "/20160918/publicIps", map[string]any{
			"compartmentId": compartment,
			"lifetime":      "RESERVED",
			"privateIpId":   allowedTarget,
		})
		require.Equal(t, http.StatusOK, created.Code, created.Body.String())

		id, _ := decode(t, created)["id"].(string)

		moved := f.do(http.MethodPut, "/20160918/publicIps/"+id, map[string]any{"privateIpId": blockedTarget})
		require.Equal(t, http.StatusBadRequest, moved.Code, moved.Body.String())

		got := f.do(http.MethodGet, "/20160918/publicIps/"+id, nil)
		require.Equal(t, http.StatusOK, got.Code)
		assert.Equal(t, allowedTarget, decode(t, got)["privateIpId"])
	})
}

// TestVCNCIDRBlocks covers a VCN carrying more than one block: every block it
// holds accepts subnets, and the blocks can be added and removed after create.
func TestVCNCIDRBlocks(t *testing.T) {
	f := newFixture(t)

	const (
		second = "192.168.0.0/16"
		third  = "172.16.0.0/16"
	)

	created := f.do(http.MethodPost, "/20160918/vcns", map[string]any{
		"compartmentId": compartment,
		"cidrBlocks":    []string{vcnCIDR, second},
		"displayName":   "multi-cidr",
	})
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())

	body := decode(t, created)
	id, _ := body["id"].(string)

	assert.Equal(t, vcnCIDR, body["cidrBlock"], "the first block is the primary one")
	assert.Equal(t, []any{vcnCIDR, second}, body["cidrBlocks"])

	addCIDR := "/20160918/vcns/" + id + "/actions/addVcnCidr"
	removeCIDR := "/20160918/vcns/" + id + "/actions/removeVcnCidr"

	newSubnet := func(t *testing.T, cidr string) *httptest.ResponseRecorder {
		t.Helper()

		return f.do(http.MethodPost, "/20160918/subnets", map[string]any{
			"compartmentId": compartment,
			"vcnId":         id,
			"cidrBlock":     cidr,
		})
	}

	t.Run("a subnet may sit in any block the VCN carries", func(t *testing.T) {
		first := newSubnet(t, "10.0.1.0/24")
		require.Equal(t, http.StatusOK, first.Code, first.Body.String())

		secondary := newSubnet(t, "192.168.1.0/24")
		require.Equal(t, http.StatusOK, secondary.Code, secondary.Body.String())

		outside := newSubnet(t, "172.31.0.0/24")
		assert.Equal(t, http.StatusBadRequest, outside.Code, "a block the VCN does not carry is still refused")
	})

	t.Run("cidrBlock and cidrBlocks are mutually exclusive", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/vcns", map[string]any{
			"compartmentId": compartment,
			"cidrBlock":     vcnCIDR,
			"cidrBlocks":    []string{second},
		})
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("a refused block leaves no half-built VCN", func(t *testing.T) {
		before := decodeList(t, f.do(http.MethodGet, "/20160918/vcns?compartmentId="+compartment, nil))

		w := f.do(http.MethodPost, "/20160918/vcns", map[string]any{
			"compartmentId": compartment,
			"cidrBlocks":    []string{"10.1.0.0/16", "10.1.1.0/24"},
		})
		require.Equal(t, http.StatusBadRequest, w.Code, "the second block overlaps the first")

		after := decodeList(t, f.do(http.MethodGet, "/20160918/vcns?compartmentId="+compartment, nil))
		assert.Len(t, after, len(before))
	})

	t.Run("addVcnCidr records a work request", func(t *testing.T) {
		w := f.do(http.MethodPost, addCIDR, map[string]any{"cidrBlock": third})
		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())

		wrID := w.Header().Get(ocirest.HeaderWorkRequestID)
		require.NotEmpty(t, wrID)

		wr, ok := f.work.Get(wrID)
		require.True(t, ok)
		assert.Equal(t, "ADD_VCN_CIDR", wr.OperationType)
		require.Len(t, wr.Resources, 1)
		assert.Equal(t, id, wr.Resources[0].Identifier)

		got := decode(t, f.do(http.MethodGet, "/20160918/vcns/"+id, nil))
		assert.Equal(t, []any{vcnCIDR, second, third}, got["cidrBlocks"])

		added := newSubnet(t, "172.16.1.0/24")
		assert.Equal(t, http.StatusOK, added.Code, added.Body.String())
	})

	t.Run("an overlapping block is refused", func(t *testing.T) {
		w := f.do(http.MethodPost, addCIDR, map[string]any{"cidrBlock": "10.0.128.0/17"})
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("cidrBlock is required", func(t *testing.T) {
		w := f.do(http.MethodPost, addCIDR, map[string]any{})
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("removeVcnCidr refuses a block still holding a subnet", func(t *testing.T) {
		w := f.do(http.MethodPost, removeCIDR, map[string]any{"cidrBlock": second})
		assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	})

	t.Run("removeVcnCidr drops an empty block", func(t *testing.T) {
		empty := f.do(http.MethodPost, addCIDR, map[string]any{"cidrBlock": "10.200.0.0/16"})
		require.Equal(t, http.StatusAccepted, empty.Code)

		w := f.do(http.MethodPost, removeCIDR, map[string]any{"cidrBlock": "10.200.0.0/16"})
		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())

		got := decode(t, f.do(http.MethodGet, "/20160918/vcns/"+id, nil))
		assert.Equal(t, []any{vcnCIDR, second, third}, got["cidrBlocks"])

		gone := newSubnet(t, "10.200.1.0/24")
		assert.Equal(t, http.StatusBadRequest, gone.Code, "the removed block no longer accepts subnets")
	})

	t.Run("an unknown action is not found", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/vcns/"+id+"/actions/modifyVcnCidr",
			map[string]any{"originalCidrBlock": second, "newCidrBlock": "192.168.0.0/17"})
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("only POST", func(t *testing.T) {
		w := f.do(http.MethodGet, addCIDR, nil)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

// TestLocalPeeringGateways covers the VCN-to-VCN path an SDK client reaches
// for: a gateway either side, connected by the action, and torn down from one
// end.
func TestLocalPeeringGateways(t *testing.T) {
	f := newFixture(t)
	local := f.newVCN()

	remote := f.do(http.MethodPost, "/20160918/vcns", map[string]any{
		"compartmentId": compartment,
		"cidrBlock":     "192.168.0.0/16",
		"displayName":   "remote-vcn",
	})
	require.Equal(t, http.StatusOK, remote.Code, remote.Body.String())

	remoteID, _ := decode(t, remote)["id"].(string)

	newLPG := func(t *testing.T, vcnID, name string) string {
		t.Helper()

		w := f.do(http.MethodPost, "/20160918/localPeeringGateways", map[string]any{
			"compartmentId": compartment,
			"vcnId":         vcnID,
			"displayName":   name,
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		body := decode(t, w)
		assert.Equal(t, "NEW", body["peeringStatus"])
		assert.Equal(t, vcnID, body["vcnId"])

		id, _ := body["id"].(string)

		return id
	}

	here := newLPG(t, local, "here")
	there := newLPG(t, remoteID, "there")

	t.Run("connect peers both ends", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/localPeeringGateways/"+here+"/actions/connect",
			map[string]any{"peerId": there})
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

		got := decode(t, f.do(http.MethodGet, "/20160918/localPeeringGateways/"+here, nil))
		assert.Equal(t, "PEERED", got["peeringStatus"])
		assert.Equal(t, there, got["peerId"])
		assert.Equal(t, "192.168.0.0/16", got["peerAdvertisedCidr"], "the far end's blocks are advertised")

		far := decode(t, f.do(http.MethodGet, "/20160918/localPeeringGateways/"+there, nil))
		assert.Equal(t, "PEERED", far["peeringStatus"])
		assert.Equal(t, here, far["peerId"])
		assert.Equal(t, vcnCIDR, far["peerAdvertisedCidr"])
	})

	t.Run("a route may point at the gateway", func(t *testing.T) {
		table := f.do(http.MethodPost, "/20160918/routeTables", map[string]any{
			"compartmentId": compartment,
			"vcnId":         local,
			"displayName":   "to-peer",
			"routeRules": []map[string]any{
				{"destination": "192.168.0.0/16", "networkEntityId": here},
			},
		})
		require.Equal(t, http.StatusOK, table.Code, table.Body.String())

		wrong := f.do(http.MethodPost, "/20160918/routeTables", map[string]any{
			"compartmentId": compartment,
			"vcnId":         local,
			"routeRules": []map[string]any{
				{"destination": "192.168.0.0/16", "networkEntityId": there},
			},
		})
		assert.Equal(t, http.StatusBadRequest, wrong.Code, "the far end's gateway serves another VCN")
	})

	t.Run("connect refuses a gateway that is already peered", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/localPeeringGateways/"+here+"/actions/connect",
			map[string]any{"peerId": there})
		assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	})

	t.Run("peerId is required", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/localPeeringGateways/"+here+"/actions/connect", map[string]any{})
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("list filters by compartment and VCN", func(t *testing.T) {
		all := decodeList(t, f.do(http.MethodGet, "/20160918/localPeeringGateways?compartmentId="+compartment, nil))
		assert.Len(t, all, 2)

		byVCN := decodeList(t, f.do(http.MethodGet,
			"/20160918/localPeeringGateways?compartmentId="+compartment+"&vcnId="+local, nil))
		require.Len(t, byVCN, 1)
		assert.Equal(t, here, byVCN[0]["id"])

		elsewhere := decodeList(t, f.do(http.MethodGet,
			"/20160918/localPeeringGateways?compartmentId="+otherCompartment, nil))
		assert.Empty(t, elsewhere)
	})

	t.Run("the VCN cannot be deleted while a gateway is on it", func(t *testing.T) {
		w := f.do(http.MethodDelete, "/20160918/vcns/"+remoteID, nil)
		assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	})

	t.Run("update renames and re-targets the route table", func(t *testing.T) {
		w := f.do(http.MethodPut, "/20160918/localPeeringGateways/"+here, map[string]any{
			"displayName": "renamed",
			"freeformTags": map[string]string{
				"env": "test",
			},
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		body := decode(t, w)
		assert.Equal(t, "renamed", body["displayName"])
		assert.Equal(t, "PEERED", body["peeringStatus"])
	})

	t.Run("deleting one end revokes the other", func(t *testing.T) {
		w := f.do(http.MethodDelete, "/20160918/localPeeringGateways/"+there, nil)
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

		got := decode(t, f.do(http.MethodGet, "/20160918/localPeeringGateways/"+here, nil))
		assert.Equal(t, "REVOKED", got["peeringStatus"])
		assert.Empty(t, got["peerId"])

		assert.Equal(t, http.StatusNotFound,
			f.do(http.MethodGet, "/20160918/localPeeringGateways/"+there, nil).Code)
	})

	t.Run("unknown action", func(t *testing.T) {
		w := f.do(http.MethodPost, "/20160918/localPeeringGateways/"+here+"/actions/explode", map[string]any{})
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// TestUnemulatedCollections covers the collections the handler claims in order
// to say it does not serve them.
func TestUnemulatedCollections(t *testing.T) {
	f := newFixture(t)

	for _, collection := range []string{"drgs", "drgAttachments", "remotePeeringConnections"} {
		t.Run(collection, func(t *testing.T) {
			w := f.do(http.MethodGet, "/20160918/"+collection+"?compartmentId="+compartment, nil)
			require.Equal(t, http.StatusNotImplemented, w.Code)

			body := decode(t, w)
			assert.Equal(t, "NotImplemented", body["code"])
			assert.Contains(t, body["message"], "localPeeringGateways")
		})
	}
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
