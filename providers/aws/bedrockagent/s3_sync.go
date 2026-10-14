package bedrockagent

import (
	"context"
	"encoding/json"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// S3 data-source sync constants.
const (
	s3ARNPrefix      = "arn:aws:s3:::"
	roleARNMarker    = ":role/"
	metadataSuffix   = ".metadata.json"
	bucketARNMarker  = ":::"
	maxSyncListPages = 1000
)

// textExtensions are the S3 object types the sync indexes as text. Other
// supported types (PDF, Word, Excel) need a document parser the emulator does
// not have; the sync counts them as failed.
//
//nolint:gochecknoglobals // fixed lookup table.
var textExtensions = map[string]bool{".txt": true, ".md": true, ".html": true, ".htm": true, ".csv": true, ".json": true}

// ObjectReader is the part of S3 a knowledge-base sync reads.
type ObjectReader interface {
	ListObjects(ctx context.Context, bucket string, opts storagedriver.ListOptions) (*storagedriver.ListResult, error)
	GetObject(ctx context.Context, bucket, key string) (*storagedriver.Object, error)
}

// SetObjectStore links the mock to S3, so ingestion jobs over S3 data sources
// index the bucket's objects and S3 documents ingested directly are read.
func (m *Mock) SetObjectStore(r ObjectReader) { m.objects = r }

// RoleAuthorizer is the part of IAM an ingestion job uses to read S3 as the
// knowledge base's service role.
type RoleAuthorizer interface {
	GetRole(ctx context.Context, name string) (*iamdriver.RoleInfo, error)
	EvaluatePermission(ctx context.Context, req iamdriver.EvalRequest) iamdriver.Decision
}

// SetRoleAuthorizer links the mock to IAM. Under --enforce-auth an ingestion
// job then reads the bucket as the knowledge base's role, as Bedrock does: a
// role that does not exist or can't list the bucket fails the job, and objects
// it can't read count as failed documents.
func (m *Mock) SetRoleAuthorizer(a RoleAuthorizer) { m.roles = a }

// s3SourceConfig is the S3 part of a data source configuration.
type s3SourceConfig struct {
	Type            string `json:"type"`
	S3Configuration *struct {
		BucketArn         string   `json:"bucketArn"`
		InclusionPrefixes []string `json:"inclusionPrefixes"`
	} `json:"s3Configuration"`
}

// s3Object is one object a sync found, with its sidecar metadata file.
type s3Object struct {
	key, etag, contentType string
	metadataKey            string
}

// syncS3 makes the data source's documents match its bucket: it indexes new
// and changed text objects (with their <key>.metadata.json attributes) and
// removes documents whose object is gone. ok is false when the data source is
// not an S3 one the emulator can read. A bucket that can't be listed fails
// the job and leaves the index as it was. Callers hold mu.
func (m *Mock) syncS3(ctx context.Context, kbID string, ds *driver.DataSource) (
	stats driver.IngestionJobStatistics, ok bool, err error,
) {
	var cfg s3SourceConfig
	if m.objects == nil || json.Unmarshal(ds.DataSourceConfiguration, &cfg) != nil ||
		cfg.Type != driver.DocumentSourceS3 || cfg.S3Configuration == nil {
		return stats, false, nil
	}

	_, bucket, _ := strings.Cut(cfg.S3Configuration.BucketArn, bucketARNMarker)

	access, err := m.roleAccess(ctx, kbID)
	if err != nil {
		return stats, true, err
	}

	if !access.allows(ctx, "s3:ListBucket", s3ARNPrefix+bucket) {
		return stats, true, errors.Newf(errors.PermissionDenied,
			"%s is not authorized to perform: s3:ListBucket on resource: %s%s", access.roleARN, s3ARNPrefix, bucket)
	}

	all, err := m.listObjects(ctx, bucket, cfg.S3Configuration.InclusionPrefixes)
	if err != nil {
		return stats, true, err
	}

	objects, sidecars := splitSidecars(all)
	stats.NumberOfMetadataDocumentsScanned = int64(sidecars)
	seen := map[string]bool{}

	for _, o := range objects {
		stats.NumberOfDocumentsScanned++

		if !access.allows(ctx, "s3:GetObject", s3ARNPrefix+bucket+"/"+o.key) {
			stats.NumberOfDocumentsFailed++
			seen[documentKey(kbID, ds.ID, driver.DocumentIdentifier{DataSourceType: driver.DocumentSourceS3,
				S3URI: s3URIPrefix + bucket + "/" + o.key})] = true

			continue
		}

		seen[m.indexObject(ctx, kbID, ds.ID, bucket, o, &stats)] = true
	}

	stats.NumberOfDocumentsDeleted = m.pruneSynced(kbID, ds.ID, seen)

	return stats, true, nil
}

// indexObject indexes one S3 object into the data source when it is new or
// changed, counts the outcome in stats and returns the document's key.
func (m *Mock) indexObject(ctx context.Context, kbID, dsID, bucket string, o s3Object, stats *driver.IngestionJobStatistics) string {
	id := driver.DocumentIdentifier{DataSourceType: driver.DocumentSourceS3, S3URI: s3URIPrefix + bucket + "/" + o.key}
	key := documentKey(kbID, dsID, id)

	text, ok := m.readText(ctx, bucket, o.key, o.contentType)
	if !ok {
		stats.NumberOfDocumentsFailed++

		return key
	}

	metadata := m.readSidecar(ctx, bucket, o.metadataKey)
	prev, existed := m.documents.Get(key)

	switch {
	case !existed:
		stats.NumberOfNewDocumentsIndexed++
	case prev.ETag != o.etag:
		stats.NumberOfModifiedDocumentsIndexed++
	case string(prev.Metadata) != string(metadata):
		stats.NumberOfMetadataDocumentsModified++
	default:
		return key
	}

	m.documents.Set(key, &storedDocument{
		Detail: driver.DocumentDetail{KnowledgeBaseID: kbID, DataSourceID: dsID, Identifier: id,
			Status: driver.DocumentIndexed, UpdatedAt: m.now()},
		Metadata: metadata, Text: text, ETag: o.etag, Synced: true,
	})

	return key
}

// pruneSynced removes the data source's synced documents whose object is gone
// and returns how many it removed.
func (m *Mock) pruneSynced(kbID, dsID string, seen map[string]bool) int64 {
	var deleted int64

	for key, d := range m.documents.All() {
		if d.Synced && strings.HasPrefix(key, versionKey(kbID, dsID)+"/") && !seen[key] {
			m.documents.Delete(key)

			deleted++
		}
	}

	return deleted
}

// roleAccess is how an ingestion job may read S3: as the knowledge base's
// role under --enforce-auth, without checks otherwise.
type roleAccess struct {
	roles    RoleAuthorizer
	roleName string
	roleARN  string
}

// roleAccess resolves the knowledge base's service role when access is
// enforced; a role that does not exist fails the job.
func (m *Mock) roleAccess(ctx context.Context, kbID string) (roleAccess, error) {
	if m.roles == nil || !m.opts.EnforceAuth {
		return roleAccess{}, nil
	}

	kb, ok := m.knowledge.Get(kbID)
	if !ok {
		return roleAccess{}, errors.Newf(errors.NotFound, "knowledge base %q not found", kbID)
	}

	name := kb.RoleArn
	if _, rest, found := strings.Cut(kb.RoleArn, roleARNMarker); found {
		name = path.Base(rest)
	}

	if _, err := m.roles.GetRole(ctx, name); err != nil {
		return roleAccess{}, errors.Newf(errors.PermissionDenied,
			"The knowledge base's service role %s can't be assumed: %s", kb.RoleArn, errors.Message(err))
	}

	return roleAccess{roles: m.roles, roleName: name, roleARN: kb.RoleArn}, nil
}

// allows reports whether the role may perform action on resource; with no
// role (access not enforced) everything is allowed.
func (a roleAccess) allows(ctx context.Context, action, resource string) bool {
	if a.roles == nil {
		return true
	}

	return a.roles.EvaluatePermission(ctx, iamdriver.EvalRequest{
		Principal: a.roleName, Action: action, Resource: resource, ResourceKnown: true,
	}) == iamdriver.DecisionAllowed
}

// listObjects lists the bucket's objects under the inclusion prefixes (all of
// it when there are none), by key.
func (m *Mock) listObjects(ctx context.Context, bucket string, prefixes []string) (map[string]storagedriver.ObjectInfo, error) {
	if len(prefixes) == 0 {
		prefixes = []string{""}
	}

	all := map[string]storagedriver.ObjectInfo{}

	for _, prefix := range prefixes {
		token := ""

		for range maxSyncListPages {
			res, err := m.objects.ListObjects(ctx, bucket, storagedriver.ListOptions{Prefix: prefix, PageToken: token})
			if err != nil {
				return nil, errors.Newf(errors.FailedPrecondition, "The data source's S3 bucket %s can't be listed: %s",
					bucket, errors.Message(err))
			}

			for i := range res.Objects {
				all[res.Objects[i].Key] = res.Objects[i]
			}

			if token = res.NextPageToken; token == "" {
				break
			}
		}
	}

	return all, nil
}

// splitSidecars separates documents from <key>.metadata.json files, pairing
// each document with its sidecar, sorted by key.
func splitSidecars(all map[string]storagedriver.ObjectInfo) (objects []s3Object, sidecars int) {
	for _, key := range slices.Sorted(maps.Keys(all)) {
		switch {
		case strings.HasSuffix(key, metadataSuffix):
			sidecars++
		case strings.HasSuffix(key, "/"):
		default:
			o := s3Object{key: key, etag: all[key].ETag, contentType: all[key].ContentType}
			if _, ok := all[key+metadataSuffix]; ok {
				o.metadataKey = key + metadataSuffix
			}

			objects = append(objects, o)
		}
	}

	return objects, sidecars
}

// readText returns an object's text when it is a text type the sync indexes.
func (m *Mock) readText(ctx context.Context, bucket, key, contentType string) (string, bool) {
	if !textExtensions[strings.ToLower(path.Ext(key))] && !strings.HasPrefix(contentType, "text/") {
		return "", false
	}

	obj, err := m.objects.GetObject(ctx, bucket, key)
	if err != nil || !utf8.Valid(obj.Data) {
		return "", false
	}

	return string(obj.Data), true
}

// readSidecar converts a <key>.metadata.json file ({"metadataAttributes":
// {name: value}}, each value plain JSON or a typed {"value": {...}}) into the
// IN_LINE_ATTRIBUTE document metadata IngestKnowledgeBaseDocuments takes.
func (m *Mock) readSidecar(ctx context.Context, bucket, key string) json.RawMessage {
	if key == "" {
		return nil
	}

	obj, err := m.objects.GetObject(ctx, bucket, key)
	if err != nil {
		return nil
	}

	var file struct {
		MetadataAttributes map[string]json.RawMessage `json:"metadataAttributes"`
	}

	if json.Unmarshal(obj.Data, &file) != nil || len(file.MetadataAttributes) == 0 {
		return nil
	}

	attrs := make([]inlineAttribute, 0, len(file.MetadataAttributes))

	for _, name := range slices.Sorted(maps.Keys(file.MetadataAttributes)) {
		if v, ok := sidecarValue(file.MetadataAttributes[name]); ok {
			attrs = append(attrs, inlineAttribute{Key: name, Value: v})
		}
	}

	out, err := json.Marshal(documentMetadata{Type: metadataInline, InlineAttributes: attrs})
	if err != nil {
		return nil
	}

	return out
}

// sidecarValue reads one sidecar attribute: a typed {"value": {...}} or a
// plain string, number, boolean or list of strings.
func sidecarValue(raw json.RawMessage) (attributeValue, bool) {
	var typed struct {
		Value *attributeValue `json:"value"`
	}

	if json.Unmarshal(raw, &typed) == nil && typed.Value != nil && typed.Value.Type != "" {
		return *typed.Value, true
	}

	var plain any
	if json.Unmarshal(raw, &plain) != nil {
		return attributeValue{}, false
	}

	return plainAttribute(plain)
}

// plainAttribute types a plain JSON sidecar value.
func plainAttribute(plain any) (attributeValue, bool) {
	switch v := plain.(type) {
	case string:
		return attributeValue{Type: attrString, StringValue: &v}, true
	case float64:
		return attributeValue{Type: attrNumber, NumberValue: &v}, true
	case bool:
		return attributeValue{Type: attrBoolean, BooleanValue: &v}, true
	case []any:
		list := make([]string, 0, len(v))

		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return attributeValue{}, false
			}

			list = append(list, s)
		}

		return attributeValue{Type: attrStringList, StringListValue: list}, true
	default:
		return attributeValue{}, false
	}
}
