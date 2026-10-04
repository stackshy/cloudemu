package s3

import (
	"net/http"
	"net/url"
)

// opID names the S3 operation a request runs. classify picks it from the
// request and ServeHTTP runs it through dispatchTable, so the IAM checks
// (IAMChecks) and the code that executes always agree on the operation.
type opID string

// The S3 operations this handler serves. opNotAllowed is every request it
// answers with 405 MethodNotAllowed.
const (
	opNotAllowed opID = "MethodNotAllowed"

	opListBuckets          opID = "ListBuckets"
	opCreateBucket         opID = "CreateBucket"
	opDeleteBucket         opID = "DeleteBucket"
	opHeadBucket           opID = "HeadBucket"
	opListObjects          opID = "ListObjects"
	opListObjectVersions   opID = "ListObjectVersions"
	opListMultipartUploads opID = "ListMultipartUploads"
	opDeleteObjects        opID = "DeleteObjects"

	opGetBucketTagging       opID = "GetBucketTagging"
	opPutBucketTagging       opID = "PutBucketTagging"
	opDeleteBucketTagging    opID = "DeleteBucketTagging"
	opGetBucketNotification  opID = "GetBucketNotificationConfiguration"
	opPutBucketNotification  opID = "PutBucketNotificationConfiguration"
	opNotificationNotAllowed opID = "NotificationMethodNotAllowed"
	opGetBucketVersioning    opID = "GetBucketVersioning"
	opPutBucketVersioning    opID = "PutBucketVersioning"
	opGetBucketACL           opID = "GetBucketAcl"
	opPutBucketACL           opID = "PutBucketAcl"
	opGetBucketConfig        opID = "GetBucketConfig"
	opPutBucketConfig        opID = "PutBucketConfig"
	opDeleteBucketConfig     opID = "DeleteBucketConfig"

	opPutObject             opID = "PutObject"
	opCopyObject            opID = "CopyObject"
	opGetObject             opID = "GetObject"
	opHeadObject            opID = "HeadObject"
	opDeleteObject          opID = "DeleteObject"
	opGetObjectAttributes   opID = "GetObjectAttributes"
	opGetObjectTagging      opID = "GetObjectTagging"
	opPutObjectTagging      opID = "PutObjectTagging"
	opDeleteObjectTagging   opID = "DeleteObjectTagging"
	opGetObjectRetention    opID = "GetObjectRetention"
	opPutObjectRetention    opID = "PutObjectRetention"
	opGetObjectLegalHold    opID = "GetObjectLegalHold"
	opPutObjectLegalHold    opID = "PutObjectLegalHold"
	opGetObjectACL          opID = "GetObjectAcl"
	opPutObjectACL          opID = "PutObjectAcl"
	opCreateMultipartUpload opID = "CreateMultipartUpload"
	opUploadPart            opID = "UploadPart"
	opUploadPartCopy        opID = "UploadPartCopy"
	opCompleteMultipart     opID = "CompleteMultipartUpload"
	opAbortMultipartUpload  opID = "AbortMultipartUpload"
	opListParts             opID = "ListParts"
)

// opArgs is what classify extracts from the request for the operation.
type opArgs struct {
	bucket, key string
	// uploadID is the ?uploadId of a multipart operation.
	uploadID string
	// sub is the bucket configuration sub-resource (policy, cors, ...).
	sub string
	// notAllowed is the 405 message for opNotAllowed.
	notAllowed string
}

const (
	msgNotAllowed        = "method not allowed"
	msgUploadsNotAllowed = "method not allowed on ?uploads"
)

// byMethod maps a request method to an operation.
type byMethod map[string]opID

// pick returns the operation for method, or opNotAllowed.
func (m byMethod) pick(method string) opID {
	if op, ok := m[method]; ok {
		return op
	}

	return opNotAllowed
}

