package wafv2

import (
	"context"
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	wafdriver "github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

// This file implements WAFv2's read-only catalog and analytics operations. They
// depend on AWS-managed vendor catalogs (managed rule groups/products, mobile
// SDK releases, managed rule sets) or on live request traffic and monetization
// data (sampled requests, top-path/revenue statistics, settlement records) that
// the emulator does not model. Each returns a plausible empty (or, where the SDK
// requires a value, synthesized) result so SDK/CLI calls succeed and round-trip.
// See docs/services.md for the parity note.

const mobileSdkReleaseURLTemplate = "https://waf-mobile-sdk.s3.amazonaws.com/emulated/"

func emptyRaw() []json.RawMessage { return []json.RawMessage{} }

// nonexistentManagedRuleSet reports that a customer-managed rule set is absent.
// The emulator does not host managed rule sets (those belong to AWS-managed and
// Marketplace vendors), so Get/Put/Update against them return WAFNonexistent.
func nonexistentManagedRuleSet(name string) error {
	return &wafdriver.APIError{
		Exception: wafdriver.ExNonexistentItem,
		Err:       cerrors.Newf(cerrors.NotFound, "managed rule set %q not found", name),
	}
}

func (h *Handler) describeAllManagedProducts(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *scopeRequest) (any, error) {
		return describeManagedProductsResponse{ManagedProducts: emptyRaw()}, nil
	})
}

func (h *Handler) describeManagedProductsByVendor(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *describeManagedProductsByVendorRequest) (any, error) {
		return describeManagedProductsResponse{ManagedProducts: emptyRaw()}, nil
	})
}

func (h *Handler) describeManagedRuleGroup(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, req *describeManagedRuleGroupRequest) (any, error) {
		return describeManagedRuleGroupResponse{
			AvailableLabels: emptyRaw(), ConsumedLabels: emptyRaw(), Rules: emptyRaw(),
			VersionName: req.VersionName,
		}, nil
	})
}

func (h *Handler) generateMobileSdkReleaseURL(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, req *generateMobileSdkReleaseURLRequest) (any, error) {
		url := mobileSdkReleaseURLTemplate + req.Platform + "/" + req.ReleaseVersion

		return generateMobileSdkReleaseURLResponse{URL: url}, nil
	})
}

func (h *Handler) getMobileSdkRelease(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *getMobileSdkReleaseRequest) (any, error) {
		return getMobileSdkReleaseResponse{}, nil
	})
}

func (h *Handler) listMobileSdkReleases(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *listMobileSdkReleasesRequest) (any, error) {
		return listMobileSdkReleasesResponse{ReleaseSummaries: emptyRaw()}, nil
	})
}

func (h *Handler) listAvailableManagedRuleGroups(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *scopeRequest) (any, error) {
		return listAvailableManagedRuleGroupsResponse{ManagedRuleGroups: emptyRaw()}, nil
	})
}

func (h *Handler) listAvailableManagedRuleGroupVersions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *listAvailableManagedRuleGroupVersionsRequest) (any, error) {
		return listAvailableManagedRuleGroupVersionsResponse{Versions: emptyRaw()}, nil
	})
}

func (h *Handler) listManagedRuleSets(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *scopeRequest) (any, error) {
		return listManagedRuleSetsResponse{ManagedRuleSets: emptyRaw()}, nil
	})
}

func (h *Handler) getManagedRuleSet(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, req *getManagedRuleSetRequest) (any, error) {
		return nil, nonexistentManagedRuleSet(req.Name)
	})
}

func (h *Handler) putManagedRuleSetVersions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, req *putManagedRuleSetVersionsRequest) (any, error) {
		return nil, nonexistentManagedRuleSet(req.Name)
	})
}

func (h *Handler) updateManagedRuleSetVersionExpiryDate(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, req *updateManagedRuleSetVersionExpiryDateRequest) (any, error) {
		return nil, nonexistentManagedRuleSet(req.Name)
	})
}

func (h *Handler) getRateBasedStatementManagedKeys(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *refRequest) (any, error) {
		empty := rateBasedManagedKeysJSON{Addresses: []string{}}

		return getRateBasedStatementManagedKeysResponse{ManagedKeysIPV4: empty, ManagedKeysIPV6: empty}, nil
	})
}

func (h *Handler) getSampledRequests(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, req *getSampledRequestsRequest) (any, error) {
		return getSampledRequestsResponse{SampledRequests: emptyRaw(), TimeWindow: req.TimeWindow}, nil
	})
}

func (h *Handler) getTopPathStatisticsByTraffic(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *scopeRequest) (any, error) {
		return getTopPathStatisticsByTrafficResponse{PathStatistics: emptyRaw(), TopCategories: emptyRaw()}, nil
	})
}

func (h *Handler) getRevenueStatistics(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *scopeRequest) (any, error) {
		return getRevenueStatisticsResponse{RevenuePathStatistics: emptyRaw(), SourceStatistics: emptyRaw()}, nil
	})
}

func (h *Handler) getRevenueStatisticsSummary(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *scopeRequest) (any, error) {
		return getRevenueStatisticsSummaryResponse{}, nil
	})
}

func (h *Handler) getRevenueStatisticsTimeSeries(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *scopeRequest) (any, error) {
		return getRevenueStatisticsTimeSeriesResponse{DataPoints: emptyRaw()}, nil
	})
}

func (h *Handler) listSettlementRecords(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, _ *scopeRequest) (any, error) {
		return listSettlementRecordsResponse{Settlements: emptyRaw()}, nil
	})
}

func (h *Handler) deleteFirewallManagerRuleGroups(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(_ *Handler, _ context.Context, req *deleteFirewallManagerRuleGroupsRequest) (any, error) {
		// Firewall Manager isn't emulated; there are no FM-managed rule groups to
		// remove, so echo back the presented lock token as the next token.
		return deleteFirewallManagerRuleGroupsResponse{NextWebACLLockToken: req.WebACLLockToken}, nil
	})
}
