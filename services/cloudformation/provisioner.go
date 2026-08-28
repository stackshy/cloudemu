package cloudformation

import "context"

// ResourceRequest is the fully-resolved request to provision one template
// resource. Properties has every intrinsic already evaluated, so a provisioner
// reads plain scalars, maps, and lists.
type ResourceRequest struct {
	LogicalID  string
	Type       string
	Properties map[string]any
	StackName  string
	StackID    string
	Region     string
	AccountID  string
}

// ProvisionedResource is what a provisioner returns after creating a resource:
// the physical id (also the resource's Ref value) and the attributes Fn::GetAtt
// can read (e.g. "Arn").
type ProvisionedResource struct {
	PhysicalID string
	Attributes map[string]string
	// DeleteID is the identifier the same provisioner's Delete needs, when it
	// differs from the CloudFormation physical id. SNS and Secrets Manager, for
	// example, expose an ARN as their physical id / Ref value but their driver
	// deletes by name. Empty means Delete is called with PhysicalID.
	DeleteID string
}

// Provisioner creates and deletes one CloudFormation resource TYPE by calling
// the existing service driver for that type. It owns no state of its own — the
// resource lives in the backing service's store, so it is queryable through that
// service's own SDK surface. Update is modeled as delete+create (replacement) by
// the orchestrator, so a provisioner only implements Create and Delete.
type Provisioner interface {
	Create(ctx context.Context, req ResourceRequest) (*ProvisionedResource, error)
	Delete(ctx context.Context, physicalID string, properties map[string]any) error
}

// Registry maps a CloudFormation resource Type (e.g. "AWS::S3::Bucket") to the
// Provisioner that realizes it. A stack that references a type with no entry
// fails to deploy, matching CloudFormation's "resource type is not supported".
type Registry map[string]Provisioner

// PropString reads a string property, tolerating a value an intrinsic resolved
// to a non-string scalar. Missing keys yield "".
func PropString(props map[string]any, key string) string {
	if props == nil {
		return ""
	}

	if v, ok := props[key]; ok {
		return scalarString(v)
	}

	return ""
}
