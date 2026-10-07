package kendra_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskendra "github.com/aws/aws-sdk-go-v2/service/kendra"
	kendratypes "github.com/aws/aws-sdk-go-v2/service/kendra/types"

	"github.com/stackshy/cloudemu/v2"
	cfg "github.com/stackshy/cloudemu/v2/config"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

var ctx = context.Background()

func clientFor(t *testing.T, opts ...cfg.Option) *awskendra.Client {
	t.Helper()

	cloud := cloudemu.NewAWS(opts...)
	srv := awsserver.New(awsserver.Drivers{Kendra: cloud.Kendra})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	c, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awskendra.NewFromConfig(c, func(o *awskendra.Options) { o.BaseEndpoint = aws.String(ts.URL) })
}

func newIndex(t *testing.T, c *awskendra.Client) *string {
	t.Helper()

	out, err := c.CreateIndex(ctx, &awskendra.CreateIndexInput{
		Name: aws.String("docs"), Edition: kendratypes.IndexEditionDeveloperEdition, RoleArn: aws.String(roleArn),
	})
	if err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}

	return out.Id
}

func requireNoErr(t *testing.T, op string, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
}

func TestSDKDocumentsQueryRetrieveAndStatistics(t *testing.T) {
	c := clientFor(t)
	idx := newIndex(t, c)

	put, err := c.BatchPutDocument(ctx, &awskendra.BatchPutDocumentInput{IndexId: idx, Documents: []kendratypes.Document{
		{
			Id: aws.String("vacation"), Title: aws.String("Vacation policy"), ContentType: kendratypes.ContentTypePlainText,
			Blob: []byte("Employees accrue vacation days every month."),
			Attributes: []kendratypes.DocumentAttribute{
				{Key: aws.String("dept"), Value: &kendratypes.DocumentAttributeValue{StringValue: aws.String("hr")}},
				{Key: aws.String("year"), Value: &kendratypes.DocumentAttributeValue{LongValue: aws.Int64(2024)}},
				{Key: aws.String("_created_at"), Value: &kendratypes.DocumentAttributeValue{DateValue: aws.Time(time.Unix(1_700_000_000, 0))}},
			},
		},
		{Id: aws.String("parking"), Title: aws.String("Parking"), ContentType: kendratypes.ContentTypePlainText, Blob: []byte("Lot B is open.")},
	}})
	requireNoErr(t, "BatchPutDocument", err)

	if len(put.FailedDocuments) != 0 {
		t.Fatalf("unexpected failures: %+v", put.FailedDocuments)
	}

	st, err := c.BatchGetDocumentStatus(ctx, &awskendra.BatchGetDocumentStatusInput{IndexId: idx, DocumentInfoList: []kendratypes.DocumentInfo{
		{DocumentId: aws.String("vacation")}, {DocumentId: aws.String("ghost")},
	}})
	requireNoErr(t, "BatchGetDocumentStatus", err)

	if st.DocumentStatusList[0].DocumentStatus != kendratypes.DocumentStatusIndexed ||
		st.DocumentStatusList[1].DocumentStatus != kendratypes.DocumentStatusNotFound {
		t.Fatalf("statuses: %+v", st.DocumentStatusList)
	}

	q, err := c.Query(ctx, &awskendra.QueryInput{
		IndexId: idx, QueryText: aws.String("vacation days"),
		AttributeFilter: &kendratypes.AttributeFilter{EqualsTo: &kendratypes.DocumentAttribute{
			Key: aws.String("dept"), Value: &kendratypes.DocumentAttributeValue{StringValue: aws.String("hr")},
		}},
		Facets: []kendratypes.Facet{{DocumentAttributeKey: aws.String("dept")}},
	})
	requireNoErr(t, "Query", err)

	if aws.ToInt32(q.TotalNumberOfResults) != 1 || len(q.ResultItems) != 1 {
		t.Fatalf("expected 1 result, got total=%d items=%d", aws.ToInt32(q.TotalNumberOfResults), len(q.ResultItems))
	}

	item := q.ResultItems[0]
	if aws.ToString(item.DocumentId) != "vacation" || item.Type != kendratypes.QueryResultTypeDocument ||
		item.ScoreAttributes == nil || item.ScoreAttributes.ScoreConfidence != kendratypes.ScoreConfidenceVeryHigh ||
		len(item.DocumentExcerpt.Highlights) == 0 || aws.ToString(q.QueryId) == "" {
		t.Fatalf("unexpected result item: %+v", item)
	}

	if len(q.FacetResults) != 1 || aws.ToString(q.FacetResults[0].DocumentAttributeKey) != "dept" ||
		aws.ToInt32(q.FacetResults[0].DocumentAttributeValueCountPairs[0].Count) != 1 {
		t.Fatalf("unexpected facets: %+v", q.FacetResults)
	}

	var created *time.Time

	for _, a := range item.DocumentAttributes {
		if aws.ToString(a.Key) == "_created_at" {
			created = a.Value.DateValue
		}
	}

	if created == nil || created.Unix() != 1_700_000_000 {
		t.Fatalf("date attribute did not round-trip: %v", created)
	}

	r, err := c.Retrieve(ctx, &awskendra.RetrieveInput{IndexId: idx, QueryText: aws.String("vacation")})
	requireNoErr(t, "Retrieve", err)

	if len(r.ResultItems) != 1 || aws.ToString(r.ResultItems[0].Content) == "" || aws.ToString(r.ResultItems[0].DocumentTitle) != "Vacation policy" {
		t.Fatalf("unexpected retrieve result: %+v", r.ResultItems)
	}

	desc, err := c.DescribeIndex(ctx, &awskendra.DescribeIndexInput{Id: idx})
	requireNoErr(t, "DescribeIndex", err)

	if desc.IndexStatistics == nil || desc.IndexStatistics.TextDocumentStatistics == nil ||
		desc.IndexStatistics.TextDocumentStatistics.IndexedTextDocumentsCount != 2 {
		t.Fatalf("unexpected statistics: %+v", desc.IndexStatistics)
	}

	_, err = c.BatchDeleteDocument(ctx, &awskendra.BatchDeleteDocumentInput{IndexId: idx, DocumentIdList: []string{"vacation"}})
	requireNoErr(t, "BatchDeleteDocument", err)

	q, err = c.Query(ctx, &awskendra.QueryInput{IndexId: idx, QueryText: aws.String("vacation")})
	requireNoErr(t, "Query after delete", err)

	if aws.ToInt32(q.TotalNumberOfResults) != 0 {
		t.Fatalf("deleted document still found")
	}

	_, err = c.BatchPutDocument(ctx, &awskendra.BatchPutDocumentInput{IndexId: idx, Documents: []kendratypes.Document{}})

	var ve *kendratypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("empty Documents: expected ValidationException, got %T %v", err, err)
	}
}

