package location

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

func (h *Handler) routeCalculatorResource() *resource {
	return &resource{
		coll:     "calculators",
		listPath: "list-calculators",
		create:   h.createRouteCalculator,
		list:     h.listRouteCalculators,
		describe: h.describeRouteCalculator,
		update:   h.updateRouteCalculator,
		del:      h.deleteRouteCalculator,
	}
}

// routeCalculatorToWire renders the DescribeRouteCalculator response.
func routeCalculatorToWire(info *driver.RouteCalculatorInfo) map[string]any {
	out := map[string]any{
		"CalculatorName": info.Name,
		"CalculatorArn":  info.Arn,
		"DataSource":     info.DataSource,
		"CreateTime":     rfc3339(info.CreateTime),
		"UpdateTime":     rfc3339(info.UpdateTime),
	}

	putNonEmpty(out, "Description", info.Description)
	putNonEmpty(out, "PricingPlan", info.PricingPlan)
	putTags(out, info.Tags)

	return out
}

func (h *Handler) createRouteCalculator(w http.ResponseWriter, r *http.Request) {
	var in driver.CreateRouteCalculatorInput
	if !decodeBody(w, r, &in) {
		return
	}

	info, err := h.loc.CreateRouteCalculator(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"CalculatorName": info.Name,
		"CalculatorArn":  info.Arn,
		"CreateTime":     rfc3339(info.CreateTime),
	})
}

func (h *Handler) describeRouteCalculator(w http.ResponseWriter, r *http.Request, name string) {
	info, err := h.loc.DescribeRouteCalculator(r.Context(), name)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, routeCalculatorToWire(info))
}

func (h *Handler) updateRouteCalculator(w http.ResponseWriter, r *http.Request, name string) {
	var in driver.UpdateRouteCalculatorInput
	if !decodeBody(w, r, &in) {
		return
	}

	in.CalculatorName = name

	info, err := h.loc.UpdateRouteCalculator(r.Context(), &in)
	if err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{
		"CalculatorName": info.Name,
		"CalculatorArn":  info.Arn,
		"UpdateTime":     rfc3339(info.UpdateTime),
	})
}

func (h *Handler) deleteRouteCalculator(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.loc.DeleteRouteCalculator(r.Context(), name); err != nil {
		writeErr(w, err)

		return
	}

	writeJSON(w, map[string]any{})
}

func (h *Handler) listRouteCalculators(w http.ResponseWriter, r *http.Request) {
	serveList(w, r, h.loc.ListRouteCalculators, routeCalculatorEntryToWire)
}

// routeCalculatorEntryToWire renders a ListRouteCalculators response entry.
func routeCalculatorEntryToWire(info *driver.RouteCalculatorInfo) map[string]any {
	e := map[string]any{
		"CalculatorName": info.Name,
		"DataSource":     info.DataSource,
		"CreateTime":     rfc3339(info.CreateTime),
		"UpdateTime":     rfc3339(info.UpdateTime),
	}

	putNonEmpty(e, "Description", info.Description)
	putNonEmpty(e, "PricingPlan", info.PricingPlan)

	return e
}
