package athena

import "github.com/stackshy/cloudemu/v2/services/athena/driver"

// --- wire -> driver ---

func engineVersionFromWire(in *engineVersionJSON) driver.EngineVersion {
	if in == nil {
		return driver.EngineVersion{}
	}

	return driver.EngineVersion{
		SelectedEngineVersion:  in.SelectedEngineVersion,
		EffectiveEngineVersion: in.EffectiveEngineVersion,
	}
}

func encryptionConfigFromWire(in *encryptionConfigJSON) *driver.EncryptionConfiguration {
	if in == nil {
		return nil
	}

	return &driver.EncryptionConfiguration{EncryptionOption: in.EncryptionOption, KmsKey: in.KmsKey}
}

func aclConfigFromWire(in *aclConfigJSON) *driver.ACLConfiguration {
	if in == nil {
		return nil
	}

	return &driver.ACLConfiguration{S3AclOption: in.S3AclOption}
}

func resultConfigFromWire(in *resultConfigJSON) *driver.ResultConfiguration {
	if in == nil {
		return nil
	}

	return &driver.ResultConfiguration{
		OutputLocation:          in.OutputLocation,
		EncryptionConfiguration: encryptionConfigFromWire(in.EncryptionConfiguration),
		ExpectedBucketOwner:     in.ExpectedBucketOwner,
		ACLConfiguration:        aclConfigFromWire(in.ACLConfiguration),
	}
}

func workGroupConfigFromWire(in *workGroupConfigJSON) driver.WorkGroupConfiguration {
	if in == nil {
		return driver.WorkGroupConfiguration{}
	}

	return driver.WorkGroupConfiguration{
		ResultConfiguration:             resultConfigFromWire(in.ResultConfiguration),
		EnforceWorkGroupConfiguration:   in.EnforceWorkGroupConfiguration,
		PublishCloudWatchMetricsEnabled: in.PublishCloudWatchMetricsEnabled,
		RequesterPaysEnabled:            in.RequesterPaysEnabled,
		BytesScannedCutoffPerQuery:      in.BytesScannedCutoffPerQuery,
		EngineVersion:                   engineVersionFromWire(in.EngineVersion),
	}
}

func resultConfigUpdatesFromWire(in *resultConfigUpdatesJSON) *driver.ResultConfigurationUpdates {
	if in == nil {
		return nil
	}

	return &driver.ResultConfigurationUpdates{
		OutputLocation:                in.OutputLocation,
		RemoveOutputLocation:          in.RemoveOutputLocation,
		EncryptionConfiguration:       encryptionConfigFromWire(in.EncryptionConfiguration),
		RemoveEncryptionConfiguration: in.RemoveEncryptionConfiguration,
		ExpectedBucketOwner:           in.ExpectedBucketOwner,
		RemoveExpectedBucketOwner:     in.RemoveExpectedBucketOwner,
		ACLConfiguration:              aclConfigFromWire(in.ACLConfiguration),
		RemoveACLConfiguration:        in.RemoveACLConfiguration,
	}
}

func configUpdatesFromWire(in *workGroupConfigUpdatesJSON) *driver.WorkGroupConfigurationUpdates {
	if in == nil {
		return nil
	}

	out := &driver.WorkGroupConfigurationUpdates{
		EnforceWorkGroupConfiguration:    in.EnforceWorkGroupConfiguration,
		PublishCloudWatchMetricsEnabled:  in.PublishCloudWatchMetricsEnabled,
		RequesterPaysEnabled:             in.RequesterPaysEnabled,
		BytesScannedCutoffPerQuery:       in.BytesScannedCutoffPerQuery,
		RemoveBytesScannedCutoffPerQuery: in.RemoveBytesScannedCutoffPerQuery,
		ResultConfigurationUpdates:       resultConfigUpdatesFromWire(in.ResultConfigurationUpdates),
	}

	if in.EngineVersion != nil {
		ev := engineVersionFromWire(in.EngineVersion)
		out.EngineVersion = &ev
	}

	return out
}

// --- driver -> wire ---

func engineVersionToWire(ev driver.EngineVersion) *engineVersionJSON {
	if ev.SelectedEngineVersion == "" && ev.EffectiveEngineVersion == "" {
		return nil
	}

	return &engineVersionJSON{
		SelectedEngineVersion:  ev.SelectedEngineVersion,
		EffectiveEngineVersion: ev.EffectiveEngineVersion,
	}
}

