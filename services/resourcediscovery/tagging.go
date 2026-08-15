package resourcediscovery

import (
	"context"
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// AWS service identifiers as they appear in ARNs.
const (
	awsServiceS3       = "s3"
	awsServiceDynamoDB = "dynamodb"
	awsServiceEC2      = "ec2"
	awsServiceLambda   = "lambda"
	awsServiceSecrets  = "secretsmanager"
	awsServiceSNS      = "sns"
	awsServiceSQS      = "sqs"
)

// DynamoDB resource type within a DynamoDB ARN.
const awsTypeTable = "table"

// EC2 compute resource types that route to the compute driver's tag store
// (the networking sub-types below route to the networking driver instead).
const (
	ec2TypeInstance = "instance"
	ec2TypeVolume   = "volume"
	ec2TypeSnapshot = "snapshot"
	ec2TypeImage    = "image"
)

// Lambda / Secrets Manager resource-type prefixes within their ARNs.
const (
	lambdaTypeFunction = "function"
	secretsTypeSecret  = "secret"
)

// arnParts is the number of colon-separated segments in a fully-qualified
// AWS ARN: arn:partition:service:region:account:resource.
const arnParts = 6

// TagResourceByARN merges tags into the resource identified by arn. The arn
// is parsed to determine the underlying service and resource type, then
// dispatched to the matching driver's tag-mutation method. Tagging is
// additive: existing tags are preserved and overlapping keys are overwritten.
//
// Supported AWS resources:
//   - S3 bucket:            arn:aws:s3:::name
//   - DynamoDB table:       arn:aws:dynamodb:region:account:table/name
//   - EC2 compute:          arn:aws:ec2:region:account:{instance,volume,snapshot,image}/id
//   - EC2 networking:       arn:aws:ec2:region:account:{vpc,subnet,security-group}/id
//   - Lambda function:      arn:aws:lambda:region:account:function:name
//   - Secrets Manager:      arn:aws:secretsmanager:region:account:secret:name
//   - SNS topic:            arn:aws:sns:region:account:name
//   - SQS queue:            arn:aws:sqs:region:account:name
//
// Returns InvalidArgument for services/resource types without a tag store
// (e.g. CloudWatch alarms, IAM, RDS) or for unparseable ARNs.
func (e *Engine) TagResourceByARN(ctx context.Context, arn string, tags map[string]string) error {
	res, err := parseSupportedARN(arn)
	if err != nil {
		return err
	}

	return e.dispatchTag(ctx, res, tags, true /* set */, nil)
}

// UntagResourceByARN removes the given tag keys from the resource identified
// by arn. ARN parsing rules match TagResourceByARN.
func (e *Engine) UntagResourceByARN(ctx context.Context, arn string, keys []string) error {
	res, err := parseSupportedARN(arn)
	if err != nil {
		return err
	}

	return e.dispatchTag(ctx, res, nil, false /* set */, keys)
}

// parsedARN holds the fragments TagResourceByARN needs to route a call.
type parsedARN struct {
	service      string // "s3", "dynamodb", "ec2", "lambda", "secretsmanager", "sns", "sqs"
	resourceType string // "table"/"instance"/"vpc"/"function"/… or "" for s3/sns/sqs
	id           string // bucket, table, vpc-id, instance-id, function/secret/topic/queue name
}

func parseSupportedARN(arn string) (parsedARN, error) {
	// AWS ARN format: arn:partition:service:region:account:resource
	// resource is either "name" (S3/SNS/SQS), "type/id", or "type:id".
	if !strings.HasPrefix(arn, "arn:aws:") {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "only AWS ARNs are supported, got %q", arn)
	}

	parts := strings.SplitN(arn, ":", arnParts)
	if len(parts) < arnParts {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "malformed ARN: %q", arn)
	}

	service := parts[2]
	resource := parts[5]

	switch service {
	case awsServiceS3:
		return parseS3ARN(arn, resource)
	case awsServiceDynamoDB:
		return parseDynamoDBARN(arn, resource)
	case awsServiceEC2:
		return parseEC2ARN(arn, resource)
	case awsServiceLambda:
		return parseLambdaARN(arn, resource)
	case awsServiceSecrets:
		return parseSecretsARN(arn, resource)
	case awsServiceSNS:
		return parseSNSARN(arn, resource)
	case awsServiceSQS:
		return parseSQSARN(arn, resource)
	default:
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument,
			"tagging service %q is not yet supported", service)
	}
}