func TestSDKTypedErrorsForMalformedAndMissingIds(t *testing.T) {
	c := clientFor(t)
	idx := newIndex(t, c)

	_, err := c.DescribeIndex(ctx, &awskendra.DescribeIndexInput{Id: aws.String("short")})

	var ve *kendratypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("malformed id: expected ValidationException, got %T %v", err, err)
	}

	_, err = c.DescribeFaq(ctx, &awskendra.DescribeFaqInput{IndexId: idx, Id: aws.String("00000000-0000-4000-8000-000000000000")})

	var nfe *kendratypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("unknown faq: expected ResourceNotFoundException, got %T %v", err, err)
	}

	_, err = c.ListIndices(ctx, &awskendra.ListIndicesInput{NextToken: aws.String("1")})
	if !errors.As(err, &ve) {
		t.Fatalf("bad token: expected ValidationException, got %T %v", err, err)
	}
}

func TestSDKAsyncConflictAndResourceInUse(t *testing.T) {
	clk := cfg.NewFakeClock(time.Unix(1_700_000_000, 0))
	c := clientFor(t, cfg.WithClock(clk), cfg.WithAsyncSettle())
	idx := newIndex(t, c)

	desc, err := c.DescribeIndex(ctx, &awskendra.DescribeIndexInput{Id: idx})
	requireNoErr(t, "DescribeIndex", err)

	if desc.Status != kendratypes.IndexStatusCreating {
		t.Fatalf("expected CREATING under async settling, got %s", desc.Status)
	}

	_, err = c.DeleteIndex(ctx, &awskendra.DeleteIndexInput{Id: idx})

	var ce *kendratypes.ConflictException
	if !errors.As(err, &ce) {
		t.Fatalf("delete while creating: expected ConflictException, got %T %v", err, err)
	}

	clk.Advance(time.Minute)

	ds, err := c.CreateDataSource(ctx, &awskendra.CreateDataSourceInput{IndexId: idx, Name: aws.String("ds"), Type: kendratypes.DataSourceTypeCustom})
	requireNoErr(t, "CreateDataSource", err)
	clk.Advance(time.Minute)

	_, err = c.StartDataSourceSyncJob(ctx, &awskendra.StartDataSourceSyncJobInput{IndexId: idx, Id: ds.Id})
	requireNoErr(t, "StartDataSourceSyncJob", err)

	_, err = c.StartDataSourceSyncJob(ctx, &awskendra.StartDataSourceSyncJobInput{IndexId: idx, Id: ds.Id})

	var inUse *kendratypes.ResourceInUseException
	if !errors.As(err, &inUse) {
		t.Fatalf("second start: expected ResourceInUseException, got %T %v", err, err)
	}

	jobs, err := c.ListDataSourceSyncJobs(ctx, &awskendra.ListDataSourceSyncJobsInput{IndexId: idx, Id: ds.Id})
	requireNoErr(t, "ListDataSourceSyncJobs", err)

	if len(jobs.History) != 1 || jobs.History[0].Status != kendratypes.DataSourceSyncJobStatusSyncing ||
		aws.ToString(jobs.History[0].Metrics.DocumentsScanned) != "0" {
		t.Fatalf("unexpected history: %+v", jobs.History)
	}

	_, err = c.StopDataSourceSyncJob(ctx, &awskendra.StopDataSourceSyncJobInput{IndexId: idx, Id: ds.Id})
	requireNoErr(t, "StopDataSourceSyncJob", err)

	jobs, _ = c.ListDataSourceSyncJobs(ctx, &awskendra.ListDataSourceSyncJobsInput{
		IndexId: idx, Id: ds.Id, StatusFilter: kendratypes.DataSourceSyncJobStatusAborted,
	})

	if len(jobs.History) != 1 {
		t.Fatalf("expected the aborted job, got %+v", jobs.History)
	}
}

