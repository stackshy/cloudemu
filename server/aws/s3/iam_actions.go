package s3

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsauthz"
)

// IAMChecks names the IAM actions and resource ARNs a request needs. It
// takes the operation from classify, the same function ServeHTTP dispatches
// on, so the authorized operation is always the one that runs. ok=false is
// returned only for requests the handler answers with an error and no side
// effect (an unsupported method, or a DeleteObjects or copy request it
// cannot parse).
//
// The actions follow the AWS Service Authorization Reference for Amazon S3:
// for example DeleteBucketTagging needs s3:PutBucketTagging, HeadBucket
// needs s3:ListBucket, and an operation on a specific version needs the
// *Version action.
func (h *Handler) IAMChecks(r *http.Request, s awsauthz.Scope) ([]awsauthz.Check, bool) {
	op, a := classify(r)

	rule, ok := iamRules[op]
	if !ok {
		return nil, false
	}

	partition := s.Partition
	if partition == "" {
		partition = "aws"
	}

	c := &checkSet{arnPrefix: "arn:" + partition + ":s3:::", sourceTagged: h.objectHasTags}
	c.bucketARN = c.arnPrefix + a.bucket
	c.objectARN = c.bucketARN + "/" + a.key

	if !rule(r, &a, c) {
		return nil, false
	}

	return c.checks, true
}

// checkSet collects the checks of one request, without duplicates.
type checkSet struct {
	// arnPrefix is "arn:<partition>:s3:::", to which a bucket name or
	// "<bucket>/<key>" is appended.
	arnPrefix string
	// sourceTagged reports whether a copy source object carries tags.
	sourceTagged         func(r *http.Request, bucket, key string) bool
	bucketARN, objectARN string
	checks               []awsauthz.Check
	seen                 map[awsauthz.Check]bool
}

func (c *checkSet) add(action, resource string) {
	ck := awsauthz.Check{Action: "s3:" + action, Resource: resource}
	if c.seen[ck] {
		return
	}

	if c.seen == nil {
		c.seen = map[awsauthz.Check]bool{}
	}

	c.seen[ck] = true
	c.checks = append(c.checks, ck)
}

func (c *checkSet) bucket(action string) { c.add(action, c.bucketARN) }
func (c *checkSet) object(action string) { c.add(action, c.objectARN) }

// iamRule adds the checks of one operation. It returns false when the
// request cannot be authorized because the handler will reject it.
type iamRule func(r *http.Request, a *opArgs, c *checkSet) bool

// bucketAction is the rule of an operation that needs one action on the
// bucket.
func bucketAction(action string) iamRule {
	return func(_ *http.Request, _ *opArgs, c *checkSet) bool {
		c.bucket(action)
		return true
	}
}

// objectAction is the rule of an operation that needs one action on the
// object, or versioned when the request names a ?versionId.
func objectAction(action, versioned string) iamRule {
	return func(r *http.Request, _ *opArgs, c *checkSet) bool {
		c.object(versionedAction(r, action, versioned))
		return true
	}
}

// versionedAction picks versioned over action when the request names a
// version and the operation has a version-specific action.
func versionedAction(r *http.Request, action, versioned string) string {
	if versioned != "" && r.URL.Query().Get("versionId") != "" {
		return versioned
	}

	return action
}

