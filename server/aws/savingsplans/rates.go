package savingsplans

// rateUnitHours is the SavingsPlanRateUnit for a per-hour compute rate.
const rateUnitHours = "Hrs"

// sampleRate is the representative discounted Savings Plan rate the seeded
// catalog quotes for an m5.large-class hour.
const sampleRate = "0.0464"

// offeringRateToWire renders a representative SavingsPlanOfferingRate for an
// offering. The rate quotes the offering's discounted hourly price alongside the
// parent offering's terms.
func offeringRateToWire(o *offering) map[string]any {
	out := map[string]any{
		"operation":   o.operation,
		"productType": firstProduct(o.productTypes),
		"rate":        sampleRate,
		"serviceCode": o.serviceCode,
		"unit":        rateUnitHours,
		"usageType":   o.usageType,
		"savingsPlanOffering": map[string]any{
			"offeringId":      o.id,
			"paymentOption":   o.paymentOption,
			"planType":        o.planType,
			"planDescription": o.description,
			"durationSeconds": o.durationSecs,
			"currency":        currencyUSD,
		},
	}

	// instanceFamily only applies to EC2Instance-type offerings; Compute and
	// SageMaker offerings carry no instance family, so the property is omitted
	// rather than emitted with an empty value.
	if o.ec2Family != "" {
		out["properties"] = []map[string]any{
			{"name": "instanceFamily", "value": o.ec2Family},
		}
	}

	return out
}

// planRatesFor renders a representative SavingsPlanRate set for a purchased plan.
func planRatesFor(p *savingsPlan) []map[string]any {
	return []map[string]any{
		{
			"currency":    p.Currency,
			"operation":   "RunInstances",
			"productType": firstProduct(p.ProductTypes),
			"rate":        sampleRate,
			"serviceCode": "AmazonEC2",
			"unit":        rateUnitHours,
			"usageType":   "BoxUsage:m5.large",
			"properties": []map[string]any{
				{"name": "instanceType", "value": "m5.large"},
				{"name": "region", "value": p.Region},
			},
		},
	}
}

// firstProduct returns the first product type, or "EC2" as a stable default when
// the offering lists none.
func firstProduct(products []string) string {
	if len(products) == 0 {
		return "EC2"
	}

	return products[0]
}
