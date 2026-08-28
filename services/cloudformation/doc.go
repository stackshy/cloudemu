// Package cloudformation holds the provider-neutral building blocks of a
// CloudFormation-style stack orchestrator: the template model and parser
// (Parameters / Resources / Outputs), the common intrinsic functions (Ref,
// Fn::GetAtt, Fn::Sub, Fn::Join), resource dependency ordering, and the
// Provisioner contract a stack executor drives to create and delete resources.
//
// Like features/topology and services/resourcediscovery, this is a
// cross-service engine: it does NOT own a data store per resource type. A
// Provisioner provisions a resource by calling the existing service driver for
// that type (S3, DynamoDB, SQS, …), so a stack's resources really exist in
// those backends and are queryable through their own SDK surfaces. The AWS
// orchestrator (providers/aws/cloudformation) owns the stack store and wires a
// registry of AWS provisioners built from those driver interfaces.
package cloudformation