func TestSDKFaqThesaurusBlockListLifecycle(t *testing.T) {
	c := clientFor(t)
	idx := newIndex(t, c)
	s3 := &kendratypes.S3Path{Bucket: aws.String("my-bucket"), Key: aws.String("file.csv")}

	faq, err := c.CreateFaq(ctx, &awskendra.CreateFaqInput{
		IndexId: idx, Name: aws.String("faq"), RoleArn: aws.String(roleArn), S3Path: s3,
		Tags: []kendratypes.Tag{{Key: aws.String("env"), Value: aws.String("test")}},
	})
	requireNoErr(t, "CreateFaq", err)

	df, err := c.DescribeFaq(ctx, &awskendra.DescribeFaqInput{IndexId: idx, Id: faq.Id})
	requireNoErr(t, "DescribeFaq", err)

	if df.Status != kendratypes.FaqStatusActive || df.FileFormat != kendratypes.FaqFileFormatCsv || aws.ToString(df.S3Path.Bucket) != "my-bucket" {
		t.Fatalf("unexpected faq: %+v", df)
	}

	lf, err := c.ListFaqs(ctx, &awskendra.ListFaqsInput{IndexId: idx})
	requireNoErr(t, "ListFaqs", err)

	if len(lf.FaqSummaryItems) != 1 {
		t.Fatalf("expected 1 faq, got %d", len(lf.FaqSummaryItems))
	}

	arn := "arn:aws:kendra:us-east-1:000000000000:index/" + aws.ToString(idx) + "/faq/" + aws.ToString(faq.Id)

	tags, err := c.ListTagsForResource(ctx, &awskendra.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
	requireNoErr(t, "ListTagsForResource", err)

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" {
		t.Fatalf("unexpected tags: %+v", tags.Tags)
	}

	_, err = c.DeleteFaq(ctx, &awskendra.DeleteFaqInput{IndexId: idx, Id: faq.Id})
	requireNoErr(t, "DeleteFaq", err)

	th, err := c.CreateThesaurus(ctx, &awskendra.CreateThesaurusInput{IndexId: idx, Name: aws.String("syn"), RoleArn: aws.String(roleArn), SourceS3Path: s3})
	requireNoErr(t, "CreateThesaurus", err)

	_, err = c.UpdateThesaurus(ctx, &awskendra.UpdateThesaurusInput{IndexId: idx, Id: th.Id, Description: aws.String("synonyms")})
	requireNoErr(t, "UpdateThesaurus", err)

	dt, err := c.DescribeThesaurus(ctx, &awskendra.DescribeThesaurusInput{IndexId: idx, Id: th.Id})
	requireNoErr(t, "DescribeThesaurus", err)

	if aws.ToString(dt.Description) != "synonyms" || aws.ToString(dt.Name) != "syn" || dt.Status != kendratypes.ThesaurusStatusActive {
		t.Fatalf("unexpected thesaurus: %+v", dt)
	}

	lt, _ := c.ListThesauri(ctx, &awskendra.ListThesauriInput{IndexId: idx})
	if len(lt.ThesaurusSummaryItems) != 1 {
		t.Fatalf("expected 1 thesaurus")
	}

	_, err = c.DeleteThesaurus(ctx, &awskendra.DeleteThesaurusInput{IndexId: idx, Id: th.Id})
	requireNoErr(t, "DeleteThesaurus", err)

	bl, err := c.CreateQuerySuggestionsBlockList(ctx, &awskendra.CreateQuerySuggestionsBlockListInput{
		IndexId: idx, Name: aws.String("blk"), RoleArn: aws.String(roleArn), SourceS3Path: s3,
	})
	requireNoErr(t, "CreateQuerySuggestionsBlockList", err)

	db, err := c.DescribeQuerySuggestionsBlockList(ctx, &awskendra.DescribeQuerySuggestionsBlockListInput{IndexId: idx, Id: bl.Id})
	requireNoErr(t, "DescribeQuerySuggestionsBlockList", err)

	if db.Status != kendratypes.QuerySuggestionsBlockListStatusActive || aws.ToString(db.Name) != "blk" {
		t.Fatalf("unexpected block list: %+v", db)
	}

	_, err = c.UpdateQuerySuggestionsBlockList(ctx, &awskendra.UpdateQuerySuggestionsBlockListInput{IndexId: idx, Id: bl.Id, Name: aws.String("blk2")})
	requireNoErr(t, "UpdateQuerySuggestionsBlockList", err)

	lb, _ := c.ListQuerySuggestionsBlockLists(ctx, &awskendra.ListQuerySuggestionsBlockListsInput{IndexId: idx})
	if len(lb.BlockListSummaryItems) != 1 || aws.ToString(lb.BlockListSummaryItems[0].Name) != "blk2" {
		t.Fatalf("unexpected block list summaries: %+v", lb.BlockListSummaryItems)
	}

	_, err = c.DeleteQuerySuggestionsBlockList(ctx, &awskendra.DeleteQuerySuggestionsBlockListInput{IndexId: idx, Id: bl.Id})
	requireNoErr(t, "DeleteQuerySuggestionsBlockList", err)
}