// iamRules maps every operation the handler runs to its checks. The two 405
// operations are absent, so IAMChecks reports them as unknown.
//
//nolint:gochecknoglobals // static lookup table
var iamRules = map[opID]iamRule{
	opListBuckets: func(_ *http.Request, _ *opArgs, c *checkSet) bool {
		c.add("ListAllMyBuckets", "*")
		return true
	},
	opCreateBucket:         createBucketChecks,
	opDeleteBucket:         bucketAction("DeleteBucket"),
	opHeadBucket:           bucketAction("ListBucket"),
	opListObjects:          bucketAction("ListBucket"),
	opListObjectVersions:   bucketAction("ListBucketVersions"),
	opListMultipartUploads: bucketAction("ListBucketMultipartUploads"),
	opDeleteObjects:        deleteObjectsChecks,

	opGetBucketTagging:      bucketAction("GetBucketTagging"),
	opPutBucketTagging:      bucketAction("PutBucketTagging"),
	opDeleteBucketTagging:   bucketAction("PutBucketTagging"),
	opGetBucketNotification: bucketAction("GetBucketNotification"),
	opPutBucketNotification: bucketAction("PutBucketNotification"),
	opGetBucketVersioning:   bucketAction("GetBucketVersioning"),
	opPutBucketVersioning:   bucketAction("PutBucketVersioning"),
	opGetBucketACL:          bucketAction("GetBucketAcl"),
	opPutBucketACL:          bucketAction("PutBucketAcl"),
	opGetBucketConfig:       configChecks(func(s configSubresource) string { return s.get }),
	opPutBucketConfig:       configChecks(func(s configSubresource) string { return s.put }),
	opDeleteBucketConfig:    configChecks(configDeleteAction),

	opPutObject:             putObjectChecks,
	opCreateMultipartUpload: putObjectChecks,
	opCopyObject:            copyObjectChecks,
	opUploadPart:            objectAction("PutObject", ""),
	opUploadPartCopy:        uploadPartCopyChecks,
	opCompleteMultipart:     objectAction("PutObject", ""),
	opAbortMultipartUpload:  objectAction("AbortMultipartUpload", ""),
	opListParts:             objectAction("ListMultipartUploadParts", ""),
	opGetObject:             objectAction("GetObject", "GetObjectVersion"),
	opHeadObject:            objectAction("GetObject", "GetObjectVersion"),
	opDeleteObject:          deleteObjectChecks,
	opGetObjectAttributes:   getObjectAttributesChecks,
	opRestoreObject:         objectAction("RestoreObject", ""),
	opGetObjectTagging:      objectAction("GetObjectTagging", "GetObjectVersionTagging"),
	opPutObjectTagging:      objectAction("PutObjectTagging", "PutObjectVersionTagging"),
	opDeleteObjectTagging:   objectAction("DeleteObjectTagging", "DeleteObjectVersionTagging"),
	opGetObjectRetention:    objectAction("GetObjectRetention", ""),
	opPutObjectRetention:    putObjectRetentionChecks,
	opGetObjectLegalHold:    objectAction("GetObjectLegalHold", ""),
	opPutObjectLegalHold:    objectAction("PutObjectLegalHold", ""),
	opGetObjectACL:          objectAction("GetObjectAcl", "GetObjectVersionAcl"),
	opPutObjectACL:          objectAction("PutObjectAcl", "PutObjectVersionAcl"),
}

// configChecks is the rule of a configuration sub-resource operation; pick
// selects its action from the sub-resource's entry in configSubresources.
func configChecks(pick func(configSubresource) string) iamRule {
	return func(_ *http.Request, a *opArgs, c *checkSet) bool {
		sub, ok := findConfigSubresource(func(s *configSubresource) bool { return s.key == a.sub })
		if !ok {
			return false
		}

		c.bucket(pick(sub))

		return true
	}
}

// configDeleteAction is the action of a configuration delete, which S3
// authorizes with the put action unless the sub-resource has its own.
func configDeleteAction(s configSubresource) string {
	if s.del != "" {
		return s.del
	}

	return s.put
}

// createBucketChecks: CreateBucket, plus the actions for the settings the
// request applies at creation (Object Lock, which also turns on versioning,
// a canned or explicit ACL, and object ownership).
func createBucketChecks(r *http.Request, _ *opArgs, c *checkSet) bool {
	c.bucket("CreateBucket")

	if strings.EqualFold(r.Header.Get("X-Amz-Bucket-Object-Lock-Enabled"), "true") {
		c.bucket("PutBucketObjectLockConfiguration")
		c.bucket("PutBucketVersioning")
	}

	if hasACLHeaders(r.Header) {
		c.bucket("PutBucketAcl")
	}

	if r.Header.Get("X-Amz-Object-Ownership") != "" {
		c.bucket("PutBucketOwnershipControls")
	}

	return true
}

// putObjectChecks covers PutObject and CreateMultipartUpload: s3:PutObject
// on the object, plus the actions for tags, an ACL and Object Lock settings
// sent with the upload.
func putObjectChecks(r *http.Request, _ *opArgs, c *checkSet) bool {
	c.object("PutObject")
	uploadSettingChecks(r, c, r.Header.Get("X-Amz-Tagging") != "")

	return true
}

// uploadSettingChecks adds the actions for the object settings a write
// request carries. tagged reports whether the request sets tags.
func uploadSettingChecks(r *http.Request, c *checkSet, tagged bool) {
	if tagged {
		c.object("PutObjectTagging")
	}

	if hasACLHeaders(r.Header) {
		c.object("PutObjectAcl")
	}

	if r.Header.Get(hdrObjectLockMode) != "" || r.Header.Get(hdrObjectLockRetainDate) != "" {
		c.object("PutObjectRetention")
	}

	if r.Header.Get(hdrObjectLockLegalHold) != "" {
		c.object("PutObjectLegalHold")
	}
}

