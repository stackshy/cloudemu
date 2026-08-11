package guardduty

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// Coverage filter/sort criterion keys accepted by ListCoverage and
// GetCoverageStatistics.
const (
	coverageKeyResourceType = "RESOURCE_TYPE"
	coverageKeyStatus       = "COVERAGE_STATUS"
)

// coverageResource is a synthetic per-detector coverage entry. GuardDuty reports
// coverage per protected resource (EKS/ECS/EC2); the emulator synthesizes one of
// each so ListCoverage and GetCoverageStatistics return well-formed data.
type coverageResource struct {
	resourceID   string
	resourceType string
	status       string
}

// syntheticCoverage returns the fixed coverage-resource set the emulator reports
// for a detector, ordered deterministically by resourceId.
func (*Mock) syntheticCoverage(detectorID string) []coverageResource {
	return []coverageResource{
		{resourceID: "eks-" + detectorID, resourceType: "EKS", status: "HEALTHY"},
		{resourceID: "ecs-" + detectorID, resourceType: "ECS", status: "HEALTHY"},
		{resourceID: "ec2-" + detectorID, resourceType: "EC2", status: "HEALTHY"},
	}
}

// coverageFilterRequest is the CoverageFilterCriteria request body.
type coverageFilterRequest struct {
	FilterCriterion []coverageCriterionRequest `json:"filterCriterion"`
}

// coverageCriterionRequest is one CoverageFilterCriterion.
type coverageCriterionRequest struct {
	CriterionKey    string                    `json:"criterionKey"`
	FilterCondition *coverageConditionRequest `json:"filterCondition"`
}

// coverageConditionRequest is a CoverageFilterCondition (equals/notEquals only).
type coverageConditionRequest struct {
	Equals    []string `json:"equals"`
	NotEquals []string `json:"notEquals"`
}

// coverageSortRequest is the CoverageSortCriteria request body.
type coverageSortRequest struct {
	AttributeName string `json:"attributeName"`
	OrderBy       string `json:"orderBy"`
}

// listCoverageRequest is the ListCoverage request body.
type listCoverageRequest struct {
	FilterCriteria *coverageFilterRequest `json:"filterCriteria"`
	SortCriteria   *coverageSortRequest   `json:"sortCriteria"`
	MaxResults     *int32                 `json:"maxResults"`
	NextToken      string                 `json:"nextToken"`
}

// ListCoverage returns the synthetic coverage resources honoring FilterCriteria,
// SortCriteria, and pagination.
func (m *Mock) ListCoverage(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	updatedAt, err := m.detectorCreatedAt(detectorID)
	if err != nil {
		return nil, err
	}

	var req listCoverageRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	filtered := filterCoverage(m.syntheticCoverage(detectorID), req.FilterCriteria)
	sortCoverage(filtered, req.SortCriteria)

	page := driver.Page{NextToken: req.NextToken, MaxResults: int32Deref(req.MaxResults)}

	pageRes, next, perr := paginateCoverage(filtered, page)
	if perr != nil {
		return nil, perr
	}

	wire := make([]map[string]any, 0, len(pageRes))
	for i := range pageRes {
		wire = append(wire, m.coverageToWire(detectorID, pageRes[i], updatedAt))
	}

	return json.Marshal(withNextToken(map[string]any{"resources": wire}, next))
}

// detectorCreatedAt returns the detector's stored creation time (used as the
// deterministic coverage updatedAt), or an error if the detector is absent.
func (m *Mock) detectorCreatedAt(detectorID string) (time.Time, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return time.Time{}, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	return dd.detector.CreatedAt, nil
}

// coverageToWire renders a coverage resource as its REST-JSON object. updatedAt
// is the detector's stored creation time so repeated reads are deterministic.
func (m *Mock) coverageToWire(detectorID string, cr coverageResource, updatedAt time.Time) map[string]any {
	return map[string]any{
		"accountId":       m.opts.AccountID,
		"detectorId":      detectorID,
		"resourceId":      cr.resourceID,
		"coverageStatus":  cr.status,
		"updatedAt":       updatedAt.UTC().Unix(),
		"resourceDetails": map[string]any{"resourceType": cr.resourceType},
	}
}

