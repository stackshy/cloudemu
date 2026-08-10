package glue

// registerRegistryRoutes wires the schema registry, security configurations,
// and dev endpoints.
func (h *Handler) registerRegistryRoutes() {
	h.routes["CreateRegistry"] = h.createRegistry
	h.routes["GetRegistry"] = h.getRegistry
	h.routes["UpdateRegistry"] = h.updateRegistry
	h.routes["DeleteRegistry"] = h.deleteRegistry
	h.routes["ListRegistries"] = h.listRegistries

	h.routes["CreateSchema"] = h.createSchema
	h.routes["GetSchema"] = h.getSchema
	h.routes["UpdateSchema"] = h.updateSchema
	h.routes["DeleteSchema"] = h.deleteSchema
	h.routes["ListSchemas"] = h.listSchemas

	h.routes["RegisterSchemaVersion"] = h.registerSchemaVersion
	h.routes["GetSchemaVersion"] = h.getSchemaVersion
	h.routes["GetSchemaByDefinition"] = h.getSchemaByDefinition
	h.routes["ListSchemaVersions"] = h.listSchemaVersions
	h.routes["DeleteSchemaVersions"] = h.deleteSchemaVersions
	h.routes["CheckSchemaVersionValidity"] = h.checkSchemaVersionValidity
	h.routes["GetSchemaVersionsDiff"] = h.getSchemaVersionsDiff

	h.routes["CreateSecurityConfiguration"] = h.createSecurityConfiguration
	h.routes["GetSecurityConfiguration"] = h.getSecurityConfiguration
	h.routes["DeleteSecurityConfiguration"] = h.deleteSecurityConfiguration
	h.routes["GetSecurityConfigurations"] = h.getSecurityConfigurations

	h.routes["CreateDevEndpoint"] = h.createDevEndpoint
	h.routes["GetDevEndpoint"] = h.getDevEndpoint
	h.routes["UpdateDevEndpoint"] = h.updateDevEndpoint
	h.routes["DeleteDevEndpoint"] = h.deleteDevEndpoint
	h.routes["GetDevEndpoints"] = h.getDevEndpoints
	h.routes["ListDevEndpoints"] = h.listDevEndpoints
	h.routes["BatchGetDevEndpoints"] = h.batchGetDevEndpoints
}
