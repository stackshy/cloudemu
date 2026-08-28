package aws

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	psdriver "github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	serverlessdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// cloudformationRegistry maps each supported CloudFormation resource type to a
// provisioner that realizes it by calling the matching AWS service driver. Like
// the resourcediscovery adapters, these live in the provider package so the
// orchestrator (services/cloudformation) stays free of concrete mock imports and
// consumes only the shared driver interfaces.
func cloudformationRegistry(p *Provider) cfn.Registry {
	return cfn.Registry{
		"AWS::S3::Bucket":             s3BucketProvisioner{p.S3},
		"AWS::DynamoDB::Table":        dynamoTableProvisioner{p.DynamoDB},
		"AWS::SQS::Queue":             sqsQueueProvisioner{p.SQS},
		"AWS::SNS::Topic":             snsTopicProvisioner{p.SNS},
		"AWS::Lambda::Function":       lambdaFunctionProvisioner{p.Lambda},
		"AWS::IAM::Role":              iamRoleProvisioner{p.IAM},
		"AWS::SecretsManager::Secret": secretProvisioner{p.SecretsManager},
		"AWS::SSM::Parameter":         ssmParameterProvisioner{p.SSM},
	}
}

// physicalName returns an explicit name property when set, otherwise a
// CloudFormation-style generated name (StackName-LogicalId-<random>).
func physicalName(req *cfn.ResourceRequest, key string, lower bool) string {
	if v := cfn.PropString(req.Properties, key); v != "" {
		return v
	}

	name := req.StackName + "-" + req.LogicalID + "-" + strings.ReplaceAll(idgen.UUID(), "-", "")[:12]
	if lower {
		return strings.ToLower(name)
	}

	return name
}

// --- AWS::S3::Bucket ---

type s3BucketProvisioner struct{ s3 storagedriver.Bucket }

//nolint:gocritic // hugeParam: interface method signature is fixed.
func (p s3BucketProvisioner) Create(ctx context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := physicalName(&req, "BucketName", true)
	if err := p.s3.CreateBucket(ctx, name); err != nil {
		return nil, err
	}

	return &cfn.ProvisionedResource{
		PhysicalID: name,
		Attributes: map[string]string{
			"Arn":                "arn:aws:s3:::" + name,
			"DomainName":         name + ".s3.amazonaws.com",
			"RegionalDomainName": name + ".s3." + req.Region + ".amazonaws.com",
			"WebsiteURL":         "http://" + name + ".s3-website-" + req.Region + ".amazonaws.com",
		},
	}, nil
}

func (p s3BucketProvisioner) Delete(ctx context.Context, physicalID string, _ map[string]any) error {
	return p.s3.DeleteBucket(ctx, physicalID)
}

// --- AWS::DynamoDB::Table ---

type dynamoTableProvisioner struct{ db dbdriver.Database }

//nolint:gocritic // hugeParam: interface method signature is fixed.
func (p dynamoTableProvisioner) Create(ctx context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := physicalName(&req, "TableName", false)

	cfg := dynamoTableConfig(name, req.Properties)
	if err := p.db.CreateTable(ctx, cfg); err != nil {
		return nil, err
	}

	attrs := map[string]string{"Arn": ""}

	if desc, err := p.db.DescribeTable(ctx, name); err == nil && desc != nil {
		attrs["Arn"] = desc.TableArn
		if desc.StreamArn != "" {
			attrs["StreamArn"] = desc.StreamArn
		}
	}

	return &cfn.ProvisionedResource{PhysicalID: name, Attributes: attrs}, nil
}

func (p dynamoTableProvisioner) Delete(ctx context.Context, physicalID string, _ map[string]any) error {
	return p.db.DeleteTable(ctx, physicalID)
}

func dynamoTableConfig(name string, props map[string]any) dbdriver.TableConfig {
	cfg := dbdriver.TableConfig{
		Name:        name,
		BillingMode: cfn.PropString(props, "BillingMode"),
	}

	for _, a := range propList(props, "AttributeDefinitions") {
		m := asMap(a)
		cfg.Attributes = append(cfg.Attributes, dbdriver.AttributeDef{
			Name: cfn.PropString(m, "AttributeName"),
			Type: cfn.PropString(m, "AttributeType"),
		})
	}

	for _, k := range propList(props, "KeySchema") {
		m := asMap(k)
		switch cfn.PropString(m, "KeyType") {
		case "HASH":
			cfg.PartitionKey = cfn.PropString(m, "AttributeName")
		case "RANGE":
			cfg.SortKey = cfn.PropString(m, "AttributeName")
		}
	}

	if pt := asMap(props["ProvisionedThroughput"]); pt != nil {
		cfg.ReadCapacityUnits = int64(propInt(pt, "ReadCapacityUnits"))
		cfg.WriteCapacityUnits = int64(propInt(pt, "WriteCapacityUnits"))
	}

	return cfg
}

