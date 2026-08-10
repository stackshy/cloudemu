package cloudtrail

import (
	"time"

	ctdriver "github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value,omitempty"`
}

func tagsToMap(tags []tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func mapToTags(m map[string]string) []tag {
	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{Key: k, Value: v})
	}

	return out
}

func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// --- selectors (wire shapes) ---

type dataResourceJSON struct {
	Type   string   `json:"Type,omitempty"`
	Values []string `json:"Values,omitempty"`
}

type eventSelectorJSON struct {
	ReadWriteType                 string             `json:"ReadWriteType,omitempty"`
	IncludeManagementEvents       *bool              `json:"IncludeManagementEvents,omitempty"`
	DataResources                 []dataResourceJSON `json:"DataResources,omitempty"`
	ExcludeManagementEventSources []string           `json:"ExcludeManagementEventSources,omitempty"`
}

type advancedFieldSelectorJSON struct {
	Field         string   `json:"Field"`
	Equals        []string `json:"Equals,omitempty"`
	NotEquals     []string `json:"NotEquals,omitempty"`
	StartsWith    []string `json:"StartsWith,omitempty"`
	NotStartsWith []string `json:"NotStartsWith,omitempty"`
	EndsWith      []string `json:"EndsWith,omitempty"`
	NotEndsWith   []string `json:"NotEndsWith,omitempty"`
}

type advancedEventSelectorJSON struct {
	Name           string                      `json:"Name,omitempty"`
	FieldSelectors []advancedFieldSelectorJSON `json:"FieldSelectors"`
}

type insightSelectorJSON struct {
	InsightType string `json:"InsightType,omitempty"`
}

func eventSelectorsToWire(in []ctdriver.EventSelector) []eventSelectorJSON {
	out := make([]eventSelectorJSON, 0, len(in))

	for i := range in {
		s := in[i]
		j := eventSelectorJSON{
			ReadWriteType:                 s.ReadWriteType,
			IncludeManagementEvents:       s.IncludeManagementEvents,
			ExcludeManagementEventSources: s.ExcludeManagementEventSources,
		}

		for _, dr := range s.DataResources {
			j.DataResources = append(j.DataResources, dataResourceJSON{Type: dr.Type, Values: dr.Values})
		}

		out = append(out, j)
	}

	return out
}

func eventSelectorsFromWire(in []eventSelectorJSON) []ctdriver.EventSelector {
	out := make([]ctdriver.EventSelector, 0, len(in))

	for i := range in {
		s := in[i]
		d := ctdriver.EventSelector{
			ReadWriteType:                 s.ReadWriteType,
			IncludeManagementEvents:       s.IncludeManagementEvents,
			ExcludeManagementEventSources: s.ExcludeManagementEventSources,
		}

		for _, dr := range s.DataResources {
			d.DataResources = append(d.DataResources, ctdriver.DataResource{Type: dr.Type, Values: dr.Values})
		}

		out = append(out, d)
	}

	return out
}

func advSelectorsToWire(in []ctdriver.AdvancedEventSelector) []advancedEventSelectorJSON {
	out := make([]advancedEventSelectorJSON, 0, len(in))

	for i := range in {
		s := in[i]
		j := advancedEventSelectorJSON{Name: s.Name}

		for k := range s.FieldSelectors {
			fs := &s.FieldSelectors[k]
			j.FieldSelectors = append(j.FieldSelectors, advancedFieldSelectorJSON{
				Field: fs.Field, Equals: fs.Equals, NotEquals: fs.NotEquals,
				StartsWith: fs.StartsWith, NotStartsWith: fs.NotStartsWith,
				EndsWith: fs.EndsWith, NotEndsWith: fs.NotEndsWith,
			})
		}

		out = append(out, j)
	}

	return out
}

func advSelectorsFromWire(in []advancedEventSelectorJSON) []ctdriver.AdvancedEventSelector {
	out := make([]ctdriver.AdvancedEventSelector, 0, len(in))

	for i := range in {
		s := in[i]
		d := ctdriver.AdvancedEventSelector{Name: s.Name}

		for k := range s.FieldSelectors {
			fs := &s.FieldSelectors[k]
			d.FieldSelectors = append(d.FieldSelectors, ctdriver.AdvancedFieldSelector{
				Field: fs.Field, Equals: fs.Equals, NotEquals: fs.NotEquals,
				StartsWith: fs.StartsWith, NotStartsWith: fs.NotStartsWith,
				EndsWith: fs.EndsWith, NotEndsWith: fs.NotEndsWith,
			})
		}

		out = append(out, d)
	}

	return out
}

func insightSelectorsToWire(in []ctdriver.InsightSelector) []insightSelectorJSON {
	out := make([]insightSelectorJSON, 0, len(in))
	for _, s := range in {
		out = append(out, insightSelectorJSON{InsightType: s.InsightType})
	}

	return out
}

func insightSelectorsFromWire(in []insightSelectorJSON) []ctdriver.InsightSelector {
	out := make([]ctdriver.InsightSelector, 0, len(in))
	for _, s := range in {
		out = append(out, ctdriver.InsightSelector{InsightType: s.InsightType})
	}

	return out
}

func destinationsToWire(in []ctdriver.Destination) []destinationJSON {
	out := make([]destinationJSON, 0, len(in))
	for _, d := range in {
		out = append(out, destinationJSON{Type: d.Type, Location: d.Location})
	}

	return out
}

func destinationsFromWire(in []destinationJSON) []ctdriver.Destination {
	out := make([]ctdriver.Destination, 0, len(in))
	for _, d := range in {
		out = append(out, ctdriver.Destination{Type: d.Type, Location: d.Location})
	}

	return out
}

type destinationJSON struct {
	Type     string `json:"Type,omitempty"`
	Location string `json:"Location,omitempty"`
}
