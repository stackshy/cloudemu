package vertexai

import "github.com/stackshy/cloudemu/vertexai/driver"

// copyLabels deep-copies a label map so stored resources never alias a caller's
// map (and vice-versa on return).
func copyLabels(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyStrSlice(in []string) []string {
	if in == nil {
		return nil
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}

// cloneModel returns a deep copy so callers never alias the stored slices/maps.
func cloneModel(in *driver.Model) *driver.Model {
	out := *in
	out.VersionAliases = copyStrSlice(in.VersionAliases)
	out.Labels = copyLabels(in.Labels)

	return &out
}

func cloneDataset(in *driver.Dataset) *driver.Dataset {
	out := *in
	out.Labels = copyLabels(in.Labels)

	return &out
}

func cloneEndpoint(in *driver.Endpoint) *driver.Endpoint {
	out := *in
	out.Labels = copyLabels(in.Labels)

	if in.DeployedModels != nil {
		out.DeployedModels = make([]driver.DeployedModel, len(in.DeployedModels))
		copy(out.DeployedModels, in.DeployedModels)
	}

	if in.TrafficSplit != nil {
		out.TrafficSplit = make(map[string]int, len(in.TrafficSplit))
		for k, v := range in.TrafficSplit {
			out.TrafficSplit[k] = v
		}
	}

	return &out
}

func cloneIndexEndpoint(in *driver.IndexEndpoint) *driver.IndexEndpoint {
	out := *in
	if in.DeployedIndexes != nil {
		out.DeployedIndexes = make([]driver.DeployedIndex, len(in.DeployedIndexes))
		copy(out.DeployedIndexes, in.DeployedIndexes)
	}

	return &out
}
