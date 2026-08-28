package resourcediscovery

import (
	"context"
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// AWS service identifiers as they appear in ARNs.
const (
	awsServiceS3          = "s3"
	awsServiceDynamoDB    = "dynamodb"
	awsServiceEC2         = "ec2"
	awsServiceLambda      = "lambda"
	awsServiceSecrets     = "secretsmanager"
	awsServiceSNS         = "sns"
	awsServiceSQS         = "sqs"
	awsServiceElastiCache = "elasticache"
	awsServiceECR         = "ecr"
	awsServiceLogs        = "logs"
	awsServiceRDS         = "rds"
	awsServiceIAM         = "iam"
	awsServiceRoute53     = "route53"
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

// ECR / CloudWatch Logs / IAM resource-type prefixes within their ARNs.
const (
	ecrTypeRepository = "repository"
	logsTypeLogGroup  = "log-group"
	iamTypeRole       = "role"
	iamTypeUser       = "user"
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
//   - ElastiCache:          arn:aws:elasticache:region:account:cluster:name
//   - ECR repository:       arn:aws:ecr:region:account:repository/name
//   - CloudWatch Logs:      arn:aws:logs:region:account:log-group:name
//   - RDS:                  arn:aws:rds:region:account:db:name
//   - IAM role/user:        arn:aws:iam::account:{role,user}/name
//
// Returns InvalidArgument for services/resource types without a tag store
// (e.g. CloudWatch alarms, Route 53 zones, IAM policies/groups) or for
// unparseable ARNs.
func (e *Engine) TagResourceByARN(ctx context.Context, arn string, tags map[string]string) error {
	res, err := parseSupportedARN(arn)
	if err == nil {
		return e.dispatchTag(ctx, res, tags, true /* set */, nil)
	}

	// The ARN is not one of the built-in services above; try a provider-wired
	// generic tagger keyed by the ARN's service token. When none is registered,
	// surface the original "not yet supported" (or malformed-ARN) error.
	if t := e.genericTagger(arn); t != nil {
		return t.TagByARN(ctx, arn, tags)
	}

	return err
}

// UntagResourceByARN removes the given tag keys from the resource identified
// by arn. ARN parsing rules match TagResourceByARN.
func (e *Engine) UntagResourceByARN(ctx context.Context, arn string, keys []string) error {
	res, err := parseSupportedARN(arn)
	if err == nil {
		return e.dispatchTag(ctx, res, nil, false /* set */, keys)
	}

	if t := e.genericTagger(arn); t != nil {
		return t.UntagByARN(ctx, arn, keys)
	}

	return err
}

// genericTagger returns the provider-wired ARNTagger for arn's service token,
// or nil when none is registered. It also recognizes a Route 53 hosted-zone id
// (a bare "Z…" identifier, not an arn:aws string), which the discovery walk
// emits verbatim as a zone's identifier, so tagging a hosted zone round-trips
// through GetResources.
func (e *Engine) genericTagger(arn string) ARNTagger {
	if e.drivers.Taggers == nil {
		return nil
	}

	return e.drivers.Taggers[taggerServiceToken(arn)]
}

// taggerServiceToken returns the AWS service token used to look up a generic
// tagger: segment 3 of an arn:aws:<service>:… ARN, or "route53" for a bare
// hosted-zone id. It returns "" for anything else.
func taggerServiceToken(arn string) string {
	const prefix = "arn:aws:"
	if strings.HasPrefix(arn, prefix) {
		if svc, _, ok := strings.Cut(arn[len(prefix):], ":"); ok {
			return svc
		}

		return ""
	}

	if looksLikeHostedZoneID(arn) {
		return awsServiceRoute53
	}

	return ""
}

// looksLikeHostedZoneID reports whether s is a Route 53 hosted-zone id: a "Z"
// followed by one or more alphanumerics (e.g. "Z1D633PJN98FT9"), the identifier
// the DNS walk surfaces for a zone in place of an ARN.
func looksLikeHostedZoneID(s string) bool {
	if len(s) < 2 || s[0] != 'Z' {
		return false
	}

	for _, r := range s[1:] {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return false
		}
	}

	return true
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

	// Each supported AWS service parses its resource segment into the routing
	// fragments the tag dispatcher needs. A service absent from the table has no
	// tag store wired (or no ARN shape we tag), and is reported as unsupported.
	parsers := map[string]func(arn, resource string) (parsedARN, error){
		awsServiceS3:          parseS3ARN,
		awsServiceDynamoDB:    parseDynamoDBARN,
		awsServiceEC2:         parseEC2ARN,
		awsServiceLambda:      parseLambdaARN,
		awsServiceSecrets:     parseSecretsARN,
		awsServiceSNS:         parseSNSARN,
		awsServiceSQS:         parseSQSARN,
		awsServiceElastiCache: parseElastiCacheARN,
		awsServiceECR:         parseECRARN,
		awsServiceLogs:        parseLogsARN,
		awsServiceRDS:         parseRDSARN,
		awsServiceIAM:         parseIAMARN,
	}

	parse, ok := parsers[service]
	if !ok {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument,
			"tagging service %q is not yet supported", service)
	}

	return parse(arn, resource)
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

// parseSNSARN accepts arn:aws:sns:region:account:topic-name. A topic name is
// alphanumeric plus '-'/'_', so a resource segment carrying a ':' or '/' is a
// subscription (real AWS uses "MyTopic:<uuid>"; the mock uses
// "subscription/<uuid>") — rejected, since a subscription is not taggable.
func parseSNSARN(arn, resource string) (parsedARN, error) {
	if resource == "" || strings.ContainsAny(resource, ":/") {
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

// parseElastiCacheARN accepts arn:aws:elasticache:region:account:{cluster|
// replicationgroup|snapshot|parametergroup|subnetgroup}:name. ElastiCache tags
// are addressed by the full ARN, so the whole ARN is carried as the id.
func parseElastiCacheARN(arn, resource string) (parsedARN, error) {
	rt, id, ok := splitTypeID(resource, ':')
	if !ok || id == "" {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "expected ElastiCache ARN, got %q", arn)
	}

	return parsedARN{service: awsServiceElastiCache, resourceType: rt, id: arn}, nil
}

// parseECRARN accepts arn:aws:ecr:region:account:repository/name.
func parseECRARN(arn, resource string) (parsedARN, error) {
	rt, id, ok := splitTypeID(resource, '/')
	if !ok || rt != ecrTypeRepository || id == "" {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "expected ECR repository ARN, got %q", arn)
	}

	return parsedARN{service: awsServiceECR, resourceType: rt, id: id}, nil
}

// parseLogsARN accepts arn:aws:logs:region:account:log-group:name — and the
// stream-qualified form (…:log-group:name:*) — resolving both to the group
// name, since a log group's tags are the tag-operation target.
func parseLogsARN(arn, resource string) (parsedARN, error) {
	rt, rest, ok := splitTypeID(resource, ':')
	if !ok || rt != logsTypeLogGroup || rest == "" {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "expected CloudWatch Logs log-group ARN, got %q", arn)
	}

	name := rest
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		name = rest[:i] // strip a trailing :* stream qualifier
	}

	if name == "" {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "CloudWatch Logs ARN missing group name: %q", arn)
	}

	return parsedARN{service: awsServiceLogs, resourceType: rt, id: name}, nil
}

