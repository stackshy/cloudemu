package functions

import (
	"regexp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// codeBadRequest is the ARM error code real Azure answers an OS change on an
// existing plan with (Azure/bicep#5724: 400 BadRequest, "You cannot change the
// OS hosting your app at this time. Please recreate your app with the desired
// OS.").
const codeBadRequest = "BadRequest"

// PlanError is an App Service plan refusal that real Azure reports with a
// specific ARM error code. It unwraps to the canonical cloudemu error, so a
// caller that only checks the code still sees it; the wire layer echoes Code.
type PlanError struct {
	Code string
	Err  error
}

// Error implements the error interface.
func (e *PlanError) Error() string { return e.Err.Error() }

// Unwrap returns the canonical cloudemu error this one stands for.
func (e *PlanError) Unwrap() error { return e.Err }

// knownPlanSKU matches the App Service plan SKU names a plan can be moved to
// and whose tier deriveSKUTier maps correctly: Free, Shared, Basic, Standard,
// Premium (v1/v2/v3), Isolated (v1/v2), Consumption, Elastic Premium and
// Workflow Standard.
var knownPlanSKU = regexp.MustCompile(
	`^(F1|D1|B[1-3]|S[1-3]|P[1-3]|P[1-3]V2|P[0-3]V3|I[1-3]|I[1-6]V2|Y1|EP[1-3]|WS[1-3])$`)

// maxPlanCapacity is the largest sku.capacity (instance count) of a dedicated
// tier, from the App Service limits (Azure subscription and service limits):
// Free and Shared 1, Basic 3, Standard 10, Premium 30, Isolated 100. Other
// tiers are not capped here.
var maxPlanCapacity = map[string]int{ //nolint:gochecknoglobals // lookup table
	tierFree: 1, tierShared: 1, tierBasic: 3, tierStandard: 10,
	tierPremium: 30, tierPremiumV2: 30, tierPremiumV3: 30,
	tierIsolated: 100, tierIsolatedV2: 100,
}

// checkPlanUpdate validates replacing plan cur with next (a PATCH or a
// re-PUT). Only what changes is checked, so a plan created with an unusual
// SKU can still be re-PUT unchanged.
func checkPlanUpdate(cur, next *AppServicePlan) error {
	if err := checkPlanOS(cur, next); err != nil {
		return err
	}

	skuChanged := !strings.EqualFold(cur.SKUName, next.SKUName)

	if skuChanged {
		if err := checkPlanSKUMove(cur, next); err != nil {
			return err
		}
	}

	if skuChanged || !strings.EqualFold(cur.SKUTier, next.SKUTier) {
		if want := deriveSKUTier(next.SKUName); !strings.EqualFold(next.SKUTier, want) {
			return cerrors.Newf(cerrors.InvalidArgument,
				"The sku tier '%s' does not match sku name '%s' (tier '%s').", next.SKUTier, next.SKUName, want)
		}
	}

	if skuChanged || cur.Capacity != next.Capacity {
		return checkPlanCapacity(next)
	}

	return nil
}

// checkPlanOS refuses a change of the plan's OS: reserved (true for Linux),
// or a kind that moves between Linux and Windows. An omitted kind (a re-PUT
// without one) is not a change of OS.
func checkPlanOS(cur, next *AppServicePlan) error {
	if cur.Reserved == next.Reserved && (next.Kind == "" || linuxKind(cur.Kind) == linuxKind(next.Kind)) {
		return nil
	}

	return &PlanError{Code: codeBadRequest, Err: cerrors.New(cerrors.InvalidArgument,
		"You cannot change the OS hosting your app at this time. Please recreate your app with the desired OS.")}
}

// checkPlanSKUMove rejects an unknown SKU, and a move between the
// Consumption, Elastic Premium, Workflow Standard and dedicated families: a
// plan's hosting model is fixed at create.
func checkPlanSKUMove(cur, next *AppServicePlan) error {
	if !knownPlanSKU.MatchString(strings.ToUpper(next.SKUName)) {
		return cerrors.Newf(cerrors.InvalidArgument, "The pricing tier '%s' is not allowed for this App Service plan.",
			next.SKUName)
	}

	from, to := planFamily(cur.SKUTier), planFamily(deriveSKUTier(next.SKUName))
	if from != to {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Cannot change the App Service plan from %s (%s) to %s (%s). Create a new plan instead.",
			cur.SKUName, cur.SKUTier, next.SKUName, deriveSKUTier(next.SKUName))
	}

	return nil
}

// checkPlanCapacity rejects an instance count below 1 or above the tier's
// maximum.
func checkPlanCapacity(p *AppServicePlan) error {
	if p.Capacity < 1 {
		return cerrors.Newf(cerrors.InvalidArgument, "Invalid sku.capacity %d: it must be at least 1.", p.Capacity)
	}

	if limit, ok := maxPlanCapacity[p.SKUTier]; ok && p.Capacity > limit {
		return cerrors.Newf(cerrors.InvalidArgument,
			"Invalid sku.capacity %d: the %s tier allows at most %d instances.", p.Capacity, p.SKUTier, limit)
	}

	return nil
}

// planFamily groups plan tiers by hosting model.
func planFamily(tier string) string {
	switch {
	case strings.EqualFold(tier, tierDynamic):
		return tierDynamic
	case strings.EqualFold(tier, tierElasticPremium):
		return tierElasticPremium
	case strings.EqualFold(tier, tierWorkflowStandard):
		return tierWorkflowStandard
	default:
		return "Dedicated"
	}
}

// linuxKind reports whether a plan kind names a Linux plan ("linux",
// "functionapp,linux", ...).
func linuxKind(kind string) bool {
	return strings.Contains(strings.ToLower(kind), "linux")
}
