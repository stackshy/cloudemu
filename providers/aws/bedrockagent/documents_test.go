package bedrockagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
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
