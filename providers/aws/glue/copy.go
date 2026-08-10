package glue

import "github.com/stackshy/cloudemu/v2/services/glue/driver"

// The copy* helpers return deep copies so Get/List/Batch reads never alias the
// stored value's maps or slices. A shallow `out := v; return &out` would leave
// the returned Tags/Columns/Parameters aliasing the store, letting a caller
// mutate committed state and racing concurrent writers under -race.

func copyColumns(in []driver.Column) []driver.Column {
	if in == nil {
		return nil
	}

	out := make([]driver.Column, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Parameters = copyTags(in[i].Parameters)
	}

	return out
}

func copyStorageDescriptor(in *driver.StorageDescriptor) *driver.StorageDescriptor {
	if in == nil {
		return nil
	}

	out := *in
	out.Columns = copyColumns(in.Columns)
	out.Parameters = copyTags(in.Parameters)
	out.BucketColumns = copyStrings(in.BucketColumns)

	if in.SortColumns != nil {
		out.SortColumns = append([]driver.Order(nil), in.SortColumns...)
	}

	if in.SerdeInfo != nil {
		serde := *in.SerdeInfo
		serde.Parameters = copyTags(in.SerdeInfo.Parameters)
		out.SerdeInfo = &serde
	}

	return &out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyDatabase(in driver.Database) driver.Database {
	out := in
	out.Parameters = copyTags(in.Parameters)

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyTable(in driver.Table) driver.Table {
	out := in
	out.Parameters = copyTags(in.Parameters)
	out.PartitionKeys = copyColumns(in.PartitionKeys)
	out.StorageDescriptor = copyStorageDescriptor(in.StorageDescriptor)

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyPartition(in driver.Partition) driver.Partition {
	out := in
	out.Values = copyStrings(in.Values)
	out.Parameters = copyTags(in.Parameters)
	out.StorageDescriptor = copyStorageDescriptor(in.StorageDescriptor)

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyUDF(in driver.UserDefinedFunction) driver.UserDefinedFunction {
	out := in
	if in.ResourceURIs != nil {
		out.ResourceURIs = append([]driver.ResourceURI(nil), in.ResourceURIs...)
	}

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyConnection(in driver.Connection) driver.Connection {
	out := in
	out.MatchCriteria = copyStrings(in.MatchCriteria)
	out.ConnectionProperties = copyTags(in.ConnectionProperties)
	out.PhysicalRequirements = copyAnyMap(in.PhysicalRequirements)

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyCrawler(in driver.Crawler) driver.Crawler {
	out := in
	out.Classifiers = copyStrings(in.Classifiers)
	out.Targets = copyAnyMap(in.Targets)

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyClassifier(in driver.Classifier) driver.Classifier {
	out := in
	out.Definition = copyAnyMap(in.Definition)

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyJob(in driver.Job) driver.Job {
	out := in
	out.Command = copyAnyMap(in.Command)
	out.DefaultArguments = copyTags(in.DefaultArguments)

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyJobRun(in driver.JobRun) driver.JobRun {
	out := in
	out.Arguments = copyTags(in.Arguments)

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyTrigger(in driver.Trigger) driver.Trigger {
	out := in
	out.Predicate = copyAnyMap(in.Predicate)

	if in.Actions != nil {
		out.Actions = make([]map[string]any, len(in.Actions))
		for i := range in.Actions {
			out.Actions[i] = copyAnyMap(in.Actions[i])
		}
	}

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyWorkflow(in driver.Workflow) driver.Workflow {
	out := in
	out.DefaultRunProperties = copyTags(in.DefaultRunProperties)

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyWorkflowRun(in driver.WorkflowRun) driver.WorkflowRun {
	out := in
	out.RunProperties = copyTags(in.RunProperties)

	return out
}

//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func copyDevEndpoint(in driver.DevEndpoint) driver.DevEndpoint {
	out := in
	out.Arguments = copyTags(in.Arguments)

	return out
}

func copySecConfig(in driver.SecurityConfiguration) driver.SecurityConfiguration {
	out := in
	out.EncryptionConfig = copyAnyMap(in.EncryptionConfig)

	return out
}

// copyAnyMap deep-copies one level of a map[string]any; nested maps and slices
// are copied recursively so JSON-decoded request payloads never alias.
func copyAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}

	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = copyAnyValue(v)
	}

	return out
}

func copyAnyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return copyAnyMap(t)
	case []any:
		s := make([]any, len(t))
		for i := range t {
			s[i] = copyAnyValue(t[i])
		}

		return s
	default:
		return v
	}
}
