package bedrockagentruntime_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsba "github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	batypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	agentruntime "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/document"
	rttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var faqDocs = map[string]string{ //nolint:gochecknoglobals // test fixture.
	"refunds":  "Refunds are processed within 5 business days.",
	"shipping": "Shipping takes 2 days within the country.",
	"returns":  "Returns are accepted for 30 days.",
}

func TestSDKRetrieveFromIngestedDocuments(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	kbID, dsID := s.newKnowledgeBase(t, "faq", faqDocs)

	first, err := s.runtime.Retrieve(ctx, &agentruntime.RetrieveInput{
		KnowledgeBaseId: aws.String(kbID), RetrievalQuery: &rttypes.KnowledgeBaseQuery{Text: aws.String("how long do refunds take")},
		RetrievalConfiguration: &rttypes.KnowledgeBaseRetrievalConfiguration{
			VectorSearchConfiguration: &rttypes.KnowledgeBaseVectorSearchConfiguration{NumberOfResults: aws.Int32(1)},
		},
	})
	require.NoError(t, err)
	require.Len(t, first.RetrievalResults, 1)
	assert.Equal(t, faqDocs["refunds"], aws.ToString(first.RetrievalResults[0].Content.Text), "the best match comes first")
	assert.Equal(t, rttypes.RetrievalResultLocationTypeCustom, first.RetrievalResults[0].Location.Type)
	assert.Equal(t, "refunds", aws.ToString(first.RetrievalResults[0].Location.CustomDocumentLocation.Id))
	require.NotNil(t, first.NextToken)

	seen := 1
	next := first.NextToken

	for next != nil {
		page, err := s.runtime.Retrieve(ctx, &agentruntime.RetrieveInput{
			KnowledgeBaseId: aws.String(kbID), RetrievalQuery: &rttypes.KnowledgeBaseQuery{Text: aws.String("how long do refunds take")},
			RetrievalConfiguration: &rttypes.KnowledgeBaseRetrievalConfiguration{
				VectorSearchConfiguration: &rttypes.KnowledgeBaseVectorSearchConfiguration{NumberOfResults: aws.Int32(1)},
			},
			NextToken: next,
		})
		require.NoError(t, err)

		seen += len(page.RetrievalResults)
		next = page.NextToken
	}

	assert.Equal(t, 3, seen, "paging walks every document once")

	empty, _ := s.newKnowledgeBase(t, "empty", nil)
	res, err := s.runtime.Retrieve(ctx, &agentruntime.RetrieveInput{
		KnowledgeBaseId: aws.String(empty), RetrievalQuery: &rttypes.KnowledgeBaseQuery{Text: aws.String("anything")},
	})
	require.NoError(t, err)
	assert.Empty(t, res.RetrievalResults, "an empty knowledge base returns nothing")

	_, err = s.runtime.Retrieve(ctx, &agentruntime.RetrieveInput{
		KnowledgeBaseId: aws.String("MISSING000"), RetrievalQuery: &rttypes.KnowledgeBaseQuery{Text: aws.String("x")},
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	_, err = s.runtime.Retrieve(ctx, &agentruntime.RetrieveInput{
		KnowledgeBaseId: aws.String("short"), RetrievalQuery: &rttypes.KnowledgeBaseQuery{Text: aws.String("x")},
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)

	// GetDocumentContent hands out a URL this server serves.
	content, err := s.runtime.GetDocumentContent(ctx, &agentruntime.GetDocumentContentInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), DocumentId: aws.String("refunds"),
	})
	require.NoError(t, err)
	assert.Equal(t, "text/plain", aws.ToString(content.MimeType))

	resp, err := http.Get(aws.ToString(content.PresignedUrl)) //nolint:noctx // test against httptest.
	require.NoError(t, err)

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, faqDocs["refunds"], string(body))

	_, err = s.runtime.GetDocumentContent(ctx, &agentruntime.GetDocumentContentInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: aws.String(dsID), DocumentId: aws.String("missing"),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)
}

