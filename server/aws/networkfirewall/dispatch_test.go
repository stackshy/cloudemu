package networkfirewall

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfmock "github.com/stackshy/cloudemu/v2/providers/aws/networkfirewall"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

// supportedOps is every Network Firewall operation the emulator implements
// (issue #1455 scope); the proxy, container-association, transit-gateway
// attachment and encryption families are documented non-goals.
//
//nolint:gochecknoglobals // immutable test fixture.
var supportedOps = []string{
	"CreateFirewall", "DescribeFirewall", "DeleteFirewall", "ListFirewalls",
	"CreateFirewallPolicy", "DescribeFirewallPolicy", "UpdateFirewallPolicy", "DeleteFirewallPolicy", "ListFirewallPolicies",
	"CreateRuleGroup", "DescribeRuleGroup", "UpdateRuleGroup", "DeleteRuleGroup", "ListRuleGroups",
	"AssociateFirewallPolicy", "AssociateSubnets", "DisassociateSubnets", "UpdateFirewallDeleteProtection",
	"UpdateLoggingConfiguration", "DescribeLoggingConfiguration", "TagResource", "UntagResource", "ListTagsForResource",
	"UpdateFirewallDescription", "UpdateSubnetChangeProtection", "UpdateFirewallPolicyChangeProtection",
	"UpdateAvailabilityZoneChangeProtection", "UpdateFirewallAnalysisSettings",
	"AssociateAvailabilityZones", "DisassociateAvailabilityZones",
	"PutResourcePolicy", "DescribeResourcePolicy", "DeleteResourcePolicy",
	"CreateTLSInspectionConfiguration", "DescribeTLSInspectionConfiguration", "UpdateTLSInspectionConfiguration",
	"DeleteTLSInspectionConfiguration", "ListTLSInspectionConfigurations",
	"CreateVpcEndpointAssociation", "DescribeVpcEndpointAssociation", "DeleteVpcEndpointAssociation", "ListVpcEndpointAssociations",
	"DescribeFirewallMetadata", "DescribeRuleGroupMetadata", "DescribeRuleGroupSummary",
	"StartFlowCapture", "StartFlowFlush", "DescribeFlowOperation", "ListFlowOperations", "ListFlowOperationResults",
	"StartAnalysisReport", "ListAnalysisReports", "GetAnalysisReportResults",
}

func newHandler() *Handler { return New(nfmock.New(config.NewOptions())) }

func call(h http.Handler, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if target != "" {
		req.Header.Set("X-Amz-Target", target)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestMatches_TargetPrefix_And_RoutesTable(t *testing.T) {
	h := newHandler()

	for _, tc := range []struct {
		target string
		want   bool
	}{
		{"NetworkFirewall_20201112.CreateFirewall", true},
		{"NetworkFirewall_20201112.", true},
		{"NetworkFirewall_2020.CreateFirewall", false},
		{"AWSKinesisService.ListStreams", false},
		{"", false},
	} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if tc.target != "" {
			req.Header.Set("X-Amz-Target", tc.target)
		}

		assert.Equal(t, tc.want, h.Matches(req), tc.target)
	}

	// A plain S3-style request is never claimed (the handler cannot shadow S3).
	assert.False(t, h.Matches(httptest.NewRequest(http.MethodGet, "/bucket/key", nil)))

	assert.Len(t, h.routes, len(supportedOps), "route table and supported-operation list agree")

	for _, op := range supportedOps {
		assert.Contains(t, h.routes, op)
	}

	rec := call(h, targetPrefix+"CreateProxy", "{}")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "InvalidRequestException")
	assert.Contains(t, rec.Body.String(), "unknown operation")

	assert.Equal(t, "network-firewall", h.IAMService())
}