func TestSDKExperienceAccessControlFeaturedAndMappings(t *testing.T) {
	c := clientFor(t)
	idx := newIndex(t, c)

	exp, err := c.CreateExperience(ctx, &awskendra.CreateExperienceInput{IndexId: idx, Name: aws.String("exp"), RoleArn: aws.String(roleArn)})
	requireNoErr(t, "CreateExperience", err)

	de, err := c.DescribeExperience(ctx, &awskendra.DescribeExperienceInput{IndexId: idx, Id: exp.Id})
	requireNoErr(t, "DescribeExperience", err)

	if de.Status != kendratypes.ExperienceStatusActive || len(de.Endpoints) != 1 || de.Endpoints[0].EndpointType != kendratypes.EndpointTypeHome {
		t.Fatalf("unexpected experience: %+v", de)
	}

	_, err = c.UpdateExperience(ctx, &awskendra.UpdateExperienceInput{IndexId: idx, Id: exp.Id, Description: aws.String("d")})
	requireNoErr(t, "UpdateExperience", err)

	le, _ := c.ListExperiences(ctx, &awskendra.ListExperiencesInput{IndexId: idx})
	if len(le.SummaryItems) != 1 {
		t.Fatalf("expected 1 experience")
	}

	_, err = c.DeleteExperience(ctx, &awskendra.DeleteExperienceInput{IndexId: idx, Id: exp.Id})
	requireNoErr(t, "DeleteExperience", err)

	acl, err := c.CreateAccessControlConfiguration(ctx, &awskendra.CreateAccessControlConfigurationInput{
		IndexId: idx, Name: aws.String("acl"),
		AccessControlList: []kendratypes.Principal{{Name: aws.String("alice"), Type: kendratypes.PrincipalTypeUser, Access: kendratypes.ReadAccessTypeAllow}},
	})
	requireNoErr(t, "CreateAccessControlConfiguration", err)

	da, err := c.DescribeAccessControlConfiguration(ctx, &awskendra.DescribeAccessControlConfigurationInput{IndexId: idx, Id: acl.Id})
	requireNoErr(t, "DescribeAccessControlConfiguration", err)

	if aws.ToString(da.Name) != "acl" || len(da.AccessControlList) != 1 || aws.ToString(da.AccessControlList[0].Name) != "alice" {
		t.Fatalf("unexpected acl: %+v", da)
	}

	_, err = c.UpdateAccessControlConfiguration(ctx, &awskendra.UpdateAccessControlConfigurationInput{IndexId: idx, Id: acl.Id, Description: aws.String("d")})
	requireNoErr(t, "UpdateAccessControlConfiguration", err)

	la, _ := c.ListAccessControlConfigurations(ctx, &awskendra.ListAccessControlConfigurationsInput{IndexId: idx})
	if len(la.AccessControlConfigurations) != 1 {
		t.Fatalf("expected 1 acl")
	}

	_, err = c.DeleteAccessControlConfiguration(ctx, &awskendra.DeleteAccessControlConfigurationInput{IndexId: idx, Id: acl.Id})
	requireNoErr(t, "DeleteAccessControlConfiguration", err)

	checkFeatured(t, c, idx)
	checkPrincipalMappings(t, c, idx)
}

