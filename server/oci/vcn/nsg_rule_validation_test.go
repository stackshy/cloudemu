package vcn_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tcpRule(minPort, maxPort int) map[string]any {
	return map[string]any{
		"direction":  "INGRESS",
		"protocol":   "6",
		"source":     "10.0.0.0/16",
		"sourceType": "CIDR_BLOCK",
		"tcpOptions": map[string]any{"destinationPortRange": map[string]int{"min": minPort, "max": maxPort}},
	}
}

// TestNSGRuleBatchesAreAtomic checks that a bad port rejects an add or update
// request before any rule is added or removed.
func TestNSGRuleBatchesAreAtomic(t *testing.T) {
	f := newFixture(t)
	vcnID := f.newVCN()

	w := f.do(http.MethodPost, "/20160918/networkSecurityGroups", map[string]any{
		"compartmentId": compartment, "vcnId": vcnID, "displayName": "web",
	})
	require.Equal(t, http.StatusOK, w.Code)

	nsgID, _ := decode(t, w)["id"].(string)
	base := "/20160918/networkSecurityGroups/" + nsgID

	added := f.do(http.MethodPost, base+"/actions/addSecurityRules",
		map[string]any{"securityRules": []map[string]any{tcpRule(443, 443)}})
	require.Equal(t, http.StatusOK, added.Code)

	rules, _ := decode(t, added)["securityRules"].([]any)
	require.Len(t, rules, 1)

	first, _ := rules[0].(map[string]any)
	ruleID, _ := first["id"].(string)

	bad := f.do(http.MethodPost, base+"/actions/addSecurityRules",
		map[string]any{"securityRules": []map[string]any{tcpRule(22, 22), tcpRule(1, 70000)}})
	assert.Equal(t, http.StatusBadRequest, bad.Code, bad.Body.String())
	assert.Len(t, decodeList(t, f.do(http.MethodGet, base+"/securityRules", nil)), 1)

	update := tcpRule(80, 70000)
	update["id"] = ruleID

	badUpdate := f.do(http.MethodPost, base+"/actions/updateSecurityRules",
		map[string]any{"securityRules": []map[string]any{update}})
	assert.Equal(t, http.StatusBadRequest, badUpdate.Code, badUpdate.Body.String())

	listed := decodeList(t, f.do(http.MethodGet, base+"/securityRules", nil))
	require.Len(t, listed, 1, "a rejected update must keep the original rule")
	assert.Equal(t, ruleID, listed[0]["id"])

	good := tcpRule(80, 80)
	good["id"] = ruleID
	missing := tcpRule(81, 81)
	missing["id"] = "deadbeef"

	missingUpdate := f.do(http.MethodPost, base+"/actions/updateSecurityRules",
		map[string]any{"securityRules": []map[string]any{good, missing}})
	assert.Equal(t, http.StatusNotFound, missingUpdate.Code)

	listed = decodeList(t, f.do(http.MethodGet, base+"/securityRules", nil))
	require.Len(t, listed, 1)
	assert.Equal(t, ruleID, listed[0]["id"], "a rejected update must not replace the first rule")
}
