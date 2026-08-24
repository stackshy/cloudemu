package eventgrid

import (
	"sort"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// scopedRecord is a wire-handler-owned resource that knows its ARM scope, so a
// list operation can filter a store by subscription (and optionally resource
// group) and order the results deterministically.
type scopedRecord interface {
	scopeKey() (sub, rg, name string)
}

func (rec *systemTopicRecord) scopeKey() (sub, rg, name string) { return rec.sub, rec.rg, rec.name }
func (rec *domainRecord) scopeKey() (sub, rg, name string)      { return rec.sub, rec.rg, rec.name }

// scopedList converts each record to its ARM element, giving conv a ResourcePath
// re-scoped to that record's own resource group and name so ids and elements are
// built for the right resource even in a subscription-wide list.
func scopedList[T scopedRecord, J any](recs []T, rp *azurearm.ResourcePath, conv func(T, *azurearm.ResourcePath) J) []J {
	out := make([]J, 0, len(recs))

	for _, rec := range recs {
		_, rg, name := rec.scopeKey()
		scoped := *rp
		scoped.ResourceGroup = rg
		scoped.ResourceName = name
		out = append(out, conv(rec, &scoped))
	}

	return out
}

// recordsInScope returns the records in m that belong to subscription sub,
// filtered to resource group rg when rg is non-empty (an RG-scoped list), sorted
// by name.
func recordsInScope[T scopedRecord](m map[string]T, sub, rg string) []T {
	out := make([]T, 0, len(m))

	for _, rec := range m {
		recSub, recRG, _ := rec.scopeKey()
		if recSub != sub {
			continue
		}

		if rg != "" && recRG != rg {
			continue
		}

		out = append(out, rec)
	}

	sort.Slice(out, func(i, j int) bool {
		_, _, ni := out[i].scopeKey()
		_, _, nj := out[j].scopeKey()

		return ni < nj
	})

	return out
}