func parseS3ARN(arn, resource string) (parsedARN, error) {
	if resource == "" {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "S3 ARN missing bucket name: %q", arn)
	}

	// arn:aws:s3:::bucket-name → resource = "bucket-name" (taggable).
	// arn:aws:s3:::bucket-name/object-key → resource = "bucket-name/object-key".
	// Object-level tagging requires PutObjectTagging, not PutBucketTagging.
	if strings.ContainsRune(resource, '/') {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument,
			"S3 object ARNs are not supported (bucket tagging only): %q", arn)
	}

	return parsedARN{service: awsServiceS3, id: resource}, nil
}

func parseDynamoDBARN(arn, resource string) (parsedARN, error) {
	rt, id, ok := splitTypeID(resource, '/')
	if !ok || rt != awsTypeTable {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "expected DynamoDB table ARN, got %q", arn)
	}

	return parsedARN{service: awsServiceDynamoDB, resourceType: rt, id: id}, nil
}

func parseEC2ARN(arn, resource string) (parsedARN, error) {
	rt, id, ok := splitTypeID(resource, '/')
	if !ok {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "malformed EC2 ARN: %q", arn)
	}

	switch rt {
	case ec2TypeInstance, ec2TypeVolume, ec2TypeSnapshot, ec2TypeImage,
		netKindVPC, netKindSubnet, netKindSecurityGroup:
		return parsedARN{service: awsServiceEC2, resourceType: rt, id: id}, nil
	default:
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument,
			"tagging EC2 resource type %q is not supported", rt)
	}
}

// parseLambdaARN accepts arn:aws:lambda:region:account:function:name — and the
// versioned/aliased form (…:function:name:1) — resolving both to the function
// name, since a Lambda's tags are shared across its versions and aliases.
func parseLambdaARN(arn, resource string) (parsedARN, error) {
	rt, rest, ok := splitTypeID(resource, ':')
	if !ok || rt != lambdaTypeFunction {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "expected Lambda function ARN, got %q", arn)
	}

	name := rest
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		name = rest[:i] // strip a trailing :version or :alias qualifier
	}

	if name == "" {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "Lambda ARN missing function name: %q", arn)
	}

	return parsedARN{service: awsServiceLambda, resourceType: lambdaTypeFunction, id: name}, nil
}

// parseSecretsARN accepts arn:aws:secretsmanager:region:account:secret:name.
func parseSecretsARN(arn, resource string) (parsedARN, error) {
	rt, id, ok := splitTypeID(resource, ':')
	if !ok || rt != secretsTypeSecret || id == "" {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "expected Secrets Manager secret ARN, got %q", arn)
	}

	return parsedARN{service: awsServiceSecrets, resourceType: secretsTypeSecret, id: id}, nil
}

// parseSNSARN accepts arn:aws:sns:region:account:topic-name. Subscription ARNs
// (…:topic:subscription-id, which contain a '/') are rejected — they are not a
// taggable resource.
func parseSNSARN(arn, resource string) (parsedARN, error) {
	if resource == "" || strings.ContainsRune(resource, '/') {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "expected SNS topic ARN, got %q", arn)
	}

	return parsedARN{service: awsServiceSNS, id: resource}, nil
}

// parseSQSARN accepts arn:aws:sqs:region:account:queue-name.
func parseSQSARN(arn, resource string) (parsedARN, error) {
	if resource == "" || strings.ContainsRune(resource, '/') {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "expected SQS queue ARN, got %q", arn)
	}

	return parsedARN{service: awsServiceSQS, id: resource}, nil
}

func splitTypeID(s string, sep rune) (rt, id string, ok bool) {
	for i, r := range s {
		if r == sep {
			return s[:i], s[i+1:], true
		}
	}

	return "", "", false
}

func isEC2ComputeType(rt string) bool {
	switch rt {
	case ec2TypeInstance, ec2TypeVolume, ec2TypeSnapshot, ec2TypeImage:
		return true
	default:
		return false
	}
}

