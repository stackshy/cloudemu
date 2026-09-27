package appsync

import (
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

// maxListResults is the maxResults ceiling on every AppSync List operation, per
// the service model. It is also the page size when the caller sends none.
const maxListResults = 25

// Request field names for the per-auth-type config blocks.
const (
	fieldUserPoolConfig   = "userPoolConfig"
	fieldOIDCConfig       = "openIDConnectConfig"
	fieldLambdaAuthConfig = "lambdaAuthorizerConfig"
	fieldAdditionalAuth   = "additionalAuthenticationProviders"
)

// Request field names for the per-type data-source config blocks.
const (
	fieldDynamoDBConfig      = "dynamodbConfig"
	fieldLambdaConfig        = "lambdaConfig"
	fieldElasticsearchConfig = "elasticsearchConfig"
	fieldOpenSearchConfig    = "openSearchServiceConfig"
	fieldHTTPConfig          = "httpConfig"
	fieldRelationalConfig    = "relationalDatabaseConfig"
	fieldEventBridgeConfig   = "eventBridgeConfig"
)

// errEmptyString is the message AppSync returns when a required string field
// (such as a data source's serviceRoleArn) is missing or empty.
const errEmptyString = "The validated string is empty"

// validDefaultActions are the accepted userPoolConfig.defaultAction values.
//
//nolint:gochecknoglobals // immutable validation set, read-only after init.
var validDefaultActions = map[string]bool{"ALLOW": true, "DENY": true}

// authConfigField maps an authentication type to the config block it needs.
// API_KEY and AWS_IAM take none.
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init.
var authConfigField = map[string]string{
	driver.AuthCognito:       fieldUserPoolConfig,
	driver.AuthOpenIDConnect: fieldOIDCConfig,
	driver.AuthLambda:        fieldLambdaAuthConfig,
}

// configBlockName is the name AppSync uses for a config block in its errors.
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init.
var configBlockName = map[string]string{
	fieldUserPoolConfig:      "UserPoolConfig",
	fieldOIDCConfig:          "OpenIDConnectConfig",
	fieldLambdaAuthConfig:    "LambdaAuthorizerConfig",
	fieldDynamoDBConfig:      "DynamodbConfig",
	fieldLambdaConfig:        "LambdaConfig",
	fieldElasticsearchConfig: "ElasticsearchConfig",
	fieldOpenSearchConfig:    "OpenSearchServiceConfig",
	fieldHTTPConfig:          "HttpConfig",
	fieldRelationalConfig:    "RelationalDatabaseConfig",
	fieldEventBridgeConfig:   "EventBridgeConfig",
}

// dataSourceConfigFields lists the data-source config blocks in a fixed order,
// so a request carrying several wrong blocks always names the same one.
//
//nolint:gochecknoglobals // immutable key list, read-only after init.
var dataSourceConfigFields = []string{
	fieldDynamoDBConfig, fieldLambdaConfig, fieldElasticsearchConfig, fieldOpenSearchConfig,
	fieldHTTPConfig, fieldRelationalConfig, fieldEventBridgeConfig,
}

// dataSourceRule is what a data-source type requires: its config block (empty
// for none), the required string fields inside it, and whether the type needs
// a serviceRoleArn.
type dataSourceRule struct {
	config   string
	required []string
	needRole bool
}

//nolint:gochecknoglobals // immutable lookup table, read-only after init.
var dataSourceRules = map[string]dataSourceRule{
	driver.DataSourceDynamoDB:       {config: fieldDynamoDBConfig, required: []string{"tableName", "awsRegion"}, needRole: true},
	driver.DataSourceLambda:         {config: fieldLambdaConfig, required: []string{"lambdaFunctionArn"}, needRole: true},
	driver.DataSourceElasticsearch:  {config: fieldElasticsearchConfig, required: []string{"endpoint", "awsRegion"}, needRole: true},
	driver.DataSourceOpenSearch:     {config: fieldOpenSearchConfig, required: []string{"endpoint", "awsRegion"}, needRole: true},
	driver.DataSourceHTTP:           {config: fieldHTTPConfig, required: []string{"endpoint"}},
	driver.DataSourceRelational:     {config: fieldRelationalConfig},
	driver.DataSourceEventBridge:    {config: fieldEventBridgeConfig, required: []string{"eventBusArn"}, needRole: true},
	driver.DataSourceNone:           {},
	driver.DataSourceBedrockRuntime: {},
}

// validatePage rejects a maxResults outside [0, 25], with the service's
// constraint-violation wording.
func validatePage(page driver.Page) error {
	if page.MaxResults < 0 {
		return badRequest("1 validation error detected: Value '%d' at 'maxResults' failed to satisfy constraint: "+
			"Member must have value greater than or equal to 0", page.MaxResults)
	}

	if page.MaxResults > maxListResults {
		return badRequest("1 validation error detected: Value '%d' at 'maxResults' failed to satisfy constraint: "+
			"Member must have value less than or equal to %d", page.MaxResults, maxListResults)
	}

	return nil
}

// present reports whether a raw field was sent with a non-null value.
func present(extra map[string]json.RawMessage, key string) bool {
	v, ok := extra[key]

	return ok && strings.TrimSpace(string(v)) != "null"
}

// validateAuthConfig checks that the primary authentication type and every
// additional provider carry the config block their type needs.
func validateAuthConfig(authType string, extra map[string]json.RawMessage) error {
	if err := checkAuthBlock(authType, extra); err != nil {
		return err
	}

	if authType == driver.AuthCognito {
		var cfg struct {
			DefaultAction string `json:"defaultAction"`
		}

		if err := json.Unmarshal(extra[fieldUserPoolConfig], &cfg); err != nil || !validDefaultActions[cfg.DefaultAction] {
			return badRequest("Invalid default effect type")
		}
	}

	return validateAdditionalAuth(extra)
}

// validateAdditionalAuth checks each additionalAuthenticationProviders entry
// names a valid type and carries that type's config block.
func validateAdditionalAuth(extra map[string]json.RawMessage) error {
	if !present(extra, fieldAdditionalAuth) {
		return nil
	}

	var providers []map[string]json.RawMessage
	if err := json.Unmarshal(extra[fieldAdditionalAuth], &providers); err != nil {
		return badRequest("additionalAuthenticationProviders must be a list")
	}

	for _, p := range providers {
		var t string
		if present(p, "authenticationType") {
			_ = json.Unmarshal(p["authenticationType"], &t)
		}

		if t == "" {
			return badRequest("AuthenticationType can't be null.")
		}

		if !validAuthTypes[t] {
			return badRequest("authenticationType %q is not valid", t)
		}

		if err := checkAuthBlock(t, p); err != nil {
			return err
		}
	}

	return nil
}

// checkAuthBlock requires the config block an authentication type needs.
func checkAuthBlock(authType string, fields map[string]json.RawMessage) error {
	field, ok := authConfigField[authType]
	if !ok || present(fields, field) {
		return nil
	}

	return badRequest("%s can't be null.", configBlockName[field])
}

// validateDataSourceConfig checks a data source's type against its config
// blocks and serviceRoleArn: the type's own block is required, a block for
// another type is rejected, and required strings inside the block must be set.
func validateDataSourceConfig(dsType, serviceRoleArn string, extra map[string]json.RawMessage) error {
	rule := dataSourceRules[dsType]

	for _, field := range dataSourceConfigFields {
		if field != rule.config && present(extra, field) {
			return badRequest("%s is not supported for data source type %s.", configBlockName[field], dsType)
		}
	}

	if rule.config != "" && !present(extra, rule.config) {
		return badRequest("%s can't be null.", configBlockName[rule.config])
	}

	if err := checkBlockFields(dsType, rule, extra); err != nil {
		return err
	}

	if serviceRoleArn == "" && (rule.needRole || httpNeedsRole(dsType, extra)) {
		return badRequest(errEmptyString)
	}

	return nil
}

// checkBlockFields requires the non-empty string fields inside a config block.
// RELATIONAL_DATABASE nests its required block one level down.
func checkBlockFields(dsType string, rule dataSourceRule, extra map[string]json.RawMessage) error {
	if rule.config == "" {
		return nil
	}

	var block map[string]json.RawMessage
	if err := json.Unmarshal(extra[rule.config], &block); err != nil {
		return badRequest("%s must be an object", configBlockName[rule.config])
	}

	if dsType == driver.DataSourceRelational && !present(block, "rdsHttpEndpointConfig") {
		return badRequest("RdsHttpEndpointConfig can't be null.")
	}

	for _, f := range rule.required {
		var s string
		if present(block, f) {
			_ = json.Unmarshal(block[f], &s)
		}

		if s == "" {
			return badRequest(errEmptyString)
		}
	}

	return nil
}

// httpNeedsRole reports whether an HTTP data source signs its requests with
// IAM. authorizationConfig.authorizationType defaults to AWS_IAM, so any
// authorizationConfig without another type needs a serviceRoleArn.
func httpNeedsRole(dsType string, extra map[string]json.RawMessage) bool {
	if dsType != driver.DataSourceHTTP {
		return false
	}

	var cfg struct {
		AuthorizationConfig *struct {
			AuthorizationType string `json:"authorizationType"`
		} `json:"authorizationConfig"`
	}

	if err := json.Unmarshal(extra[fieldHTTPConfig], &cfg); err != nil || cfg.AuthorizationConfig == nil {
		return false
	}

	t := cfg.AuthorizationConfig.AuthorizationType

	return t == "" || t == driver.AuthAWSIAM
}