// The method tables of the sub-resources whose other methods answer 405.
//
//nolint:gochecknoglobals // static dispatch tables
var (
	bucketMethods = byMethod{
		http.MethodPut: opCreateBucket, http.MethodDelete: opDeleteBucket,
		http.MethodGet: opListObjects, http.MethodHead: opHeadBucket,
	}
	bucketTaggingMethods = byMethod{
		http.MethodPut: opPutBucketTagging, http.MethodGet: opGetBucketTagging, http.MethodDelete: opDeleteBucketTagging,
	}
	notificationMethods = byMethod{http.MethodPut: opPutBucketNotification, http.MethodGet: opGetBucketNotification}
	versioningMethods   = byMethod{http.MethodPut: opPutBucketVersioning, http.MethodGet: opGetBucketVersioning}
	objectMethods       = byMethod{
		http.MethodPut: opPutObject, http.MethodGet: opGetObject,
		http.MethodHead: opHeadObject, http.MethodDelete: opDeleteObject,
	}
	objectTaggingMethods = byMethod{
		http.MethodPut: opPutObjectTagging, http.MethodGet: opGetObjectTagging, http.MethodDelete: opDeleteObjectTagging,
	}
	multipartMethods = byMethod{
		http.MethodPut: opUploadPart, http.MethodPost: opCompleteMultipart,
		http.MethodDelete: opAbortMultipartUpload, http.MethodGet: opListParts,
	}
)

// classify names the operation of an S3 request and its arguments. It reads
// only the request line and headers, never the body, and has no side
// effects; both ServeHTTP and IAMChecks call it.
func classify(r *http.Request) (opID, opArgs) {
	bucket, key := parsePath(r.URL.Path)
	a := opArgs{bucket: bucket, key: key, notAllowed: msgNotAllowed}

	switch {
	case bucket == "":
		if r.Method != http.MethodGet {
			return opNotAllowed, a
		}

		return opListBuckets, a
	case key == "":
		op := classifyBucket(r, &a)
		return op, a
	default:
		op := classifyObject(r, &a)
		return op, a
	}
}

// classifyBucket names a bucket-level operation.
func classifyBucket(r *http.Request, a *opArgs) opID {
	q := r.URL.Query()

	if op, ok := bucketSubresourceOp(r.Method, q, a); ok {
		return op
	}

	// Bucket configuration sub-resources (policy, cors, encryption, location,
	// ...). Any method other than PUT and DELETE reads the document.
	if sub := configSubresourceKey(q); sub != "" {
		a.sub = sub

		switch r.Method {
		case http.MethodPut:
			return opPutBucketConfig
		case http.MethodDelete:
			return opDeleteBucketConfig
		default:
			return opGetBucketConfig
		}
	}

	return bucketMethods.pick(r.Method)
}

// bucketSubresourceOp names the operations of the bucket sub-resources that
// take precedence over the configuration documents. ok=false when none is
// present.
func bucketSubresourceOp(method string, q url.Values, a *opArgs) (opID, bool) {
	switch {
	case q.Has("tagging"):
		return bucketTaggingMethods.pick(method), true
	case q.Has("notification"):
		if op := notificationMethods.pick(method); op != opNotAllowed {
			return op, true
		}

		return opNotificationNotAllowed, true
	case q.Has("versioning"):
		return versioningMethods.pick(method), true
	case q.Has("uploads"):
		// GET /{bucket}?uploads is ListMultipartUploads. Any other method is
		// rejected rather than creating or deleting the bucket.
		a.notAllowed = msgUploadsNotAllowed
		return byMethod{http.MethodGet: opListMultipartUploads}.pick(method), true
	case q.Has("versions"):
		a.notAllowed = "method not allowed on ?versions"
		return byMethod{http.MethodGet: opListObjectVersions}.pick(method), true
	case q.Has("delete"):
		// POST /{bucket}?delete is DeleteObjects, whatever the method.
		return opDeleteObjects, true
	case q.Has("acl"):
		// GET returns a canned ACL; a write is a no-op so it does not fall
		// through to CreateBucket.
		return aclOp(method, opGetBucketACL, opPutBucketACL), true
	}

	return "", false
}

