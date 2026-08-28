package serverkit

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/pricing"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// costLine is one always-on resource with its estimated monthly cost.
type costLine struct {
	Provider   string  `json:"provider"`
	Service    string  `json:"service"`
	Type       string  `json:"type"`
	ID         string  `json:"id"`
	MonthlyUSD float64 `json:"monthlyUsd"`
}

// serveCost answers GET /_cloudemu/cost with an estimated monthly cost of the
// current inventory (always-on resources only; usage-based services excluded).
func serveCost(w http.ResponseWriter, r *http.Request, engines map[string]*resourcediscovery.Engine) {
	var (
		lines     []costLine
		total     float64
		byService = map[string]float64{}
	)

	for prov, eng := range engines {
		if eng == nil {
			continue
		}

		res, err := eng.ListAll(r.Context())
		if err != nil {
			writeNetErr(w, http.StatusInternalServerError, err.Error())

			return
		}

		for i := range res {
			rr := &res[i]

			est := pricing.Monthly(rr.Provider, rr.Service, rr.Type, rr.SKU, rr.Region, rr.Properties)
			if est <= 0 {
				continue
			}

			lines = append(lines, costLine{Provider: prov, Service: rr.Service, Type: rr.Type, ID: rr.ID, MonthlyUSD: est})
			total += est
			byService[prov+"/"+rr.Service] += est
		}
	}

	writeNetJSON(w, map[string]any{
		"estimatedMonthlyUsd": total,
		"byService":           byService,
		"resources":           lines,
	})
}