// --- AWS::SQS::Queue ---

type sqsQueueProvisioner struct{ sqs mqdriver.MessageQueue }

//nolint:gocritic // hugeParam: interface method signature is fixed.
func (p sqsQueueProvisioner) Create(ctx context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := physicalName(&req, "QueueName", false)

	cfg := mqdriver.QueueConfig{
		Name:              name,
		FIFO:              propBool(req.Properties, "FifoQueue") || strings.HasSuffix(name, ".fifo"),
		DelaySeconds:      propInt(req.Properties, "DelaySeconds"),
		VisibilityTimeout: propInt(req.Properties, "VisibilityTimeout"),
		MaxMessageSize:    propInt(req.Properties, "MaximumMessageSize"),
		MessageRetention:  propInt(req.Properties, "MessageRetentionPeriod"),
	}

	info, err := p.sqs.CreateQueue(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return &cfn.ProvisionedResource{
		PhysicalID: info.URL,
		Attributes: map[string]string{"Arn": info.ARN, "QueueName": info.Name, "QueueUrl": info.URL},
	}, nil
}

func (p sqsQueueProvisioner) Delete(ctx context.Context, physicalID string, _ map[string]any) error {
	return p.sqs.DeleteQueue(ctx, physicalID)
}

// --- AWS::SNS::Topic ---

type snsTopicProvisioner struct{ sns notifdriver.Notification }

//nolint:gocritic // hugeParam: interface method signature is fixed.
func (p snsTopicProvisioner) Create(ctx context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := physicalName(&req, "TopicName", false)

	info, err := p.sns.CreateTopic(ctx, notifdriver.TopicConfig{
		Name:        name,
		DisplayName: cfn.PropString(req.Properties, "DisplayName"),
		FifoTopic:   propBool(req.Properties, "FifoTopic") || strings.HasSuffix(name, ".fifo"),
	})
	if err != nil {
		return nil, err
	}

	// SNS Ref returns the topic ARN (ResourceID); the driver deletes by name.
	return &cfn.ProvisionedResource{
		PhysicalID: info.ResourceID,
		DeleteID:   info.Name,
		Attributes: map[string]string{"TopicArn": info.ResourceID, "TopicName": info.Name},
	}, nil
}

func (p snsTopicProvisioner) Delete(ctx context.Context, deleteID string, _ map[string]any) error {
	return p.sns.DeleteTopic(ctx, deleteID)
}

// --- AWS::Lambda::Function ---

type lambdaFunctionProvisioner struct{ lambda serverlessdriver.Serverless }

//nolint:gocritic // hugeParam: interface method signature is fixed.
func (p lambdaFunctionProvisioner) Create(ctx context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := physicalName(&req, "FunctionName", false)

	cfg := serverlessdriver.FunctionConfig{
		Name:        name,
		Runtime:     cfn.PropString(req.Properties, "Runtime"),
		Handler:     cfn.PropString(req.Properties, "Handler"),
		Role:        cfn.PropString(req.Properties, "Role"),
		Description: cfn.PropString(req.Properties, "Description"),
		Memory:      propInt(req.Properties, "MemorySize"),
		Timeout:     propInt(req.Properties, "Timeout"),
		Environment: lambdaEnvironment(req.Properties),
		Code:        lambdaCode(req.Properties),
	}

	info, err := p.lambda.CreateFunction(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return &cfn.ProvisionedResource{
		PhysicalID: info.Name,
		Attributes: map[string]string{"Arn": info.ARN},
	}, nil
}

func (p lambdaFunctionProvisioner) Delete(ctx context.Context, physicalID string, _ map[string]any) error {
	return p.lambda.DeleteFunction(ctx, physicalID)
}

func lambdaEnvironment(props map[string]any) map[string]string {
	vars := asMap(asMap(props["Environment"])["Variables"])
	if len(vars) == 0 {
		return nil
	}

	out := make(map[string]string, len(vars))
	for k, v := range vars {
		out[k] = cfn.Stringify(v)
	}

	return out
}

func lambdaCode(props map[string]any) []byte {
	code := asMap(props["Code"])
	if zip := cfn.PropString(code, "ZipFile"); zip != "" {
		return []byte(zip)
	}

	return nil
}

// --- AWS::IAM::Role ---

type iamRoleProvisioner struct{ iam iamdriver.IAM }

//nolint:gocritic // hugeParam: interface method signature is fixed.
func (p iamRoleProvisioner) Create(ctx context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := physicalName(&req, "RoleName", false)

	info, err := p.iam.CreateRole(ctx, iamdriver.RoleConfig{
		Name:                name,
		Path:                cfn.PropString(req.Properties, "Path"),
		Description:         cfn.PropString(req.Properties, "Description"),
		AssumeRolePolicyDoc: jsonProp(req.Properties, "AssumeRolePolicyDocument"),
		MaxSessionDuration:  propInt(req.Properties, "MaxSessionDuration"),
	})
	if err != nil {
		return nil, err
	}

	return &cfn.ProvisionedResource{
		PhysicalID: info.Name,
		Attributes: map[string]string{"Arn": info.ARN, "RoleId": info.ID},
	}, nil
}

func (p iamRoleProvisioner) Delete(ctx context.Context, physicalID string, _ map[string]any) error {
	return p.iam.DeleteRole(ctx, physicalID)
}

// --- AWS::SecretsManager::Secret ---

type secretProvisioner struct{ secrets secretsdriver.Secrets }

//nolint:gocritic // hugeParam: interface method signature is fixed.
func (p secretProvisioner) Create(ctx context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := physicalName(&req, "Name", false)

	info, err := p.secrets.CreateSecret(ctx, secretsdriver.SecretConfig{
		Name:        name,
		Description: cfn.PropString(req.Properties, "Description"),
		KMSKeyID:    cfn.PropString(req.Properties, "KmsKeyId"),
	}, []byte(cfn.PropString(req.Properties, "SecretString")))
	if err != nil {
		return nil, err
	}

	// Secrets Manager Ref returns the secret ARN; the driver deletes by name.
	return &cfn.ProvisionedResource{
		PhysicalID: info.ResourceID,
		DeleteID:   info.Name,
		Attributes: map[string]string{"Arn": info.ResourceID, "Id": info.ResourceID},
	}, nil
}

func (p secretProvisioner) Delete(ctx context.Context, deleteID string, _ map[string]any) error {
	return p.secrets.DeleteSecret(ctx, deleteID)
}

// --- AWS::SSM::Parameter ---

type ssmParameterProvisioner struct{ ssm psdriver.ParameterStore }

//nolint:gocritic // hugeParam: interface method signature is fixed.
func (p ssmParameterProvisioner) Create(ctx context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := physicalName(&req, "Name", false)

	ptype := cfn.PropString(req.Properties, "Type")
	if ptype == "" {
		ptype = "String"
	}

	value := cfn.PropString(req.Properties, "Value")

	if _, _, err := p.ssm.PutParameter(ctx, psdriver.PutConfig{
		Name:        name,
		Value:       value,
		Type:        ptype,
		Description: cfn.PropString(req.Properties, "Description"),
		Tier:        cfn.PropString(req.Properties, "Tier"),
	}); err != nil {
		return nil, err
	}

	return &cfn.ProvisionedResource{
		PhysicalID: name,
		Attributes: map[string]string{"Type": ptype, "Value": value, "Arn": ssmParameterARN(&req, name)},
	}, nil
}

func (p ssmParameterProvisioner) Delete(ctx context.Context, physicalID string, _ map[string]any) error {
	return p.ssm.DeleteParameter(ctx, physicalID)
}

func ssmParameterARN(req *cfn.ResourceRequest, name string) string {
	resource := "parameter/" + name
	if strings.HasPrefix(name, "/") {
		resource = "parameter" + name
	}

	return idgen.AWSARN("ssm", req.Region, req.AccountID, resource)
}

// --- property helpers ---

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func propList(props map[string]any, key string) []any {
	l, _ := props[key].([]any)
	return l
}

func propInt(props map[string]any, key string) int {
	switch v := props[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		return 0
	default:
		return 0
	}
}

func propBool(props map[string]any, key string) bool {
	switch v := props[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	default:
		return false
	}
}

// jsonProp marshals a property value (typically a policy document object) back
// to a JSON string, the form the driver stores.
func jsonProp(props map[string]any, key string) string {
	v, ok := props[key]
	if !ok {
		return ""
	}

	if s, ok := v.(string); ok {
		return s
	}

	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}

	return string(b)
}