// classifyObject names an object-level operation.
func classifyObject(r *http.Request, a *opArgs) opID {
	q := r.URL.Query()

	switch {
	case q.Has("attributes"):
		// GET /{bucket}/{key}?attributes is GetObjectAttributes. Without this it
		// would fall through to GetObject.
		return byMethod{http.MethodGet: opGetObjectAttributes}.pick(r.Method)
	case q.Has("tagging"):
		return objectTaggingMethods.pick(r.Method)
	case q.Has("retention"):
		return putOrGet(r.Method, opPutObjectRetention, opGetObjectRetention)
	case q.Has("legal-hold"):
		return putOrGet(r.Method, opPutObjectLegalHold, opGetObjectLegalHold)
	case q.Has("uploads"):
		// POST /{bucket}/{key}?uploads is CreateMultipartUpload; any other method
		// is rejected rather than falling through to a plain object operation.
		a.notAllowed = msgUploadsNotAllowed
		return byMethod{http.MethodPost: opCreateMultipartUpload}.pick(r.Method)
	case q.Has("uploadId"):
		a.uploadID = q.Get("uploadId")
		return withCopySource(r, multipartMethods.pick(r.Method), opUploadPart, opUploadPartCopy)
	case q.Has("acl"):
		// GET returns a canned ACL; a write is a no-op so it does not overwrite
		// the object with the ACL body.
		return aclOp(r.Method, opGetObjectACL, opPutObjectACL)
	}

	return withCopySource(r, objectMethods.pick(r.Method), opPutObject, opCopyObject)
}

// withCopySource turns the plain upload op into its copy variant when the
// request names a copy source.
func withCopySource(r *http.Request, op, upload, uploadCopy opID) opID {
	if op == upload && r.Header.Get("X-Amz-Copy-Source") != "" {
		return uploadCopy
	}

	return op
}

// putOrGet is put for PUT and get for every other method.
func putOrGet(method string, put, get opID) opID {
	if method == http.MethodPut {
		return put
	}

	return get
}

// aclOp is get for GET; any other method writes the ACL.
func aclOp(method string, get, put opID) opID {
	if method == http.MethodGet {
		return get
	}

	return put
}

// opHandler runs one classified operation.
type opHandler func(h *Handler, w http.ResponseWriter, r *http.Request, a *opArgs)