func checkFeatured(t *testing.T, c *awskendra.Client, idx *string) {
	t.Helper()

	_, err := c.BatchPutDocument(ctx, &awskendra.BatchPutDocumentInput{IndexId: idx, Documents: []kendratypes.Document{
		{Id: aws.String("home"), Title: aws.String("Home"), ContentType: kendratypes.ContentTypePlainText, Blob: []byte("welcome")},
	}})
	requireNoErr(t, "BatchPutDocument", err)

	fr, err := c.CreateFeaturedResultsSet(ctx, &awskendra.CreateFeaturedResultsSetInput{
		IndexId: idx, FeaturedResultsSetName: aws.String("Holiday"), QueryTexts: []string{"holiday"},
		FeaturedDocuments: []kendratypes.FeaturedDocument{{Id: aws.String("home")}, {Id: aws.String("gone")}},
	})
	requireNoErr(t, "CreateFeaturedResultsSet", err)

	id := fr.FeaturedResultsSet.FeaturedResultsSetId

	d, err := c.DescribeFeaturedResultsSet(ctx, &awskendra.DescribeFeaturedResultsSetInput{IndexId: idx, FeaturedResultsSetId: id})
	requireNoErr(t, "DescribeFeaturedResultsSet", err)

	if len(d.FeaturedDocumentsWithMetadata) != 1 || aws.ToString(d.FeaturedDocumentsWithMetadata[0].Title) != "Home" ||
		len(d.FeaturedDocumentsMissing) != 1 || d.Status != kendratypes.FeaturedResultsSetStatusActive {
		t.Fatalf("unexpected featured set: %+v", d)
	}

	_, err = c.CreateFeaturedResultsSet(ctx, &awskendra.CreateFeaturedResultsSetInput{
		IndexId: idx, FeaturedResultsSetName: aws.String("Other"), QueryTexts: []string{"holiday"},
	})

	var conflict *kendratypes.FeaturedResultsConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate query: expected FeaturedResultsConflictException, got %T %v", err, err)
	}

	up, err := c.UpdateFeaturedResultsSet(ctx, &awskendra.UpdateFeaturedResultsSetInput{
		IndexId: idx, FeaturedResultsSetId: id, Status: kendratypes.FeaturedResultsSetStatusInactive,
	})
	requireNoErr(t, "UpdateFeaturedResultsSet", err)

	if up.FeaturedResultsSet.Status != kendratypes.FeaturedResultsSetStatusInactive {
		t.Fatalf("status not updated: %+v", up.FeaturedResultsSet)
	}

	l, _ := c.ListFeaturedResultsSets(ctx, &awskendra.ListFeaturedResultsSetsInput{IndexId: idx})
	if len(l.FeaturedResultsSetSummaryItems) != 1 {
		t.Fatalf("expected 1 featured set")
	}

	del, err := c.BatchDeleteFeaturedResultsSet(ctx, &awskendra.BatchDeleteFeaturedResultsSetInput{
		IndexId: idx, FeaturedResultsSetIds: []string{aws.ToString(id), "nosuch"},
	})
	requireNoErr(t, "BatchDeleteFeaturedResultsSet", err)

	if len(del.Errors) != 1 || aws.ToString(del.Errors[0].Id) != "nosuch" {
		t.Fatalf("unexpected batch delete errors: %+v", del.Errors)
	}
}