func encryptionConfigToWire(in *driver.EncryptionConfiguration) *encryptionConfigJSON {
	if in == nil {
		return nil
	}

	return &encryptionConfigJSON{EncryptionOption: in.EncryptionOption, KmsKey: in.KmsKey}
}

func aclConfigToWire(in *driver.ACLConfiguration) *aclConfigJSON {
	if in == nil {
		return nil
	}

	return &aclConfigJSON{S3AclOption: in.S3AclOption}
}

func resultConfigToWire(in *driver.ResultConfiguration) *resultConfigJSON {
	if in == nil {
		return nil
	}

	return &resultConfigJSON{
		OutputLocation:          in.OutputLocation,
		EncryptionConfiguration: encryptionConfigToWire(in.EncryptionConfiguration),
		ExpectedBucketOwner:     in.ExpectedBucketOwner,
		ACLConfiguration:        aclConfigToWire(in.ACLConfiguration),
	}
}

func workGroupConfigToWire(cfg driver.WorkGroupConfiguration) *workGroupConfigJSON {
	return &workGroupConfigJSON{
		ResultConfiguration:             resultConfigToWire(cfg.ResultConfiguration),
		EnforceWorkGroupConfiguration:   cfg.EnforceWorkGroupConfiguration,
		PublishCloudWatchMetricsEnabled: cfg.PublishCloudWatchMetricsEnabled,
		RequesterPaysEnabled:            cfg.RequesterPaysEnabled,
		BytesScannedCutoffPerQuery:      cfg.BytesScannedCutoffPerQuery,
		EngineVersion:                   engineVersionToWire(cfg.EngineVersion),
	}
}

func workGroupToWire(wg *driver.WorkGroup) workGroupJSON {
	return workGroupJSON{
		Name:          wg.Name,
		State:         wg.State,
		Description:   wg.Description,
		Configuration: workGroupConfigToWire(wg.Configuration),
		CreationTime:  epochOrNil(wg.CreationTime),
	}
}

func workGroupSummaryToWire(s driver.WorkGroupSummary) workGroupSummaryJSON { //nolint:gocritic // hugeParam: read-only projection
	return workGroupSummaryJSON{
		Name:          s.Name,
		State:         s.State,
		Description:   s.Description,
		CreationTime:  epochOrNil(s.CreationTime),
		EngineVersion: engineVersionToWire(s.EngineVersion),
	}
}

func namedQueryToWire(nq *driver.NamedQuery) namedQueryJSON {
	return namedQueryJSON{
		Name:         nq.Name,
		Description:  nq.Description,
		Database:     nq.Database,
		QueryString:  nq.QueryString,
		NamedQueryID: nq.NamedQueryID,
		WorkGroup:    nq.WorkGroup,
	}
}

func queryExecutionContextToWire(in *driver.QueryExecutionContext) *queryExecutionContextJSON {
	if in == nil {
		return nil
	}

	return &queryExecutionContextJSON{Database: in.Database, Catalog: in.Catalog}
}

func queryExecutionToWire(qe *driver.QueryExecution) queryExecutionJSON {
	return queryExecutionJSON{
		QueryExecutionID:      qe.QueryExecutionID,
		Query:                 qe.Query,
		StatementType:         qe.StatementType,
		ResultConfiguration:   resultConfigToWire(qe.ResultConfiguration),
		QueryExecutionContext: queryExecutionContextToWire(qe.QueryExecutionContext),
		Status: &queryExecutionStatusJSON{
			State:              qe.Status.State,
			StateChangeReason:  qe.Status.StateChangeReason,
			SubmissionDateTime: epochOrNil(qe.Status.SubmissionDateTime),
			CompletionDateTime: epochOrNil(qe.Status.CompletionDateTime),
		},
		Statistics: &queryExecutionStatisticsJSON{
			EngineExecutionTimeInMillis: qe.Statistics.EngineExecutionTimeInMillis,
			DataScannedInBytes:          qe.Statistics.DataScannedInBytes,
			TotalExecutionTimeInMillis:  qe.Statistics.TotalExecutionTimeInMillis,
		},
		WorkGroup:     qe.WorkGroup,
		EngineVersion: engineVersionToWire(qe.EngineVersion),
	}
}

func databaseToWire(db *driver.Database) databaseJSON {
	return databaseJSON{Name: db.Name, Description: db.Description, Parameters: db.Parameters}
}

func dataCatalogToWire(dc *driver.DataCatalog) dataCatalogJSON {
	return dataCatalogJSON{Name: dc.Name, Description: dc.Description, Type: dc.Type, Parameters: dc.Parameters}
}