// dispatchTable runs each operation classify can return.
//
//nolint:gochecknoglobals // static dispatch table
var dispatchTable = map[opID]opHandler{
	opNotAllowed: func(_ *Handler, w http.ResponseWriter, _ *http.Request, a *opArgs) {
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", a.notAllowed)
	},
	opListBuckets: func(h *Handler, w http.ResponseWriter, r *http.Request, _ *opArgs) { h.listBuckets(w, r) },

	opCreateBucket:         bucketOp((*Handler).createBucket),
	opDeleteBucket:         bucketOp((*Handler).deleteBucket),
	opHeadBucket:           bucketOp((*Handler).headBucket),
	opListObjects:          bucketOp((*Handler).listObjects),
	opListObjectVersions:   bucketOp((*Handler).listObjectVersions),
	opListMultipartUploads: bucketOp((*Handler).listMultipartUploads),
	opDeleteObjects:        bucketOp((*Handler).deleteObjects),
	opGetBucketTagging:     bucketOp((*Handler).getBucketTagging),
	opPutBucketTagging:     bucketOp((*Handler).putBucketTagging),
	opDeleteBucketTagging:  bucketOp((*Handler).deleteBucketTagging),
	opGetBucketVersioning:  bucketOp((*Handler).getBucketVersioning),
	opPutBucketVersioning:  bucketOp((*Handler).putBucketVersioning),

	opGetBucketNotification:  notificationOp((*Handler).getBucketNotification),
	opPutBucketNotification:  notificationOp((*Handler).putBucketNotification),
	opNotificationNotAllowed: notificationOp(nil),

	opGetBucketACL: func(_ *Handler, w http.ResponseWriter, _ *http.Request, _ *opArgs) { writeCannedACL(w) },
	opPutBucketACL: func(_ *Handler, w http.ResponseWriter, _ *http.Request, _ *opArgs) { w.WriteHeader(http.StatusOK) },
	opGetObjectACL: func(_ *Handler, w http.ResponseWriter, _ *http.Request, _ *opArgs) { writeCannedACL(w) },
	opPutObjectACL: func(_ *Handler, w http.ResponseWriter, _ *http.Request, _ *opArgs) { w.WriteHeader(http.StatusOK) },

	opGetBucketConfig:    configOp((*Handler).getBucketConfig),
	opPutBucketConfig:    configOp((*Handler).putBucketConfig),
	opDeleteBucketConfig: configOp((*Handler).deleteBucketConfig),

	opPutObject:             objectOp((*Handler).putObject),
	opCopyObject:            objectOp((*Handler).copyObject),
	opGetObject:             objectOp((*Handler).getObject),
	opHeadObject:            objectOp((*Handler).headObject),
	opDeleteObject:          objectOp((*Handler).deleteObject),
	opGetObjectAttributes:   objectOp((*Handler).getObjectAttributes),
	opGetObjectTagging:      objectOp((*Handler).getObjectTagging),
	opPutObjectTagging:      objectOp((*Handler).putObjectTagging),
	opDeleteObjectTagging:   objectOp((*Handler).deleteObjectTagging),
	opGetObjectRetention:    objectOp((*Handler).getRetention),
	opPutObjectRetention:    objectOp((*Handler).putRetention),
	opGetObjectLegalHold:    objectOp((*Handler).getLegalHold),
	opPutObjectLegalHold:    objectOp((*Handler).putLegalHold),
	opCreateMultipartUpload: objectOp((*Handler).createMultipartUpload),

	opUploadPart:           uploadOp((*Handler).uploadPart),
	opUploadPartCopy:       uploadOp((*Handler).uploadPartCopy),
	opCompleteMultipart:    uploadOp((*Handler).completeMultipartUpload),
	opAbortMultipartUpload: uploadOp((*Handler).abortMultipartUpload),
	opListParts:            uploadOp((*Handler).listParts),
}

func bucketOp(f func(*Handler, http.ResponseWriter, *http.Request, string)) opHandler {
	return func(h *Handler, w http.ResponseWriter, r *http.Request, a *opArgs) { f(h, w, r, a.bucket) }
}

func objectOp(f func(*Handler, http.ResponseWriter, *http.Request, string, string)) opHandler {
	return func(h *Handler, w http.ResponseWriter, r *http.Request, a *opArgs) { f(h, w, r, a.bucket, a.key) }
}

func uploadOp(f func(*Handler, http.ResponseWriter, *http.Request, string, string, string)) opHandler {
	return func(h *Handler, w http.ResponseWriter, r *http.Request, a *opArgs) {
		f(h, w, r, a.bucket, a.key, a.uploadID)
	}
}

func configOp(f func(*Handler, http.ResponseWriter, *http.Request, string, string)) opHandler {
	return func(h *Handler, w http.ResponseWriter, r *http.Request, a *opArgs) { f(h, w, r, a.bucket, a.sub) }
}

// notificationOp runs a ?notification operation on a driver that supports
// bucket notifications, and answers 501 otherwise. A nil f is the 405 for a
// method the sub-resource does not serve.
func notificationOp(f func(*Handler, http.ResponseWriter, *http.Request, string, bucketNotifier)) opHandler {
	return func(h *Handler, w http.ResponseWriter, r *http.Request, a *opArgs) {
		notifier, ok := h.bucket.(bucketNotifier)
		if !ok {
			writeError(w, http.StatusNotImplemented, "NotImplemented", "notifications not supported")
			return
		}

		if f == nil {
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", msgNotAllowed)
			return
		}

		f(h, w, r, a.bucket, notifier)
	}
}

// ServeHTTP classifies the request and runs the operation.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op, a := classify(r)
	dispatchTable[op](h, w, r, &a)
}
