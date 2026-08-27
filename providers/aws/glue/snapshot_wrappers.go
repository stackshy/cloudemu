package glue

import "github.com/stackshy/cloudemu/v2/services/glue/driver"

// getGlueX reads the exported record out of an *xData wrapper under its lock;
// buildGlueX is the inverse, wrapping a restored record in a fresh *xData. They
// are the field maps snapshotWrapped/restoreWrapped drive, kept together so a
// reviewer can confirm every store round-trips without loss. Builders take a
// pointer to avoid copying heavy driver values by value.

func getGlueDB(d *databaseData) driver.Database {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.db
}

func getGluePart(d *partitionData) driver.Partition {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.part
}

func getGlueUDF(d *udfData) driver.UserDefinedFunction {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.fn
}

func getGlueConn(d *connectionData) driver.Connection {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.conn
}

func getGlueCatalog(d *catalogData) driver.Catalog {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.cat
}

func getGlueCrawler(d *crawlerData) driver.Crawler {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.crawler
}

func getGlueClassifier(d *classifierData) driver.Classifier {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.classifier
}

func getGlueJob(d *jobData) driver.Job {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.job
}

func getGlueJobRun(d *jobRunData) driver.JobRun {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.run
}

func getGlueTrigger(d *triggerData) driver.Trigger {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.trigger
}

func getGlueWorkflow(d *workflowData) driver.Workflow {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.workflow
}

func getGlueWorkflowRun(d *workflowRunData) driver.WorkflowRun {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.run
}

func getGlueBlueprint(d *blueprintData) driver.Blueprint {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.blueprint
}

func getGlueBlueprintRun(d *blueprintRunData) driver.BlueprintRun {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.run
}

func getGlueSecConfig(d *secConfigData) driver.SecurityConfiguration {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.sc
}

func getGlueRegistry(d *registryData) driver.Registry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.registry
}

func getGlueDevEndpoint(d *devEndpointData) driver.DevEndpoint {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.endpoint
}

func getGlueTable(d *tableData) tableDataSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return tableDataSnapshot{Table: d.table, Versions: d.versions, NextVer: d.nextVer}
}

func getGlueSchema(d *schemaData) schemaDataSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return schemaDataSnapshot{Schema: d.schema, Versions: d.versions}
}

func buildGlueDB(v *driver.Database) *databaseData { return &databaseData{db: *v} }

func buildGluePart(v *driver.Partition) *partitionData { return &partitionData{part: *v} }

func buildGlueUDF(v *driver.UserDefinedFunction) *udfData { return &udfData{fn: *v} }

func buildGlueConn(v *driver.Connection) *connectionData { return &connectionData{conn: *v} }

func buildGlueCatalog(v *driver.Catalog) *catalogData { return &catalogData{cat: *v} }

func buildGlueCrawler(v *driver.Crawler) *crawlerData { return &crawlerData{crawler: *v} }

func buildGlueClassifier(v *driver.Classifier) *classifierData {
	return &classifierData{classifier: *v}
}

func buildGlueJob(v *driver.Job) *jobData { return &jobData{job: *v} }

func buildGlueJobRun(v *driver.JobRun) *jobRunData { return &jobRunData{run: *v} }

func buildGlueTrigger(v *driver.Trigger) *triggerData { return &triggerData{trigger: *v} }

func buildGlueWorkflow(v *driver.Workflow) *workflowData { return &workflowData{workflow: *v} }

func buildGlueWorkflowRun(v *driver.WorkflowRun) *workflowRunData { return &workflowRunData{run: *v} }

func buildGlueBlueprint(v *driver.Blueprint) *blueprintData { return &blueprintData{blueprint: *v} }

func buildGlueBlueprintRun(v *driver.BlueprintRun) *blueprintRunData {
	return &blueprintRunData{run: *v}
}

func buildGlueSecConfig(v *driver.SecurityConfiguration) *secConfigData {
	return &secConfigData{sc: *v}
}

func buildGlueRegistry(v *driver.Registry) *registryData { return &registryData{registry: *v} }

func buildGlueDevEndpoint(v *driver.DevEndpoint) *devEndpointData {
	return &devEndpointData{endpoint: *v}
}

func buildGlueTable(v *tableDataSnapshot) *tableData {
	return &tableData{table: v.Table, versions: v.Versions, nextVer: v.NextVer}
}

func buildGlueSchema(v *schemaDataSnapshot) *schemaData {
	return &schemaData{schema: v.Schema, versions: v.Versions}
}