func checkPrincipalMappings(t *testing.T, c *awskendra.Client, idx *string) {
	t.Helper()

	members := &kendratypes.GroupMembers{MemberUsers: []kendratypes.MemberUser{{UserId: aws.String("alice")}}}

	_, err := c.PutPrincipalMapping(ctx, &awskendra.PutPrincipalMappingInput{
		IndexId: idx, GroupId: aws.String("eng"), GroupMembers: members, OrderingId: aws.Int64(100),
	})
	requireNoErr(t, "PutPrincipalMapping", err)

	d, err := c.DescribePrincipalMapping(ctx, &awskendra.DescribePrincipalMappingInput{IndexId: idx, GroupId: aws.String("eng")})
	requireNoErr(t, "DescribePrincipalMapping", err)

	if len(d.GroupOrderingIdSummaries) != 1 || d.GroupOrderingIdSummaries[0].Status != kendratypes.PrincipalMappingStatusSucceeded {
		t.Fatalf("unexpected summaries: %+v", d.GroupOrderingIdSummaries)
	}

	g, err := c.ListGroupsOlderThanOrderingId(ctx, &awskendra.ListGroupsOlderThanOrderingIdInput{IndexId: idx, OrderingId: aws.Int64(200)})
	requireNoErr(t, "ListGroupsOlderThanOrderingId", err)

	if len(g.GroupsSummaries) != 1 || aws.ToString(g.GroupsSummaries[0].GroupId) != "eng" {
		t.Fatalf("unexpected groups: %+v", g.GroupsSummaries)
	}

	_, err = c.DeletePrincipalMapping(ctx, &awskendra.DeletePrincipalMappingInput{IndexId: idx, GroupId: aws.String("eng"), OrderingId: aws.Int64(300)})
	requireNoErr(t, "DeletePrincipalMapping", err)
}

func TestSDKQuerySuggestions(t *testing.T) {
	c := clientFor(t)
	idx := newIndex(t, c)

	d, err := c.DescribeQuerySuggestionsConfig(ctx, &awskendra.DescribeQuerySuggestionsConfigInput{IndexId: idx})
	requireNoErr(t, "DescribeQuerySuggestionsConfig", err)

	if d.Mode != kendratypes.ModeEnabled || d.Status != kendratypes.QuerySuggestionsStatusActive || aws.ToInt32(d.QueryLogLookBackWindowInDays) != 180 {
		t.Fatalf("unexpected defaults: %+v", d)
	}

	_, err = c.UpdateQuerySuggestionsConfig(ctx, &awskendra.UpdateQuerySuggestionsConfigInput{
		IndexId: idx, MinimumQueryCount: aws.Int32(1), MinimumNumberOfQueryingUsers: aws.Int32(1),
	})
	requireNoErr(t, "UpdateQuerySuggestionsConfig", err)

	_, err = c.BatchPutDocument(ctx, &awskendra.BatchPutDocumentInput{IndexId: idx, Documents: []kendratypes.Document{
		{Id: aws.String("d"), Title: aws.String("Vacation policy"), ContentType: kendratypes.ContentTypePlainText, Blob: []byte("vacation policy details")},
	}})
	requireNoErr(t, "BatchPutDocument", err)

	_, err = c.Query(ctx, &awskendra.QueryInput{IndexId: idx, QueryText: aws.String("vacation policy")})
	requireNoErr(t, "Query", err)

	s, err := c.GetQuerySuggestions(ctx, &awskendra.GetQuerySuggestionsInput{IndexId: idx, QueryText: aws.String("vac")})
	requireNoErr(t, "GetQuerySuggestions", err)

	if len(s.Suggestions) != 1 || aws.ToString(s.Suggestions[0].Value.Text.Text) != "vacation policy" {
		t.Fatalf("unexpected suggestions: %+v", s.Suggestions)
	}

	_, err = c.ClearQuerySuggestions(ctx, &awskendra.ClearQuerySuggestionsInput{IndexId: idx})
	requireNoErr(t, "ClearQuerySuggestions", err)

	s, _ = c.GetQuerySuggestions(ctx, &awskendra.GetQuerySuggestionsInput{IndexId: idx, QueryText: aws.String("vac")})
	if len(s.Suggestions) != 0 {
		t.Fatalf("suggestions survived a clear: %+v", s.Suggestions)
	}
}