func TestSDKRetrieveAndGenerateFromKnowledgeBase(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	kbID, _ := s.newKnowledgeBase(t, "faq", faqDocs)
	modelARN := "arn:aws:bedrock:us-east-1::foundation-model/" + claudeModel

	out, err := s.runtime.RetrieveAndGenerate(ctx, &agentruntime.RetrieveAndGenerateInput{
		Input: &rttypes.RetrieveAndGenerateInput{Text: aws.String("how long do refunds take")},
		RetrieveAndGenerateConfiguration: &rttypes.RetrieveAndGenerateConfiguration{
			Type: rttypes.RetrieveAndGenerateTypeKnowledgeBase,
			KnowledgeBaseConfiguration: &rttypes.KnowledgeBaseRetrieveAndGenerateConfiguration{
				KnowledgeBaseId: aws.String(kbID), ModelArn: aws.String(modelARN),
			},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(out.Output.Text), faqDocs["refunds"])
	require.NotEmpty(t, out.Citations)
	assert.NotEmpty(t, out.Citations[0].RetrievedReferences)

	sessionID := out.SessionId

	again, err := s.runtime.RetrieveAndGenerate(ctx, &agentruntime.RetrieveAndGenerateInput{
		Input: &rttypes.RetrieveAndGenerateInput{Text: aws.String("and returns?")}, SessionId: sessionID,
		RetrieveAndGenerateConfiguration: out2Config(kbID, modelARN),
	})
	require.NoError(t, err)
	assert.Equal(t, aws.ToString(sessionID), aws.ToString(again.SessionId), "the session continues")

	ext, err := s.runtime.RetrieveAndGenerate(ctx, &agentruntime.RetrieveAndGenerateInput{
		Input: &rttypes.RetrieveAndGenerateInput{Text: aws.String("what colour is the sky")},
		RetrieveAndGenerateConfiguration: &rttypes.RetrieveAndGenerateConfiguration{
			Type: rttypes.RetrieveAndGenerateTypeExternalSources,
			ExternalSourcesConfiguration: &rttypes.ExternalSourcesRetrieveAndGenerateConfiguration{
				ModelArn: aws.String(modelARN),
				Sources: []rttypes.ExternalSource{{
					SourceType: rttypes.ExternalSourceTypeByteContent,
					ByteContent: &rttypes.ByteContentDoc{
						Identifier: aws.String("sky.txt"), ContentType: aws.String("text/plain"), Data: []byte("The sky is blue."),
					},
				}},
			},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, aws.ToString(ext.Output.Text), "The sky is blue.")

	_, err = s.runtime.RetrieveAndGenerate(ctx, &agentruntime.RetrieveAndGenerateInput{
		Input: &rttypes.RetrieveAndGenerateInput{Text: aws.String("x")}, RetrieveAndGenerateConfiguration: out2Config("MISSING000", modelARN),
	})
	requireAPIError(t, err, "ResourceNotFoundException", http.StatusNotFound)

	stream, err := s.runtime.RetrieveAndGenerateStream(ctx, &agentruntime.RetrieveAndGenerateStreamInput{
		Input: &rttypes.RetrieveAndGenerateInput{Text: aws.String("how long do refunds take")}, RetrieveAndGenerateConfiguration: out2Config(kbID, modelARN),
	})
	require.NoError(t, err)

	var text string

	var citations int

	es := stream.GetStream()
	for ev := range es.Events() {
		switch e := ev.(type) {
		case *rttypes.RetrieveAndGenerateStreamResponseOutputMemberOutput:
			text += aws.ToString(e.Value.Text)
		case *rttypes.RetrieveAndGenerateStreamResponseOutputMemberCitation:
			citations++
		}
	}

	requireStreamDone(t, es.Err())
	_ = es.Close()
	assert.Contains(t, text, faqDocs["refunds"])
	assert.Equal(t, 1, citations)
	assert.NotEmpty(t, aws.ToString(stream.SessionId))
}

func out2Config(kbID, modelARN string) *rttypes.RetrieveAndGenerateConfiguration {
	return &rttypes.RetrieveAndGenerateConfiguration{
		Type: rttypes.RetrieveAndGenerateTypeKnowledgeBase,
		KnowledgeBaseConfiguration: &rttypes.KnowledgeBaseRetrieveAndGenerateConfiguration{
			KnowledgeBaseId: aws.String(kbID), ModelArn: aws.String(modelARN),
		},
	}
}

func TestSDKRerankGenerateQueryOptimizeAgentic(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	src := func(text string) rttypes.RerankSource {
		return rttypes.RerankSource{Type: rttypes.RerankSourceTypeInline, InlineDocumentSource: &rttypes.RerankDocument{
			Type: rttypes.RerankDocumentTypeText, TextDocument: &rttypes.RerankTextDocument{Text: aws.String(text)},
		}}
	}

	ranked, err := s.runtime.Rerank(ctx, &agentruntime.RerankInput{
		Queries: []rttypes.RerankQuery{{Type: rttypes.RerankQueryContentTypeText, TextQuery: &rttypes.RerankTextDocument{Text: aws.String("refund policy")}}},
		Sources: []rttypes.RerankSource{src("shipping times"), src("our refund policy is generous")},
		RerankingConfiguration: &rttypes.RerankingConfiguration{
			Type: rttypes.RerankingConfigurationTypeBedrockRerankingModel,
			BedrockRerankingConfiguration: &rttypes.BedrockRerankingConfiguration{
				NumberOfResults:    aws.Int32(1),
				ModelConfiguration: &rttypes.BedrockRerankingModelConfiguration{ModelArn: aws.String("arn:aws:bedrock:us-west-2::foundation-model/amazon.rerank-v1:0")},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, ranked.Results, 1)
	assert.Equal(t, int32(1), aws.ToInt32(ranked.Results[0].Index), "the refund source ranks first")
	assert.InDelta(t, 1.0, aws.ToFloat32(ranked.Results[0].RelevanceScore), 0.001)

	kb, err := s.agent.CreateKnowledgeBase(ctx, &awsba.CreateKnowledgeBaseInput{
		Name: aws.String("sales"), RoleArn: aws.String(roleARN),
		KnowledgeBaseConfiguration: &batypes.KnowledgeBaseConfiguration{Type: batypes.KnowledgeBaseTypeSql},
	})
	require.NoError(t, err)

	q, err := s.runtime.GenerateQuery(ctx, &agentruntime.GenerateQueryInput{
		QueryGenerationInput: &rttypes.QueryGenerationInput{Type: rttypes.InputQueryTypeText, Text: aws.String("total sales in May")},
		TransformationConfiguration: &rttypes.TransformationConfiguration{
			Mode: rttypes.QueryTransformationModeTextToSql,
			TextToSqlConfiguration: &rttypes.TextToSqlConfiguration{
				Type:                       rttypes.TextToSqlConfigurationTypeKnowledgeBase,
				KnowledgeBaseConfiguration: &rttypes.TextToSqlKnowledgeBaseConfiguration{KnowledgeBaseArn: kb.KnowledgeBase.KnowledgeBaseArn},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, q.Queries, 1)
	assert.Equal(t, rttypes.GeneratedQueryTypeRedshiftSql, q.Queries[0].Type)
	assert.Contains(t, aws.ToString(q.Queries[0].Sql), "SELECT")

	op, err := s.runtime.OptimizePrompt(ctx, &agentruntime.OptimizePromptInput{
		Input:         &rttypes.InputPromptMemberTextPrompt{Value: rttypes.TextPrompt{Text: aws.String("summarize   this text")}},
		TargetModelId: aws.String(claudeModel),
	})
	require.NoError(t, err)

	var analysis, optimized bool

	ops := op.GetStream()
	for ev := range ops.Events() {
		switch ev.(type) {
		case *rttypes.OptimizedPromptStreamMemberAnalyzePromptEvent:
			analysis = true
		case *rttypes.OptimizedPromptStreamMemberOptimizedPromptEvent:
			optimized = true
		}
	}

	requireStreamDone(t, ops.Err())
	_ = ops.Close()
	assert.True(t, analysis && optimized)

	faqID, _ := s.newKnowledgeBase(t, "faq", faqDocs)

	ag, err := s.runtime.AgenticRetrieveStream(ctx, &agentruntime.AgenticRetrieveStreamInput{
		Messages: []rttypes.AgenticRetrieveMessage{{
			Role: rttypes.ConversationRoleUser, Content: &rttypes.AgenticRetrieveMessageContent{Text: aws.String("refunds")},
		}},
		Retrievers: []rttypes.AgenticRetriever{{Configuration: &rttypes.RetrieverConfigurationMemberKnowledgeBase{
			Value: rttypes.KnowledgeBaseRetrieverConfiguration{KnowledgeBaseId: aws.String(faqID)},
		}}},
		GenerateResponse: aws.Bool(true),
		// Required: the SDK's own validation of a missing configuration blocks
		// the call instead of returning (it never reaches its response reader).
		AgenticRetrieveConfiguration: &rttypes.AgenticRetrieveConfiguration{
			FoundationModelType: rttypes.FoundationModelTypeManaged,
		},
	})
	require.NoError(t, err)

	var results int

	var answer string

	as := ag.GetStream()
	for ev := range as.Events() {
		switch e := ev.(type) {
		case *rttypes.AgenticRetrieveStreamResponseOutputMemberResult:
			results = len(e.Value.Results)
		case *rttypes.AgenticRetrieveStreamResponseOutputMemberResponseEvent:
			answer = aws.ToString(e.Value.Text)
		}
	}

	requireStreamDone(t, as.Err())
	_ = as.Close()
	assert.Equal(t, 3, results)
	assert.Contains(t, answer, faqDocs["refunds"])
}

// TestSDKS3KnowledgeBaseFilteredRetrieve is the standard knowledge-base flow:
// documents and their .metadata.json sidecars in S3, an S3 data source, an
// ingestion job, then a Retrieve filtered on the sidecar attributes.
func TestSDKS3KnowledgeBaseFilteredRetrieve(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	require.NoError(t, s.provider.S3.CreateBucket(ctx, "kb-docs"))

	for key, body := range map[string]string{
		"refunds.txt":               "Refunds are processed within 5 business days.",
		"refunds.txt.metadata.json": `{"metadataAttributes":{"team":"billing","year":2024}}`,
		"returns.txt":               "Returns are accepted for 30 days.",
		"returns.txt.metadata.json": `{"metadataAttributes":{"team":"support","year":2023}}`,
	} {
		require.NoError(t, s.provider.S3.PutObject(ctx, "kb-docs", key, []byte(body), "", nil))
	}

	kbID, _ := s.newKnowledgeBase(t, "s3kb", nil)

	ds, err := s.agent.CreateDataSource(ctx, &awsba.CreateDataSourceInput{
		KnowledgeBaseId: aws.String(kbID), Name: aws.String("bucket"),
		DataSourceConfiguration: &batypes.DataSourceConfiguration{
			Type:            batypes.DataSourceTypeS3,
			S3Configuration: &batypes.S3DataSourceConfiguration{BucketArn: aws.String("arn:aws:s3:::kb-docs")},
		},
	})
	require.NoError(t, err)

	job, err := s.agent.StartIngestionJob(ctx, &awsba.StartIngestionJobInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: ds.DataSource.DataSourceId,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), job.IngestionJob.Statistics.NumberOfNewDocumentsIndexed)
	assert.Equal(t, int64(2), job.IngestionJob.Statistics.NumberOfMetadataDocumentsScanned)

	out, err := s.runtime.Retrieve(ctx, &agentruntime.RetrieveInput{
		KnowledgeBaseId: aws.String(kbID), RetrievalQuery: &rttypes.KnowledgeBaseQuery{Text: aws.String("how many days")},
		RetrievalConfiguration: &rttypes.KnowledgeBaseRetrievalConfiguration{
			VectorSearchConfiguration: &rttypes.KnowledgeBaseVectorSearchConfiguration{
				Filter: &rttypes.RetrievalFilterMemberAndAll{Value: []rttypes.RetrievalFilter{
					&rttypes.RetrievalFilterMemberEquals{Value: rttypes.FilterAttribute{Key: aws.String("team"), Value: document.NewLazyDocument("billing")}},
					&rttypes.RetrievalFilterMemberGreaterThan{Value: rttypes.FilterAttribute{Key: aws.String("year"), Value: document.NewLazyDocument(2023)}},
				}},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, out.RetrievalResults, 1)

	r := out.RetrievalResults[0]
	assert.Equal(t, "s3://kb-docs/refunds.txt", aws.ToString(r.Location.S3Location.Uri))

	var team string
	require.NoError(t, r.Metadata["team"].UnmarshalSmithyDocument(&team))
	assert.Equal(t, "billing", team)

	var year float64
	require.NoError(t, r.Metadata["year"].UnmarshalSmithyDocument(&year))
	assert.InDelta(t, 2024, year, 0)

	var uri string
	require.NoError(t, r.Metadata["x-amz-bedrock-kb-source-uri"].UnmarshalSmithyDocument(&uri))
	assert.Equal(t, "s3://kb-docs/refunds.txt", uri)

	content, err := s.runtime.GetDocumentContent(ctx, &agentruntime.GetDocumentContentInput{
		KnowledgeBaseId: aws.String(kbID), DataSourceId: ds.DataSource.DataSourceId, DocumentId: aws.String(uri),
	})
	require.NoError(t, err)

	resp, err := http.Get(aws.ToString(content.PresignedUrl)) //nolint:noctx // test download of the presigned URL.
	require.NoError(t, err)

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, "Refunds are processed within 5 business days.", string(body), "an S3 document id stays one path segment")

	_, err = s.runtime.Retrieve(ctx, &agentruntime.RetrieveInput{
		KnowledgeBaseId: aws.String(kbID), RetrievalQuery: &rttypes.KnowledgeBaseQuery{Text: aws.String("x")},
		RetrievalConfiguration: &rttypes.KnowledgeBaseRetrievalConfiguration{
			VectorSearchConfiguration: &rttypes.KnowledgeBaseVectorSearchConfiguration{
				Filter: &rttypes.RetrievalFilterMemberOrAll{Value: []rttypes.RetrievalFilter{
					&rttypes.RetrievalFilterMemberEquals{Value: rttypes.FilterAttribute{Key: aws.String("team"), Value: document.NewLazyDocument("x")}},
				}},
			},
		},
	})
	requireAPIError(t, err, "ValidationException", http.StatusBadRequest)
}