func TestWriteErr_Mapping(t *testing.T) {
	typed := nfdriver.InvalidToken("stale")
	wrapped := errors.Join(errors.New("context"), typed) //nolint:err113 // test fixture

	for name, tc := range map[string]struct {
		err    error
		status int
		kind   string
	}{
		"typed":                {typed, http.StatusBadRequest, "InvalidTokenException"},
		"typed through a wrap": {wrapped, http.StatusBadRequest, "InvalidTokenException"},
		"invalid operation":    {nfdriver.InvalidOperation("x"), http.StatusBadRequest, "InvalidOperationException"},
		"resource policy":      {nfdriver.InvalidResourcePolicy("x"), http.StatusBadRequest, "InvalidResourcePolicyException"},
		"not found":            {cerrors.New(cerrors.NotFound, "x"), http.StatusBadRequest, "ResourceNotFoundException"},
		"already exists":       {cerrors.New(cerrors.AlreadyExists, "x"), http.StatusBadRequest, "InvalidRequestException"},
		"invalid argument":     {cerrors.New(cerrors.InvalidArgument, "x"), http.StatusBadRequest, "InvalidRequestException"},
		"failed precondition":  {cerrors.New(cerrors.FailedPrecondition, "x"), http.StatusBadRequest, "InvalidOperationException"},
		"unexpected":           {errors.New("boom"), http.StatusInternalServerError, "InternalServerError"}, //nolint:err113 // test fixture
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeErr(rec, tc.err)

			assert.Equal(t, tc.status, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.kind)
		})
	}
}

// baseOnly hides every optional capability of the wrapped driver: embedding the
// interface promotes only the base NetworkFirewall methods.
type baseOnly struct{ nfdriver.NetworkFirewall }

func TestOptionalCapabilities_UnsupportedWhenAbsent(t *testing.T) {
	h := New(baseOnly{nfmock.New(config.NewOptions())})

	for _, op := range []string{
		"UpdateFirewallDescription", "UpdateSubnetChangeProtection", "AssociateAvailabilityZones", "PutResourcePolicy",
		"CreateTLSInspectionConfiguration", "CreateVpcEndpointAssociation", "DescribeFirewallMetadata", "StartFlowCapture", "StartAnalysisReport",
	} {
		rec := call(h, targetPrefix+op, "{}")

		assert.Equal(t, http.StatusBadRequest, rec.Code, op)
		assert.Contains(t, rec.Body.String(), "UnsupportedOperationException", op)
	}

	// The base operations still work.
	rec := call(h, targetPrefix+"ListFirewalls", "{}")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestDecodeErrors_AreRequestErrors(t *testing.T) {
	h := newHandler()

	rec := call(h, targetPrefix+"CreateFirewall", "{not json")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDuplicateCreate_IsInvalidRequest(t *testing.T) {
	h := newHandler()
	body := `{"FirewallPolicyName":"p","FirewallPolicy":{"StatelessDefaultActions":["aws:pass"],"StatelessFragmentDefaultActions":["aws:pass"]}}`

	require.Equal(t, http.StatusOK, call(h, targetPrefix+"CreateFirewallPolicy", body).Code)

	rec := call(h, targetPrefix+"CreateFirewallPolicy", body)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "InvalidRequestException")
}

func TestRuleGroupBody_Resolution(t *testing.T) {
	for name, tc := range map[string]struct {
		group   string
		rules   string
		wantErr bool
		wantNil bool
	}{
		"both":    {`{"RulesSource":{}}`, "pass any", true, true},
		"neither": {"", "", false, true},
		"null":    {"null", "", false, true},
		"group":   {`{"RulesSource":{}}`, "", false, false},
		"rules":   {"", "pass any", false, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ruleGroupBody([]byte(tc.group), tc.rules)

			assert.Equal(t, tc.wantErr, err != nil)
			assert.Equal(t, tc.wantNil, got == nil)
		})
	}
}

// TestEveryOp_BadInput_IsRequestError sends every operation an empty body (no
// identifiers) and a malformed body: neither may succeed, panic or answer 5xx.
func TestEveryOp_BadInput_IsRequestError(t *testing.T) {
	h := newHandler()

	// Operations with no required identifier answer an empty body successfully.
	listOps := map[string]bool{
		"ListFirewalls": true, "ListFirewallPolicies": true, "ListRuleGroups": true,
		"ListTLSInspectionConfigurations": true, "ListVpcEndpointAssociations": true,
	}

	for _, op := range supportedOps {
		t.Run(op, func(t *testing.T) {
			rec := call(h, targetPrefix+op, "{}")

			if listOps[op] {
				assert.Equal(t, http.StatusOK, rec.Code)
			} else {
				assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			}

			bad := call(h, targetPrefix+op, "{not json")
			assert.Equal(t, http.StatusBadRequest, bad.Code)
		})
	}
}
