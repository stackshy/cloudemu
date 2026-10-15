package bedrockagent_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func customDoc(id, text string) batypes.KnowledgeBaseDocument {
	return batypes.KnowledgeBaseDocument{Content: &batypes.DocumentContent{
		DataSourceType: batypes.ContentDataSourceTypeCustom,
		Custom: &batypes.CustomContent{
			CustomDocumentIdentifier: &batypes.CustomDocumentIdentifier{Id: aws.String(id)},
			SourceType:               batypes.CustomSourceTypeInLine,
			InlineContent: &batypes.InlineContent{
				Type: batypes.InlineContentTypeText, TextContent: &batypes.TextContentDoc{Data: aws.String(text)},
			},
		},
	}}
}

func customID(id string) batypes.DocumentIdentifier {
	return batypes.DocumentIdentifier{
		DataSourceType: batypes.ContentDataSourceTypeCustom,
		Custom:         &batypes.CustomDocumentIdentifier{Id: aws.String(id)},
	}
}

func TestSDKKnowledgeBaseDocuments(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	kbID := createKnowledgeBase(t, client, "custom")
	dsID := createDataSource(t, client, kbID, batypes.DataSourceTypeCustom)

	ingested, err := client.IngestKnowledgeBaseDocuments(ctx, &awsba.IngestKnowledgeBaseDocumentsInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID),
		Documents: []batypes.KnowledgeBaseDocument{customDoc("doc-1", "hello"), customDoc("doc-2", "world")},
	})
	require.NoError(t, err)
	requireStatus(t, ingested.ResultMetadata, http.StatusAccepted)
	require.Len(t, ingested.DocumentDetails, 2)
	assert.Equal(t, batypes.DocumentStatusIndexed, ingested.DocumentDetails[0].Status)
	assert.Equal(t, "doc-1", aws.ToString(ingested.DocumentDetails[0].Identifier.Custom.Id))

	got, err := client.GetKnowledgeBaseDocuments(ctx, &awsba.GetKnowledgeBaseDocumentsInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID),
		DocumentIdentifiers: []batypes.DocumentIdentifier{customID("doc-1"), customID("nope")},
	})
	require.NoError(t, err)
	require.Len(t, got.DocumentDetails, 2)
	assert.Equal(t, batypes.DocumentStatusIndexed, got.DocumentDetails[0].Status)
	assert.Equal(t, batypes.DocumentStatusNotFound, got.DocumentDetails[1].Status)

	list, err := client.ListKnowledgeBaseDocuments(ctx, &awsba.ListKnowledgeBaseDocumentsInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), MaxResults: aws.Int32(1),
	})
	require.NoError(t, err)
	require.Len(t, list.DocumentDetails, 1)
	require.NotNil(t, list.NextToken)

	job, err := client.StartIngestionJob(ctx, &awsba.StartIngestionJobInput{KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID)})
	require.NoError(t, err)
	assert.Equal(t, int64(2), job.IngestionJob.Statistics.NumberOfDocumentsScanned)

	deleted, err := client.DeleteKnowledgeBaseDocuments(ctx, &awsba.DeleteKnowledgeBaseDocumentsInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID),
		DocumentIdentifiers: []batypes.DocumentIdentifier{customID("doc-1")},
	})
	require.NoError(t, err)
	requireStatus(t, deleted.ResultMetadata, http.StatusAccepted)
	assert.Equal(t, batypes.DocumentStatusDeleting, deleted.DocumentDetails[0].Status)

	after, err := client.ListKnowledgeBaseDocuments(ctx, &awsba.ListKnowledgeBaseDocumentsInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID),
	})
	require.NoError(t, err)
	assert.Len(t, after.DocumentDetails, 1)

	s3Doc := batypes.KnowledgeBaseDocument{Content: &batypes.DocumentContent{
		DataSourceType: batypes.ContentDataSourceTypeS3,
		S3:             &batypes.S3Content{S3Location: &batypes.S3Location{Uri: aws.String("s3://bucket/a.txt")}},
	}}
	_, err = client.IngestKnowledgeBaseDocuments(ctx, &awsba.IngestKnowledgeBaseDocumentsInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), Documents: []batypes.KnowledgeBaseDocument{s3Doc},
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	tooMany := make([]batypes.KnowledgeBaseDocument, 11)
	for i := range tooMany {
		tooMany[i] = customDoc("d", "x")
	}

	_, err = client.IngestKnowledgeBaseDocuments(ctx, &awsba.IngestKnowledgeBaseDocumentsInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), Documents: tooMany,
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)
}
