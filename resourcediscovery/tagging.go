package resourcediscovery

import (
	"context"
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/errors"
)

// AWS service identifiers as they appear in ARNs.
const (
	awsServiceS3       = "s3"
	awsServiceDynamoDB = "dynamodb"
	awsServiceEC2      = "ec2"
)

// DynamoDB resource type within an EC2 ARN.
const awsTypeTable = "table"

// arnParts is the number of colon-separated segments in a fully-qualified
// AWS ARN: arn:partition:service:region:account:resource.
const arnParts = 6

// TagResourceByARN merges tags into the resource identified by arn. The arn
// is parsed to determine the underlying service and resource type, then
// dispatched to the matching driver's tag-mutation method.
//
// Supported in Phase 2:
//   - AWS S3 bucket: arn:aws:s3:::name
//   - AWS DynamoDB table: arn:aws:dynamodb:region:account:table/name
//   - AWS VPC/Subnet/SecurityGroup: arn:aws:ec2:region:account:{vpc,subnet,security-group}/id
//
// Returns InvalidArgument for unsupported services (lambda, ec2 instance, etc.)
// or unparseable ARNs.
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
	service      string // "s3", "dynamodb", "ec2"
	resourceType string // "table", "vpc", "subnet", "security-group", or "" for s3 bucket
	id           string // bucket name, table name, vpc-id, subnet-id, sg-id
}

func parseSupportedARN(arn string) (parsedARN, error) {
	// AWS ARN format: arn:partition:service:region:account:resource
	// resource is either "name" (S3), "type/id", or "type:id".
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
	default:
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument,
			"tagging service %q is not yet supported (Phase 2 covers s3, dynamodb, ec2 networking)", service)
	}
}

func parseS3ARN(arn, resource string) (parsedARN, error) {
	if resource == "" {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "S3 ARN missing bucket name: %q", arn)
	}

	// arn:aws:s3:::bucket-name → resource = "bucket-name" (taggable).
	// arn:aws:s3:::bucket-name/object-key → resource = "bucket-name/object-key".
	// Object-level tagging requires PutObjectTagging, not PutBucketTagging;
	// reject until Phase 2.x adds object support.
	if strings.ContainsRune(resource, '/') {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument,
			"S3 object ARNs are not yet supported (Phase 2 covers buckets only): %q", arn)
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
	case netKindVPC, netKindSubnet, netKindSecurityGroup:
		return parsedARN{service: awsServiceEC2, resourceType: rt, id: id}, nil
	default:
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument,
			"tagging EC2 resource type %q is not yet supported (Phase 2 covers vpc, subnet, security-group)", rt)
	}
}

func splitTypeID(s string, sep rune) (rt, id string, ok bool) {
	for i, r := range s {
		if r == sep {
			return s[:i], s[i+1:], true
		}
	}

	return "", "", false
}

func (e *Engine) dispatchTag(ctx context.Context, res parsedARN, tags map[string]string, set bool, keys []string) error {
	switch {
	case res.service == awsServiceS3:
		return e.tagS3(ctx, res.id, tags, set, keys)
	case res.service == awsServiceDynamoDB && res.resourceType == awsTypeTable:
		return e.tagDynamoDB(ctx, res.id, tags, set, keys)
	case res.service == awsServiceEC2:
		return e.tagEC2Network(ctx, res.resourceType, res.id, tags, set, keys)
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "internal: unrouted parsedARN %+v", res)
	}
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
