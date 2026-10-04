package iam

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

const (
	trustAcct    = "123456789012"
	trustUserARN = "arn:aws:iam::" + trustAcct + ":user/alice"
	trustRoleARN = "arn:aws:iam::" + trustAcct + ":role/team/chain"
	trustSessARN = "arn:aws:sts::" + trustAcct + ":assumed-role/chain/s1"
)

func trustDoc(statements string) string {
	return `{"Version":"2012-10-17","Statement":[` + statements + `]}`
}

func allowStmt(principal string) string {
	return `{"Effect":"Allow","Principal":` + principal + `,"Action":"sts:AssumeRole"}`
}

func TestEvaluateTrust(t *testing.T) {
	user := []string{trustUserARN}
	session := []string{trustRoleARN, trustSessARN}

	cases := []struct {
		name    string
		doc     string
		callers []string
		account string
		action  string
		cctx    map[string]string
		want    driver.TrustResult
	}{
		{"exact user ARN is named directly", trustDoc(allowStmt(`{"AWS":"` + trustUserARN + `"}`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true, Allow: true, NamedDirectly: true}},
		{"account root ARN matches without naming", trustDoc(allowStmt(`{"AWS":"arn:aws:iam::` + trustAcct + `:root"}`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true, Allow: true}},
		{"bare account id matches without naming", trustDoc(allowStmt(`{"AWS":"` + trustAcct + `"}`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true, Allow: true}},
		{"another account's root does not match", trustDoc(allowStmt(`{"AWS":"arn:aws:iam::999999999999:root"}`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true}},
		{"another user does not match", trustDoc(allowStmt(`{"AWS":"arn:aws:iam::` + trustAcct + `:user/bob"}`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true}},
		{"AWS wildcard matches without naming", trustDoc(allowStmt(`{"AWS":"*"}`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true, Allow: true}},
		{"string wildcard matches without naming", trustDoc(allowStmt(`"*"`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true, Allow: true}},
		{"no ARN wildcarding", trustDoc(allowStmt(`{"AWS":"arn:aws:iam::` + trustAcct + `:user/*"}`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true}},
		{"Federated star never matches a signed caller", trustDoc(allowStmt(`{"Federated":"*"}`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true}},
		{"Service star never matches a signed caller", trustDoc(allowStmt(`{"Service":"*"}`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true}},
		{"CanonicalUser never matches", trustDoc(allowStmt(`{"CanonicalUser":"*"}`)),
			user, trustAcct, "", nil, driver.TrustResult{RoleExists: true}},
		{"role ARN names a role session", trustDoc(allowStmt(`{"AWS":"` + trustRoleARN + `"}`)),
			session, trustAcct, "", nil, driver.TrustResult{RoleExists: true, Allow: true, NamedDirectly: true}},
		{"explicit deny", trustDoc(allowStmt(`"*"`) + `,{"Effect":"Deny","Principal":{"AWS":"` + trustUserARN +
			`"},"Action":"sts:AssumeRole"}`), user, trustAcct, "", nil,
			driver.TrustResult{RoleExists: true, Allow: true, ExplicitDeny: true}},
		{"NotPrincipal deny hits a caller it does not list", trustDoc(allowStmt(`"*"`) +
			`,{"Effect":"Deny","NotPrincipal":{"AWS":["arn:aws:iam::` + trustAcct + `:user/bob","` + trustAcct + `"]},` +
			`"Action":"sts:AssumeRole"}`), user, trustAcct, "", nil,
			driver.TrustResult{RoleExists: true, Allow: true, ExplicitDeny: true}},
		{"NotPrincipal deny spares a caller it lists with the account", trustDoc(allowStmt(`"*"`) +
			`,{"Effect":"Deny","NotPrincipal":{"AWS":["` + trustUserARN + `","arn:aws:iam::` + trustAcct + `:root"]},` +
			`"Action":"sts:AssumeRole"}`), user, trustAcct, "", nil,
			driver.TrustResult{RoleExists: true, Allow: true}},
		{"NotPrincipal must list both the role and the session", trustDoc(allowStmt(`"*"`) +
			`,{"Effect":"Deny","NotPrincipal":{"AWS":["` + trustRoleARN + `","` + trustAcct + `"]},` +
			`"Action":"sts:AssumeRole"}`), session, trustAcct, "", nil,
			driver.TrustResult{RoleExists: true, Allow: true, ExplicitDeny: true}},
		{"NotPrincipal never grants", trustDoc(`{"Effect":"Allow","NotPrincipal":{"AWS":"arn:aws:iam::1:user/x"},` +
			`"Action":"sts:AssumeRole"}`), user, trustAcct, "", nil, driver.TrustResult{RoleExists: true}},
		{"ExternalId condition met", trustDoc(`{"Effect":"Allow","Principal":{"AWS":"` + trustUserARN + `"},` +
			`"Action":"sts:AssumeRole","Condition":{"StringEquals":{"sts:ExternalId":"x-1"}}}`), user, trustAcct, "",
			map[string]string{"sts:ExternalId": "x-1"}, driver.TrustResult{RoleExists: true, Allow: true, NamedDirectly: true}},
		{"ExternalId condition missing", trustDoc(`{"Effect":"Allow","Principal":{"AWS":"` + trustUserARN + `"},` +
			`"Action":"sts:AssumeRole","Condition":{"StringEquals":{"sts:ExternalId":"x-1"}}}`), user, trustAcct, "",
			nil, driver.TrustResult{RoleExists: true}},
		{"tag keys any value", trustDoc(`{"Effect":"Allow","Principal":"*","Action":"sts:TagSession",` +
			`"Condition":{"ForAnyValue:StringEquals":{"aws:TagKeys":"team"}}}`), user, trustAcct, "sts:TagSession",
			map[string]string{"aws:TagKeys": "env" + driver.ConditionValueSeparator + "team"},
			driver.TrustResult{RoleExists: true, Allow: true}},
		{"tag keys all values", trustDoc(`{"Effect":"Allow","Principal":"*","Action":"sts:TagSession",` +
			`"Condition":{"ForAllValues:StringEquals":{"aws:TagKeys":["team"]}}}`), user, trustAcct, "sts:TagSession",
			map[string]string{"aws:TagKeys": "env" + driver.ConditionValueSeparator + "team"},
			driver.TrustResult{RoleExists: true}},
		{"the statement must cover the action", trustDoc(allowStmt(`"*"`)), user, trustAcct, "sts:TagSession", nil,
			driver.TrustResult{RoleExists: true}},
		{"role tags are resource tags", trustDoc(`{"Effect":"Allow","Principal":"*","Action":"sts:AssumeRole",` +
			`"Condition":{"StringEquals":{"aws:ResourceTag/tier":"gold"}}}`), user, trustAcct, "", nil,
			driver.TrustResult{RoleExists: true, Allow: true}},
		{"malformed document allows nothing", "{", user, trustAcct, "", nil, driver.TrustResult{RoleExists: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			ctx := context.Background()

			_, err := m.CreateRole(ctx, driver.RoleConfig{
				Name: "target", AssumeRolePolicyDoc: tc.doc, Tags: map[string]string{"tier": "gold"},
			})
			requireNoError(t, err)

			action := tc.action
			if action == "" {
				action = "sts:AssumeRole"
			}

			got := m.EvaluateTrust(ctx, &driver.TrustRequest{
				RoleName: "target", Action: action, CallerARNs: tc.callers, CallerAccount: tc.account, Context: tc.cctx,
			})
			assertEqual(t, tc.want, got)
		})
	}
}

func TestEvaluateTrustMissingRole(t *testing.T) {
	m := newTestMock()

	got := m.EvaluateTrust(context.Background(), &driver.TrustRequest{
		RoleName: "ghost", Action: "sts:AssumeRole", CallerARNs: []string{trustUserARN}, CallerAccount: trustAcct,
	})
	assertEqual(t, driver.TrustResult{}, got)
}

// TestEvaluateAssumeRoleTrustLegacyUnchanged pins the auth-off evaluation: it
// ignores conditions and principal types, as it always has.
func TestEvaluateAssumeRoleTrustLegacyUnchanged(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateRole(ctx, driver.RoleConfig{
		Name: "legacy",
		AssumeRolePolicyDoc: trustDoc(`{"Effect":"Allow","Principal":{"AWS":"` + rootCaller + `"},` +
			`"Action":"sts:AssumeRole","Condition":{"StringEquals":{"sts:ExternalId":"x"}}}`),
	})
	requireNoError(t, err)

	_, allowed := m.EvaluateAssumeRoleTrust(ctx, "legacy", rootCaller)
	assertEqual(t, true, allowed)
}
