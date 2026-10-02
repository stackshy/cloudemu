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
// the existing service driver for that type. It owns no state of its own: the
// resource lives in the backing service's store, so it is queryable through that
// service's own SDK surface. A provisioner that also implements Updater can
// change some properties in place. Otherwise every change replaces the resource.
type Provisioner interface {
	Create(ctx context.Context, req ResourceRequest) (*ProvisionedResource, error)
	Delete(ctx context.Context, physicalID string, properties map[string]any) error
}

// ReplacementSchema is implemented by a Provisioner that knows which of its
// properties need a new physical resource when they change, the ones the
// resource reference lists as "Update requires: Replacement". A change to any
// other property is applied in place.
type ReplacementSchema interface {
	RequiresReplacement(property string) bool
}

// NamedResource is implemented by a Provisioner whose resources take a custom
// physical name from one property, such as BucketName or TableName.
type NamedResource interface {
	NameProperty() string
}

// Updater is implemented by a Provisioner that can update its backend in
// place. A ReplacementSchema without an Updater only records the new
// properties on an in-place change, so the backend resource is kept.
type Updater interface {
	ReplacementSchema
	// Update applies req.Properties to the existing resource. previous holds
	// the properties it was last created or updated with.
	Update(ctx context.Context, physicalID string, previous map[string]any, req ResourceRequest) (*ProvisionedResource, error)
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