// copyObjectChecks: s3:PutObject on the destination and s3:GetObject (or
// s3:GetObjectVersion) on the source named by x-amz-copy-source, read with
// the same parser the copy uses. Tags count only when the copy replaces
// them.
func copyObjectChecks(r *http.Request, _ *opArgs, c *checkSet) bool {
	c.object("PutObject")

	replaceTags := strings.EqualFold(r.Header.Get("X-Amz-Tagging-Directive"), "REPLACE")
	uploadSettingChecks(r, c, replaceTags && r.Header.Get("X-Amz-Tagging") != "")

	if !copySourceChecks(r, c) {
		return false
	}

	// With the default COPY tagging directive the source's tags are copied,
	// which needs s3:GetObjectTagging on the source and s3:PutObjectTagging on
	// the destination when the source has tags (CopyObject API reference).
	if !replaceTags {
		srcBucket, srcKey, _ := parseCopySource(r.Header.Get("X-Amz-Copy-Source"))
		if c.sourceTagged(r, srcBucket, srcKey) {
			c.add("GetObjectTagging", c.arnPrefix+srcBucket+"/"+srcKey)
			c.object("PutObjectTagging")
		}
	}

	return true
}

// objectHasTags reports whether bucket/key exists with a non-empty tag set.
func (h *Handler) objectHasTags(r *http.Request, bucket, key string) bool {
	if h.bucket == nil {
		return false
	}

	tags, err := h.bucket.GetObjectTagging(r.Context(), bucket, key)

	return err == nil && len(tags) > 0
}

// uploadPartCopyChecks: s3:PutObject on the destination and read access to
// the source.
func uploadPartCopyChecks(r *http.Request, _ *opArgs, c *checkSet) bool {
	c.object("PutObject")

	return copySourceChecks(r, c)
}

// copySourceChecks adds the read check on the copy source. A source the
// copy cannot parse is answered with 400 InvalidArgument, so it reports
// false.
func copySourceChecks(r *http.Request, c *checkSet) bool {
	srcBucket, srcKey, srcVersionID := parseCopySource(r.Header.Get("X-Amz-Copy-Source"))
	if srcBucket == "" || srcKey == "" {
		return false
	}

	action := "GetObject"
	if srcVersionID != "" {
		action = "GetObjectVersion"
	}

	c.add(action, c.arnPrefix+srcBucket+"/"+srcKey)

	return true
}

// deleteObjectChecks: s3:DeleteObject, or s3:DeleteObjectVersion for a
// ?versionId, plus s3:BypassGovernanceRetention when the request asks to
// bypass GOVERNANCE retention.
func deleteObjectChecks(r *http.Request, _ *opArgs, c *checkSet) bool {
	c.object(versionedAction(r, "DeleteObject", "DeleteObjectVersion"))

	if bypassGovernance(r) {
		c.object("BypassGovernanceRetention")
	}

	return true
}

// putObjectRetentionChecks: s3:PutObjectRetention, plus
// s3:BypassGovernanceRetention when the request bypasses GOVERNANCE mode.
func putObjectRetentionChecks(r *http.Request, _ *opArgs, c *checkSet) bool {
	c.object("PutObjectRetention")

	if bypassGovernance(r) {
		c.object("BypassGovernanceRetention")
	}

	return true
}

// getObjectAttributesChecks: GetObjectAttributes needs both s3:GetObject
// and s3:GetObjectAttributes, or both version actions for a ?versionId.
func getObjectAttributesChecks(r *http.Request, _ *opArgs, c *checkSet) bool {
	c.object(versionedAction(r, "GetObject", "GetObjectVersion"))
	c.object(versionedAction(r, "GetObjectAttributes", "GetObjectVersionAttributes"))

	return true
}

// deleteObjectsChecks authorizes every key of a DeleteObjects body as its
// own DeleteObject (or DeleteObjectVersion), plus
// s3:BypassGovernanceRetention on each when the request bypasses GOVERNANCE
// mode. It reads the body exactly as deleteObjects does, so a body the
// handler rejects (too large, malformed, or with no keys) reports false.
//
// Real S3 evaluates each key on its own and reports a denied key as an
// AccessDenied entry in the result while deleting the others. Here one
// denied key denies the whole request. That is stricter than S3, never more
// permissive.
func deleteObjectsChecks(r *http.Request, _ *opArgs, c *checkSet) bool {
	if r.Body == nil {
		return false
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxDeleteBody+1))
	if err != nil || len(body) > maxDeleteBody {
		return false
	}

	var req deleteObjectsRequest
	if err := xml.Unmarshal(body, &req); err != nil || len(req.Objects) == 0 {
		return false
	}

	bypass := bypassGovernance(r)

	for _, obj := range req.Objects {
		arn := c.bucketARN + "/" + obj.Key

		action := "DeleteObject"
		if obj.VersionID != "" {
			action = "DeleteObjectVersion"
		}

		c.add(action, arn)

		if bypass {
			c.add("BypassGovernanceRetention", arn)
		}
	}

	return true
}

// hasACLHeaders reports whether a request sets an ACL: a canned x-amz-acl or
// any x-amz-grant-* header.
func hasACLHeaders(h http.Header) bool {
	if h.Get("X-Amz-Acl") != "" {
		return true
	}

	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "X-Amz-Grant-") {
			return true
		}
	}

	return false
}