// getCoverageStatisticsRequest is the GetCoverageStatistics request body.
type getCoverageStatisticsRequest struct {
	StatisticsType []string               `json:"statisticsType"`
	FilterCriteria *coverageFilterRequest `json:"filterCriteria"`
}

// GetCoverageStatistics returns coverage counts by status and/or resource type for
// the requested statistics types, honoring FilterCriteria.
func (m *Mock) GetCoverageStatistics(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	if _, err := m.getDetector(detectorID); err != nil {
		return nil, err
	}

	var req getCoverageStatisticsRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	filtered := filterCoverage(m.syntheticCoverage(detectorID), req.FilterCriteria)
	stats := map[string]any{}

	if contains(req.StatisticsType, "COUNT_BY_COVERAGE_STATUS") {
		stats["countByCoverageStatus"] = countBy(filtered, func(c coverageResource) string { return c.status })
	}

	if contains(req.StatisticsType, "COUNT_BY_RESOURCE_TYPE") {
		stats["countByResourceType"] = countBy(filtered, func(c coverageResource) string { return c.resourceType })
	}

	return json.Marshal(map[string]any{"coverageStatistics": stats})
}

// countBy groups coverage resources by a key function, returning label→count. The
// counts are int64 to match the CoverageStatistics wire shape.
func countBy(rs []coverageResource, key func(coverageResource) string) map[string]int64 {
	out := map[string]int64{}
	for i := range rs {
		out[key(rs[i])]++
	}

	return out
}

// filterCoverage applies a CoverageFilterCriteria (RESOURCE_TYPE / COVERAGE_STATUS
// / ACCOUNT_ID) to the coverage set. An unrecognized criterion key is ignored, but
// a recognized one is enforced so a filter is never silently dropped.
func filterCoverage(rs []coverageResource, fc *coverageFilterRequest) []coverageResource {
	if fc == nil || len(fc.FilterCriterion) == 0 {
		return rs
	}

	out := make([]coverageResource, 0, len(rs))

	for i := range rs {
		if coverageMatches(rs[i], fc.FilterCriterion) {
			out = append(out, rs[i])
		}
	}

	return out
}

// coverageMatches reports whether a coverage resource satisfies every criterion.
func coverageMatches(cr coverageResource, crit []coverageCriterionRequest) bool {
	for i := range crit {
		got, known := coverageField(cr, crit[i].CriterionKey)
		if !known {
			continue
		}

		if !coverageConditionMatches(got, crit[i].FilterCondition) {
			return false
		}
	}

	return true
}

// coverageField returns the coverage field for a criterion key and whether the key
// is one the emulator can evaluate.
func coverageField(cr coverageResource, key string) (string, bool) {
	switch key {
	case coverageKeyResourceType:
		return cr.resourceType, true
	case coverageKeyStatus:
		return cr.status, true
	default:
		return "", false
	}
}

// coverageConditionMatches applies an equals/notEquals condition.
func coverageConditionMatches(got string, cond *coverageConditionRequest) bool {
	if cond == nil {
		return true
	}

	if len(cond.Equals) > 0 && !contains(cond.Equals, got) {
		return false
	}

	if len(cond.NotEquals) > 0 && contains(cond.NotEquals, got) {
		return false
	}

	return true
}

// sortCoverage orders coverage resources by the requested attribute (default and
// fallback: resourceId ascending).
func sortCoverage(rs []coverageResource, sc *coverageSortRequest) {
	desc := sc != nil && strings.EqualFold(sc.OrderBy, "DESC")
	attr := ""

	if sc != nil {
		attr = sc.AttributeName
	}

	sort.SliceStable(rs, func(i, j int) bool {
		a, b := coverageSortKey(rs[i], attr), coverageSortKey(rs[j], attr)
		if desc {
			return a > b
		}

		return a < b
	})
}

// coverageSortKey returns the sort key string for a coverage resource by attribute.
func coverageSortKey(cr coverageResource, attr string) string {
	switch attr {
	case coverageKeyStatus:
		return cr.status
	case coverageKeyResourceType:
		return cr.resourceType
	default:
		return cr.resourceID
	}
}

// paginateCoverage returns a deterministic page of coverage resources honoring the
// numeric offset token, mirroring paginateIDs' semantics.
func paginateCoverage(rs []coverageResource, page driver.Page) (out []coverageResource, next string, err error) {
	return paginateSlice(rs, page)
}