func (e *Engine) dispatchTag(ctx context.Context, res parsedARN, tags map[string]string, set bool, keys []string) error {
	switch res.service {
	case awsServiceS3:
		return e.tagS3(ctx, res.id, tags, set, keys)
	case awsServiceDynamoDB:
		return e.tagDynamoDB(ctx, res.id, tags, set, keys)
	case awsServiceEC2:
		if isEC2ComputeType(res.resourceType) {
			return e.tagEC2Compute(ctx, res.id, tags, set, keys)
		}

		return e.tagEC2Network(ctx, res.resourceType, res.id, tags, set, keys)
	case awsServiceLambda:
		return e.tagLambda(ctx, res.id, tags, set, keys)
	case awsServiceSecrets:
		return e.tagSecret(ctx, res.id, tags, set, keys)
	case awsServiceSNS:
		return e.tagTopic(ctx, res.id, tags, set, keys)
	case awsServiceSQS:
		return e.tagQueue(ctx, res.id, tags, set, keys)
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "internal: unrouted parsedARN %+v", res)
	}
}

// The tag-mutation methods below are provider-specific: only the AWS mocks
// implement them, and the AWS Resource Groups Tagging API is the only surface
// that reaches this dispatcher. Rather than widen every shared services/*/driver
// interface (which would force Azure/GCP to implement AWS-only tag plumbing),
// we type-assert the driver to a narrow capability interface — the same pattern
// resourcediscovery already uses for KubernetesClusters/ScaleSets/RelationalDatabases.

// computeTagger is implemented by the AWS EC2 mock; it tags instances, volumes,
// snapshots, and images by their prefixed id (i-/vol-/snap-/ami-).
type computeTagger interface {
	TagResource(ctx context.Context, id string, tags map[string]string) error
	UntagResource(ctx context.Context, id string, keys []string) error
}

type functionTagger interface {
	TagFunction(ctx context.Context, name string, tags map[string]string) error
	UntagFunction(ctx context.Context, name string, keys []string) error
}

type secretTagger interface {
	TagSecret(ctx context.Context, name string, tags map[string]string) error
	UntagSecret(ctx context.Context, name string, keys []string) error
}

type topicTagger interface {
	TagTopic(ctx context.Context, name string, tags map[string]string) error
	UntagTopic(ctx context.Context, name string, keys []string) error
}

type queueTagger interface {
	TagQueue(ctx context.Context, queueURL string, tags map[string]string) error
	UntagQueue(ctx context.Context, queueURL string, keys []string) error
}

func (e *Engine) tagS3(ctx context.Context, bucket string, tags map[string]string, set bool, keys []string) error {
	if e.drivers.Storage == nil {
		return cerrors.Newf(cerrors.FailedPrecondition, "storage driver not configured on engine")
	}

	if set {
		// S3 PutBucketTagging replaces the entire tag set. To get "merge"
		// semantics we read-modify-write.
		existing, err := e.drivers.Storage.GetBucketTagging(ctx, bucket)
		if err != nil && !cerrors.IsNotFound(err) {
			return fmt.Errorf("tagS3 read %q: %w", bucket, err)
		}

		merged := make(map[string]string, len(existing)+len(tags))
		for k, v := range existing {
			merged[k] = v
		}

		for k, v := range tags {
			merged[k] = v
		}

		return e.drivers.Storage.PutBucketTagging(ctx, bucket, merged)
	}

	existing, err := e.drivers.Storage.GetBucketTagging(ctx, bucket)
	if err != nil {
		return fmt.Errorf("tagS3 read %q: %w", bucket, err)
	}

	for _, k := range keys {
		delete(existing, k)
	}

	return e.drivers.Storage.PutBucketTagging(ctx, bucket, existing)
}

func (e *Engine) tagDynamoDB(ctx context.Context, table string, tags map[string]string, set bool, keys []string) error {
	if e.drivers.Database == nil {
		return cerrors.Newf(cerrors.FailedPrecondition, "database driver not configured on engine")
	}

	if set {
		return e.drivers.Database.TagResource(ctx, table, tags)
	}

	return e.drivers.Database.UntagResource(ctx, table, keys)
}

