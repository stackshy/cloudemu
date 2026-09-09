package athena

import "github.com/stackshy/cloudemu/v2/services/athena/driver"

// The stored driver values carry pointer and map members, so every store
// read/write deep-copies to keep callers from aliasing internal state.

func copyBoolPtr(b *bool) *bool {
	if b == nil {
		return nil
	}

	v := *b

	return &v
}

func copyInt64Ptr(n *int64) *int64 {
	if n == nil {
		return nil
	}

	v := *n

	return &v
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyEncryptionConfiguration(in *driver.EncryptionConfiguration) *driver.EncryptionConfiguration {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func copyACLConfiguration(in *driver.ACLConfiguration) *driver.ACLConfiguration {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func copyResultConfiguration(in *driver.ResultConfiguration) *driver.ResultConfiguration {
	if in == nil {
		return nil
	}

	return &driver.ResultConfiguration{
		OutputLocation:          in.OutputLocation,
		EncryptionConfiguration: copyEncryptionConfiguration(in.EncryptionConfiguration),
		ExpectedBucketOwner:     in.ExpectedBucketOwner,
		ACLConfiguration:        copyACLConfiguration(in.ACLConfiguration),
	}
}

func copyWorkGroupConfiguration(in driver.WorkGroupConfiguration) driver.WorkGroupConfiguration {
	return driver.WorkGroupConfiguration{
		ResultConfiguration:             copyResultConfiguration(in.ResultConfiguration),
		EnforceWorkGroupConfiguration:   copyBoolPtr(in.EnforceWorkGroupConfiguration),
		PublishCloudWatchMetricsEnabled: copyBoolPtr(in.PublishCloudWatchMetricsEnabled),
		RequesterPaysEnabled:            copyBoolPtr(in.RequesterPaysEnabled),
		BytesScannedCutoffPerQuery:      copyInt64Ptr(in.BytesScannedCutoffPerQuery),
		EngineVersion:                   in.EngineVersion,
	}
}

// copyWorkGroup deep-copies a workgroup. Taken by value so it satisfies the
// func(V) V deep-copy callback signature used across the stores and snapshot.
//
//nolint:gocritic // hugeParam: value signature required by the func(V) V copy callback
func copyWorkGroup(in driver.WorkGroup) driver.WorkGroup {
	out := in
	out.Configuration = copyWorkGroupConfiguration(in.Configuration)

	return out
}

func copyQueryExecutionContext(in *driver.QueryExecutionContext) *driver.QueryExecutionContext {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

// copyQueryExecution deep-copies a query execution. Taken by value so it
// satisfies the func(V) V deep-copy callback signature used across the stores
// and snapshot.
//
//nolint:gocritic // hugeParam: value signature required by the func(V) V copy callback
func copyQueryExecution(in driver.QueryExecution) driver.QueryExecution {
	out := in
	out.ResultConfiguration = copyResultConfiguration(in.ResultConfiguration)
	out.QueryExecutionContext = copyQueryExecutionContext(in.QueryExecutionContext)

	return out
}

func copyDatabase(in driver.Database) driver.Database {
	out := in
	out.Parameters = copyStringMap(in.Parameters)

	return out
}

func copyDataCatalog(in driver.DataCatalog) driver.DataCatalog {
	out := in
	out.Parameters = copyStringMap(in.Parameters)

	return out
}
