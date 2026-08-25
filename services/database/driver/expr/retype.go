package expr

// RetypeItem walks a decoded item and reconstructs every DynamoDB Number (N)
// attribute that a generic map[string]any JSON round-trip left as the
// self-describing {"$ddbN":"25"} object back into a Number, recursing through
// nested maps and lists. A bare JSON number decodes into any as float64, which
// would silently drop exact-decimal precision; Number's marshaled tag form
// survives the round-trip and this rebuilds it. Both the persist snapshot path
// and per-driver snapshots use it so item numbers restore losslessly.
func RetypeItem(item map[string]any) map[string]any {
	out := make(map[string]any, len(item))
	for k, v := range item {
		out[k] = RetypeValue(v)
	}

	return out
}

// RetypeValue reconstructs a single decoded value, recursing through maps and
// lists. A one-key map carrying NumberJSONTag becomes a Number; everything else
// is returned structurally unchanged.
func RetypeValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 1 {
			if n, ok := t[NumberJSONTag]; ok {
				if s, isStr := n.(string); isStr {
					return Number(s)
				}
			}
		}

		return RetypeItem(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = RetypeValue(t[i])
		}

		return out
	default:
		return v
	}
}