func (e *Engine) tagEC2Compute(ctx context.Context, id string, tags map[string]string, set bool, keys []string) error {
	ct, err := driverAs[computeTagger](e.drivers.Compute, "compute")
	if err != nil {
		return err
	}

	if set {
		return ct.TagResource(ctx, id, tags)
	}

	return ct.UntagResource(ctx, id, keys)
}

func (e *Engine) tagEC2Network(ctx context.Context, resourceType, id string,
	tags map[string]string, set bool, keys []string,
) error {
	if e.drivers.Networking == nil {
		return cerrors.Newf(cerrors.FailedPrecondition, "networking driver not configured on engine")
	}

	switch resourceType {
	case netKindVPC:
		if set {
			return e.drivers.Networking.UpdateVPCTags(ctx, id, tags)
		}

		return e.drivers.Networking.RemoveVPCTags(ctx, id, keys)
	case netKindSubnet:
		if set {
			return e.drivers.Networking.UpdateSubnetTags(ctx, id, tags)
		}

		return e.drivers.Networking.RemoveSubnetTags(ctx, id, keys)
	case netKindSecurityGroup:
		if set {
			return e.drivers.Networking.UpdateSecurityGroupTags(ctx, id, tags)
		}

		return e.drivers.Networking.RemoveSecurityGroupTags(ctx, id, keys)
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported EC2 resource type %q", resourceType)
	}
}

func (e *Engine) tagLambda(ctx context.Context, name string, tags map[string]string, set bool, keys []string) error {
	ft, err := driverAs[functionTagger](e.drivers.Serverless, "serverless")
	if err != nil {
		return err
	}

	if set {
		return ft.TagFunction(ctx, name, tags)
	}

	return ft.UntagFunction(ctx, name, keys)
}

func (e *Engine) tagSecret(ctx context.Context, name string, tags map[string]string, set bool, keys []string) error {
	st, err := driverAs[secretTagger](e.drivers.Secrets, "secrets")
	if err != nil {
		return err
	}

	if set {
		return st.TagSecret(ctx, name, tags)
	}

	return st.UntagSecret(ctx, name, keys)
}

func (e *Engine) tagTopic(ctx context.Context, name string, tags map[string]string, set bool, keys []string) error {
	tt, err := driverAs[topicTagger](e.drivers.Notification, "notification")
	if err != nil {
		return err
	}

	if set {
		return tt.TagTopic(ctx, name, tags)
	}

	return tt.UntagTopic(ctx, name, keys)
}

// tagQueue resolves the queue name (from the SQS ARN) to the queue URL that the
// SQS tag store is keyed by, then applies the tag mutation.
func (e *Engine) tagQueue(ctx context.Context, name string, tags map[string]string, set bool, keys []string) error {
	qt, err := driverAs[queueTagger](e.drivers.MessageQueue, "message queue")
	if err != nil {
		return err
	}

	url, err := e.resolveQueueURL(ctx, name)
	if err != nil {
		return err
	}

	if set {
		return qt.TagQueue(ctx, url, tags)
	}

	return qt.UntagQueue(ctx, url, keys)
}

func (e *Engine) resolveQueueURL(ctx context.Context, name string) (string, error) {
	queues, err := e.drivers.MessageQueue.ListQueues(ctx, "")
	if err != nil {
		return "", fmt.Errorf("resolveQueueURL: %w", err)
	}

	for i := range queues {
		if queues[i].Name == name {
			return queues[i].URL, nil
		}
	}

	return "", cerrors.Newf(cerrors.NotFound, "queue %q not found", name)
}

// driverAs returns the driver typed as the capability interface T. It returns
// FailedPrecondition when the driver is unconfigured (nil) or does not
// implement the tag capability (e.g. a non-AWS provider), which the RGT handler
// surfaces as an InternalServiceException.
func driverAs[T any](driver any, name string) (T, error) {
	var zero T

	// A nil interface value, or a typed-nil driver, both mean "not configured".
	if driver == nil {
		return zero, cerrors.Newf(cerrors.FailedPrecondition, "%s driver not configured on engine", name)
	}

	tagger, ok := driver.(T)
	if !ok {
		return zero, cerrors.Newf(cerrors.FailedPrecondition, "%s driver does not support tagging", name)
	}

	return tagger, nil
}