// parseRDSARN accepts arn:aws:rds:region:account:{db|cluster|snapshot|...}:name.
// RDS tags are addressed by the full ARN, so the whole ARN is carried as the id.
func parseRDSARN(arn, resource string) (parsedARN, error) {
	rt, id, ok := splitTypeID(resource, ':')
	if !ok || id == "" {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "expected RDS ARN, got %q", arn)
	}

	return parsedARN{service: awsServiceRDS, resourceType: rt, id: arn}, nil
}

// parseIAMARN accepts arn:aws:iam::account:{role|user}/name. Only roles and
// users carry a tag store; other IAM resource types are rejected as untaggable.
func parseIAMARN(arn, resource string) (parsedARN, error) {
	rt, id, ok := splitTypeID(resource, '/')
	if !ok || id == "" {
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument, "malformed IAM ARN: %q", arn)
	}

	switch rt {
	case iamTypeRole, iamTypeUser:
		return parsedARN{service: awsServiceIAM, resourceType: rt, id: id}, nil
	default:
		return parsedARN{}, cerrors.Newf(cerrors.InvalidArgument,
			"tagging IAM resource type %q is not supported", rt)
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

func isEC2ComputeType(rt string) bool {
	switch rt {
	case ec2TypeInstance, ec2TypeVolume, ec2TypeSnapshot, ec2TypeImage:
		return true
	default:
		return false
	}
}

func (e *Engine) dispatchTag(ctx context.Context, res parsedARN, tags map[string]string, set bool, keys []string) error {
	routes := map[string]func() error{
		awsServiceS3:          func() error { return e.tagS3(ctx, res.id, tags, set, keys) },
		awsServiceDynamoDB:    func() error { return e.tagDynamoDB(ctx, res.id, tags, set, keys) },
		awsServiceEC2:         func() error { return e.tagEC2(ctx, res, tags, set, keys) },
		awsServiceLambda:      func() error { return e.tagLambda(ctx, res.id, tags, set, keys) },
		awsServiceSecrets:     func() error { return e.tagSecret(ctx, res.id, tags, set, keys) },
		awsServiceSNS:         func() error { return e.tagTopic(ctx, res.id, tags, set, keys) },
		awsServiceSQS:         func() error { return e.tagQueue(ctx, res.id, tags, set, keys) },
		awsServiceElastiCache: func() error { return e.tagCache(ctx, res.id, tags, set, keys) },
		awsServiceECR:         func() error { return e.tagRepository(ctx, res.id, tags, set, keys) },
		awsServiceLogs:        func() error { return e.tagLogGroup(ctx, res.id, tags, set, keys) },
		awsServiceRDS:         func() error { return e.tagRDS(ctx, res.id, tags, set, keys) },
		awsServiceIAM:         func() error { return e.tagIAM(ctx, res.resourceType, res.id, tags, set, keys) },
	}

	route, ok := routes[res.service]
	if !ok {
		return cerrors.Newf(cerrors.InvalidArgument, "internal: unrouted parsedARN %+v", res)
	}

	return route()
}

// tagEC2 routes an EC2 ARN to the compute or networking tag store by its
// resource type (instance/volume/snapshot/image → compute; vpc/subnet/
// security-group → networking).
func (e *Engine) tagEC2(ctx context.Context, res parsedARN, tags map[string]string, set bool, keys []string) error {
	if isEC2ComputeType(res.resourceType) {
		return e.tagEC2Compute(ctx, res.id, tags, set, keys)
	}

	return e.tagEC2Network(ctx, res.resourceType, res.id, tags, set, keys)
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

// cacheTagger is implemented by the AWS ElastiCache mock; it tags a cache
// cluster / replication group by its full ARN.
type cacheTagger interface {
	AddTags(ctx context.Context, arn string, tags map[string]string) error
	RemoveTags(ctx context.Context, arn string, keys []string) error
}

// repositoryTagger is implemented by the AWS ECR mock; it tags a repository by
// its short name.
type repositoryTagger interface {
	TagRepository(ctx context.Context, name string, tags map[string]string) error
	UntagRepository(ctx context.Context, name string, keys []string) error
}

// logGroupTagger is implemented by the AWS CloudWatch Logs mock; it tags a log
// group by its name.
type logGroupTagger interface {
	TagLogGroup(ctx context.Context, name string, tags map[string]string) error
	UntagLogGroup(ctx context.Context, name string, keys []string) error
}

// relationalDBTagger is implemented by the AWS RDS discovery adapter (delegating
// to the RDS mock); it tags an RDS instance/cluster/snapshot by its full ARN.
type relationalDBTagger interface {
	AddTagsToResource(ctx context.Context, arn string, tags map[string]string) error
	RemoveTagsFromResource(ctx context.Context, arn string, keys []string) error
}

// iamTagger is implemented by the AWS IAM mock; it tags roles and users by name.
type iamTagger interface {
	TagRole(ctx context.Context, name string, tags map[string]string) error
	UntagRole(ctx context.Context, name string, keys []string) error
	TagUser(ctx context.Context, name string, tags map[string]string) error
	UntagUser(ctx context.Context, name string, keys []string) error
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

func (e *Engine) tagCache(ctx context.Context, arn string, tags map[string]string, set bool, keys []string) error {
	ct, err := driverAs[cacheTagger](e.drivers.Cache, "cache")
	if err != nil {
		return err
	}

	if set {
		return ct.AddTags(ctx, arn, tags)
	}

	return ct.RemoveTags(ctx, arn, keys)
}

func (e *Engine) tagRepository(ctx context.Context, name string, tags map[string]string, set bool, keys []string) error {
	rt, err := driverAs[repositoryTagger](e.drivers.ContainerReg, "container registry")
	if err != nil {
		return err
	}

	if set {
		return rt.TagRepository(ctx, name, tags)
	}

	return rt.UntagRepository(ctx, name, keys)
}

func (e *Engine) tagLogGroup(ctx context.Context, name string, tags map[string]string, set bool, keys []string) error {
	lt, err := driverAs[logGroupTagger](e.drivers.Logging, "logging")
	if err != nil {
		return err
	}

	if set {
		return lt.TagLogGroup(ctx, name, tags)
	}

	return lt.UntagLogGroup(ctx, name, keys)
}

func (e *Engine) tagRDS(ctx context.Context, arn string, tags map[string]string, set bool, keys []string) error {
	rt, err := driverAs[relationalDBTagger](e.drivers.RelationalDB, "relational database")
	if err != nil {
		return err
	}

	if set {
		return rt.AddTagsToResource(ctx, arn, tags)
	}

	return rt.RemoveTagsFromResource(ctx, arn, keys)
}

func (e *Engine) tagIAM(ctx context.Context, resourceType, id string,
	tags map[string]string, set bool, keys []string,
) error {
	it, err := driverAs[iamTagger](e.drivers.IAM, "iam")
	if err != nil {
		return err
	}

	switch resourceType {
	case iamTypeRole:
		if set {
			return it.TagRole(ctx, id, tags)
		}

		return it.UntagRole(ctx, id, keys)
	case iamTypeUser:
		if set {
			return it.TagUser(ctx, id, tags)
		}

		return it.UntagUser(ctx, id, keys)
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "tagging IAM resource type %q is not supported", resourceType)
	}
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

	// An unconfigured driver is stored as a nil interface field (the engine
	// never wraps a typed-nil pointer), so this catches the "not wired" case.
	if driver == nil {
		return zero, cerrors.Newf(cerrors.FailedPrecondition, "%s driver not configured on engine", name)
	}

	tagger, ok := driver.(T)
	if !ok {
		return zero, cerrors.Newf(cerrors.FailedPrecondition, "%s driver does not support tagging", name)
	}

	return tagger, nil
}
