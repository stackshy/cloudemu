package glue

// registerComputeRoutes wires crawlers, classifiers, jobs and runs, triggers,
// workflows and runs, and blueprints and runs.
//
//nolint:funlen // a flat route table; one line per operation is the clearest form
func (h *Handler) registerComputeRoutes() {
	h.routes["CreateCrawler"] = h.createCrawler
	h.routes["GetCrawler"] = h.getCrawler
	h.routes["UpdateCrawler"] = h.updateCrawler
	h.routes["DeleteCrawler"] = h.deleteCrawler
	h.routes["GetCrawlers"] = h.getCrawlers
	h.routes["ListCrawlers"] = h.listCrawlers
	h.routes["StartCrawler"] = h.startCrawler
	h.routes["StopCrawler"] = h.stopCrawler
	h.routes["BatchGetCrawlers"] = h.batchGetCrawlers

	h.routes["CreateClassifier"] = h.createClassifier
	h.routes["GetClassifier"] = h.getClassifier
	h.routes["UpdateClassifier"] = h.updateClassifier
	h.routes["DeleteClassifier"] = h.deleteClassifier
	h.routes["GetClassifiers"] = h.getClassifiers

	h.routes["CreateJob"] = h.createJob
	h.routes["GetJob"] = h.getJob
	h.routes["UpdateJob"] = h.updateJob
	h.routes["DeleteJob"] = h.deleteJob
	h.routes["GetJobs"] = h.getJobs
	h.routes["ListJobs"] = h.listJobs
	h.routes["BatchGetJobs"] = h.batchGetJobs
	h.routes["StartJobRun"] = h.startJobRun
	h.routes["GetJobRun"] = h.getJobRun
	h.routes["GetJobRuns"] = h.getJobRuns
	h.routes["BatchStopJobRun"] = h.batchStopJobRun

	h.routes["CreateTrigger"] = h.createTrigger
	h.routes["GetTrigger"] = h.getTrigger
	h.routes["UpdateTrigger"] = h.updateTrigger
	h.routes["DeleteTrigger"] = h.deleteTrigger
	h.routes["GetTriggers"] = h.getTriggers
	h.routes["ListTriggers"] = h.listTriggers
	h.routes["StartTrigger"] = h.startTrigger
	h.routes["StopTrigger"] = h.stopTrigger
	h.routes["BatchGetTriggers"] = h.batchGetTriggers

	h.routes["CreateWorkflow"] = h.createWorkflow
	h.routes["GetWorkflow"] = h.getWorkflow
	h.routes["UpdateWorkflow"] = h.updateWorkflow
	h.routes["DeleteWorkflow"] = h.deleteWorkflow
	h.routes["ListWorkflows"] = h.listWorkflows
	h.routes["BatchGetWorkflows"] = h.batchGetWorkflows
	h.routes["StartWorkflowRun"] = h.startWorkflowRun
	h.routes["GetWorkflowRun"] = h.getWorkflowRun
	h.routes["GetWorkflowRuns"] = h.getWorkflowRuns
	h.routes["StopWorkflowRun"] = h.stopWorkflowRun
	h.routes["ResumeWorkflowRun"] = h.resumeWorkflowRun
	h.routes["GetWorkflowRunProperties"] = h.getWorkflowRunProperties
	h.routes["PutWorkflowRunProperties"] = h.putWorkflowRunProperties

	h.routes["CreateBlueprint"] = h.createBlueprint
	h.routes["GetBlueprint"] = h.getBlueprint
	h.routes["UpdateBlueprint"] = h.updateBlueprint
	h.routes["DeleteBlueprint"] = h.deleteBlueprint
	h.routes["ListBlueprints"] = h.listBlueprints
	h.routes["BatchGetBlueprints"] = h.batchGetBlueprints
	h.routes["StartBlueprintRun"] = h.startBlueprintRun
	h.routes["GetBlueprintRun"] = h.getBlueprintRun
	h.routes["GetBlueprintRuns"] = h.getBlueprintRuns
}
