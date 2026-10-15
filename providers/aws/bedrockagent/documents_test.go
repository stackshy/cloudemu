package bedrockagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	s3prov "github.com/stackshy/cloudemu/v2/providers/aws/s3"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

func TestDocumentsRules(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	kb := mustKB(t, m, "kb")
	web := mustDataSource(t, m, kb.ID, "WEB")
	s3 := mustDataSource(t, m, kb.ID, "S3")
	s3ID := driver.DocumentIdentifier{DataSourceType: "S3", S3URI: "s3://bucket/a.txt"}

	_, err := m.GetKnowledgeBaseDocuments(ctx, kb.ID, web, []driver.DocumentIdentifier{s3ID})
	assert.True(t, cerrors.IsInvalidArgument(err), "a WEB data source has no documents API")

	_, err = m.GetKnowledgeBaseDocuments(ctx, kb.ID, s3, nil)
	assert.True(t, cerrors.IsInvalidArgument(err), "1 to 10 identifiers")

	_, err = m.GetKnowledgeBaseDocuments(ctx, kb.ID, s3, []driver.DocumentIdentifier{{DataSourceType: "S3", S3URI: "https://x"}})
	assert.True(t, cerrors.IsInvalidArgument(err), "S3 URIs start with s3://")

	_, err = m.IngestKnowledgeBaseDocuments(ctx, kb.ID, s3, []driver.KnowledgeBaseDocument{{Content: []byte(`"x"`)}})
	assert.True(t, cerrors.IsInvalidArgument(err))

	docs, err := m.IngestKnowledgeBaseDocuments(ctx, kb.ID, s3, []driver.KnowledgeBaseDocument{{
		Content: []byte(`{"dataSourceType":"S3","s3":{"s3Location":{"uri":"s3://bucket/a.txt"}}}`),
	}})
	require.NoError(t, err)
	assert.Equal(t, driver.DocumentIndexed, docs[0].Status)

	gone, err := m.DeleteKnowledgeBaseDocuments(ctx, kb.ID, s3, []driver.DocumentIdentifier{
		s3ID, {DataSourceType: "S3", S3URI: "s3://bucket/missing.txt"},
	})
	require.NoError(t, err)
	assert.Equal(t, driver.DocumentDeleting, gone[0].Status)
	assert.Equal(t, driver.DocumentNotFound, gone[1].Status)

	_, err = m.IngestKnowledgeBaseDocuments(ctx, kb.ID, "MISSING000", []driver.KnowledgeBaseDocument{{}})
	assert.True(t, cerrors.IsNotFound(err))

	// Deleting the knowledge base takes its documents.
	_, err = m.IngestKnowledgeBaseDocuments(ctx, kb.ID, s3, []driver.KnowledgeBaseDocument{{
		Content: []byte(`{"dataSourceType":"S3","s3":{"s3Location":{"uri":"s3://bucket/b.txt"}}}`),
	}})
	require.NoError(t, err)

	_, err = m.DeleteKnowledgeBase(ctx, kb.ID)
	require.NoError(t, err)
	assert.Empty(t, m.documents.All())
}

func TestDocumentMetadataEdges(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions()
	m := New(opts)
	s3 := s3prov.New(opts)
	m.SetObjectStore(s3)

	require.NoError(t, s3.CreateBucket(ctx, "b"))
	require.NoError(t, s3.PutObject(ctx, "b", "bad.json", []byte("not json"), "", nil))
	require.NoError(t, s3.PutObject(ctx, "b", "empty.json", []byte(`{"metadataAttributes":{}}`), "", nil))
	require.NoError(t, s3.PutObject(ctx, "b", "bin.txt", []byte{0xff, 0xfe}, "", nil))
	require.NoError(t, s3.PutObject(ctx, "b", "mixed.json",
		[]byte(`{"metadataAttributes":{"ok":"v","list":["a",1],"obj":{"x":1},"none":null}}`), "", nil))

	assert.Nil(t, m.readSidecar(ctx, "b", ""), "no sidecar key")
	assert.Nil(t, m.readSidecar(ctx, "b", "missing.json"), "a missing sidecar")
	assert.Nil(t, m.readSidecar(ctx, "b", "bad.json"), "a sidecar that is not JSON")
	assert.Nil(t, m.readSidecar(ctx, "b", "empty.json"), "a sidecar with no attributes")
	assert.Equal(t, map[string]any{"ok": "v"}, documentAttributes(m.readSidecar(ctx, "b", "mixed.json")),
		"lists of non-strings, objects and nulls are skipped")

	_, ok := m.readText(ctx, "b", "bin.txt", "")
	assert.False(t, ok, "bytes that are not UTF-8 are not text")

	raw := json.RawMessage(`{"type":"S3_LOCATION","s3Location":{"uri":"s3://no-key"}}`)
	assert.Equal(t, raw, m.resolveS3Metadata(ctx, raw), "a metadata URI with no key is kept as sent")

	missing := json.RawMessage(`{"type":"S3_LOCATION","s3Location":{"uri":"s3://b/missing.json"}}`)
	assert.Equal(t, missing, m.resolveS3Metadata(ctx, missing), "a metadata file that can't be read is kept as sent")

	assert.Empty(t, m.s3DocumentText(ctx, driver.DocumentIdentifier{DataSourceType: driver.DocumentSourceS3, S3URI: "s3://no-key"}))
	assert.Empty(t, m.s3DocumentText(ctx, driver.DocumentIdentifier{DataSourceType: driver.DocumentSourceCustom, CustomID: "x"}))

	assert.Nil(t, documentAttributes(json.RawMessage(`{"type":"S3_LOCATION"}`)), "only inline attributes are read")
	assert.Empty(t, documentAttributes(json.RawMessage(`{"type":"IN_LINE_ATTRIBUTE","inlineAttributes":[{"key":"k","value":{"type":"NUMBER"}}]}`)),
		"a typed value with no value is skipped")

	_, ok = sidecarValue(json.RawMessage(`{`))
	assert.False(t, ok)
}
