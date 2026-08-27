package cloudtrail

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// PutEventSelectors sets a trail's basic or advanced event selectors. Basic and
// advanced selectors are mutually exclusive, matching CloudTrail.
func (m *Mock) PutEventSelectors(
	_ context.Context, trailName string, sel []driver.EventSelector, adv []driver.AdvancedEventSelector,
) (string, []driver.EventSelector, []driver.AdvancedEventSelector, error) {
	if len(sel) > 0 && len(adv) > 0 {
		return "", nil, nil, errInvalidParameter(
			"cannot set both EventSelectors and AdvancedEventSelectors on a trail")
	}

	td, err := m.resolveTrail(trailName)
	if err != nil {
		return "", nil, nil, err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	td.selors = normalizeEventSelectors(copyEventSelectors(sel))
	td.advSel = copyAdvSelectors(adv)
	td.trail.HasCustomEventSelectors = len(sel) > 0 || len(adv) > 0

	return td.trail.TrailARN, copyEventSelectors(td.selors), copyAdvSelectors(td.advSel), nil
}

// normalizeEventSelectors applies CloudTrail's basic-selector field defaults:
// an omitted ReadWriteType defaults to "All" and an omitted
// IncludeManagementEvents defaults to true, so they read back the way AWS
// stores them.
func normalizeEventSelectors(sel []driver.EventSelector) []driver.EventSelector {
	for i := range sel {
		if sel[i].ReadWriteType == "" {
			sel[i].ReadWriteType = readWriteAll
		}

		if sel[i].IncludeManagementEvents == nil {
			include := true
			sel[i].IncludeManagementEvents = &include
		}
	}

	return sel
}

// defaultEventSelectors is the implicit selector CloudTrail reports for a trail
// created without explicit selectors: one basic selector logging all read and
// write management events, and no data events.
func defaultEventSelectors() []driver.EventSelector {
	include := true

	return []driver.EventSelector{{
		ReadWriteType:                 readWriteAll,
		IncludeManagementEvents:       &include,
		DataResources:                 []driver.DataResource{},
		ExcludeManagementEventSources: []string{},
	}}
}

// GetEventSelectors returns a trail's event selectors.
func (m *Mock) GetEventSelectors(
	_ context.Context, trailName string,
) (string, []driver.EventSelector, []driver.AdvancedEventSelector, error) {
	td, err := m.resolveTrail(trailName)
	if err != nil {
		return "", nil, nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	// A trail with no explicit selectors reports CloudTrail's implicit default:
	// one basic selector logging all management events.
	if len(td.selors) == 0 && len(td.advSel) == 0 {
		return td.trail.TrailARN, defaultEventSelectors(), nil, nil
	}

	return td.trail.TrailARN, copyEventSelectors(td.selors), copyAdvSelectors(td.advSel), nil
}

// PutInsightSelectors sets insight selectors on a trail or event data store.
//
//nolint:gocritic // trailARN/edsARN results are self-documenting from the doc comment.
func (m *Mock) PutInsightSelectors(
	_ context.Context, trailName, edsARN string, sel []driver.InsightSelector,
) (string, string, []driver.InsightSelector, error) {
	if edsARN != "" {
		ed, err := m.resolveEDS(edsARN)
		if err != nil {
			return "", "", nil, err
		}

		ed.mu.Lock()
		defer ed.mu.Unlock()

		ed.insights = copyInsightSelectors(sel)

		return "", ed.eds.ARN, copyInsightSelectors(ed.insights), nil
	}

	td, err := m.resolveTrail(trailName)
	if err != nil {
		return "", "", nil, err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	td.insights = copyInsightSelectors(sel)
	td.trail.HasInsightSelectors = len(sel) > 0

	return td.trail.TrailARN, "", copyInsightSelectors(td.insights), nil
}

// GetInsightSelectors returns insight selectors for a trail or event data store.
//
//nolint:gocritic // trailARN/edsARN results are self-documenting from the doc comment.
func (m *Mock) GetInsightSelectors(
	_ context.Context, trailName, edsARN string,
) (string, string, []driver.InsightSelector, error) {
	if edsARN != "" {
		ed, err := m.resolveEDS(edsARN)
		if err != nil {
			return "", "", nil, err
		}

		ed.mu.RLock()
		defer ed.mu.RUnlock()

		return "", ed.eds.ARN, copyInsightSelectors(ed.insights), nil
	}

	td, err := m.resolveTrail(trailName)
	if err != nil {
		return "", "", nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	return td.trail.TrailARN, "", copyInsightSelectors(td.insights), nil
}
