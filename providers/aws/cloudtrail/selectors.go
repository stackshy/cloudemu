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

	td.selors = copyEventSelectors(sel)
	td.advSel = copyAdvSelectors(adv)
	td.trail.HasCustomEventSelectors = len(sel) > 0 || len(adv) > 0

	return td.trail.TrailARN, copyEventSelectors(td.selors), copyAdvSelectors(td.advSel), nil
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
