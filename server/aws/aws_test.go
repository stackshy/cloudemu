package aws_test

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stackshy/cloudemu"
	awsserver "github.com/stackshy/cloudemu/server/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T) (string, aws.Config) {
	t.Helper()

	provider := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{
		S3:       provider.S3,
		DynamoDB: provider.DynamoDB,
		EC2:      provider.EC2,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)

	return ts.URL, cfg
}

func newS3Client(t *testing.T) *s3.Client {
	t.Helper()

	url, cfg := newTestServer(t)

	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(url)
		o.UsePathStyle = true
	})
}

func newDDBClient(t *testing.T) *dynamodb.Client {
	t.Helper()

	url, cfg := newTestServer(t)

	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(url)
	})
}

func TestS3CreateAndListBuckets(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("my-bucket"),
	})
	require.NoError(t, err)

	result, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.NoError(t, err)

	var found bool

	for _, b := range result.Buckets {
		if aws.ToString(b.Name) == "my-bucket" {
			found = true
		}
	}

	assert.True(t, found, "bucket 'my-bucket' not found in list")
}

func TestS3PutAndGetObject(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("data-bucket"),
	})
	require.NoError(t, err)

	body := []byte("hello cloudemu")

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String("data-bucket"),
		Key:         aws.String("greeting.txt"),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("text/plain"),
	})
	require.NoError(t, err)

	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("data-bucket"),
		Key:    aws.String("greeting.txt"),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	got, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, "text/plain", aws.ToString(result.ContentType))
}

func TestS3HeadObject(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("head-bucket"),
	})
	require.NoError(t, err)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("head-bucket"),
		Key:    aws.String("file.bin"),
		Body:   bytes.NewReader([]byte("data")),
	})
	require.NoError(t, err)

	result, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String("head-bucket"),
		Key:    aws.String("file.bin"),
	})
	require.NoError(t, err)
	assert.NotNil(t, result.ContentLength)
}

func TestS3DeleteObject(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("del-bucket"),
	})
	require.NoError(t, err)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("del-bucket"),
		Key:    aws.String("to-delete.txt"),
		Body:   bytes.NewReader([]byte("bye")),
	})
	require.NoError(t, err)

	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String("del-bucket"),
		Key:    aws.String("to-delete.txt"),
	})
	require.NoError(t, err)

	_, err = client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("del-bucket"),
		Key:    aws.String("to-delete.txt"),
	})
	require.Error(t, err)
}

func TestS3ListObjectsWithPrefix(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("list-bucket"),
	})
	require.NoError(t, err)

	for _, key := range []string{"a/1.txt", "a/2.txt", "b/1.txt"} {
		_, err = client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String("list-bucket"),
			Key:    aws.String(key),
			Body:   bytes.NewReader([]byte("x")),
		})
		require.NoError(t, err)
	}

	result, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String("list-bucket"),
		Prefix: aws.String("a/"),
	})
	require.NoError(t, err)
	assert.Len(t, result.Contents, 2)
}

func TestS3ListObjectsWithDelimiter(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("delim-bucket"),
	})
	require.NoError(t, err)

	for _, key := range []string{"photos/2024/a.jpg", "photos/2025/b.jpg", "docs/c.txt"} {
		_, err = client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String("delim-bucket"),
			Key:    aws.String(key),
			Body:   bytes.NewReader([]byte("x")),
		})
		require.NoError(t, err)
	}

	result, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    aws.String("delim-bucket"),
		Delimiter: aws.String("/"),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result.CommonPrefixes, "expected common prefixes with delimiter")
}

func TestS3DeleteBucket(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("temp-bucket"),
	})
	require.NoError(t, err)

	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{
		Bucket: aws.String("temp-bucket"),
	})
	require.NoError(t, err)

	result, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.NoError(t, err)

	for _, b := range result.Buckets {
		assert.NotEqual(t, "temp-bucket", aws.ToString(b.Name))
	}
}

func TestS3CopyObject(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("src-bucket"),
	})
	require.NoError(t, err)

	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("dst-bucket"),
	})
	require.NoError(t, err)

	body := []byte("copy me")

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("src-bucket"),
		Key:    aws.String("original.txt"),
		Body:   bytes.NewReader(body),
	})
	require.NoError(t, err)

	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String("dst-bucket"),
		Key:        aws.String("copied.txt"),
		CopySource: aws.String("src-bucket/original.txt"),
	})
	require.NoError(t, err)

	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("dst-bucket"),
		Key:    aws.String("copied.txt"),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	got, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestS3GetObjectNotFound(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("nf-bucket"),
	})
	require.NoError(t, err)

	_, err = client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("nf-bucket"),
		Key:    aws.String("nonexistent"),
	})
	require.Error(t, err)
}

func TestS3EmptyBody(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("empty-bucket"),
	})
	require.NoError(t, err)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("empty-bucket"),
		Key:    aws.String("empty.txt"),
		Body:   bytes.NewReader([]byte{}),
	})
	require.NoError(t, err)

	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("empty-bucket"),
		Key:    aws.String("empty.txt"),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	got, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestS3SpecialCharactersInKey(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("special-bucket"),
	})
	require.NoError(t, err)

	key := "path/to/file with spaces.txt"

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("special-bucket"),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte("data")),
	})
	require.NoError(t, err)

	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("special-bucket"),
		Key:    aws.String(key),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	got, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("data"), got)
}

func TestS3DuplicateBucketReturnsError(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("dup-bucket"),
	})
	require.NoError(t, err)

	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String("dup-bucket"),
	})
	require.Error(t, err)
}

func TestDDBCreateTableAndListTables(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("users"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	result, err := client.ListTables(ctx, &dynamodb.ListTablesInput{})
	require.NoError(t, err)
	assert.Contains(t, result.TableNames, "users")
}

func TestDDBPutAndGetItem(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("items"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("items"),
		Item: map[string]ddbtypes.AttributeValue{
			"id":   &ddbtypes.AttributeValueMemberS{Value: "item-1"},
			"name": &ddbtypes.AttributeValueMemberS{Value: "Widget"},
		},
	})
	require.NoError(t, err)

	result, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("items"),
		Key: map[string]ddbtypes.AttributeValue{
			"id": &ddbtypes.AttributeValueMemberS{Value: "item-1"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Item)

	nameAttr, ok := result.Item["name"]
	require.True(t, ok)

	nameVal, ok := nameAttr.(*ddbtypes.AttributeValueMemberS)
	require.True(t, ok)
	assert.Equal(t, "Widget", nameVal.Value)
}

func TestDDBDeleteItem(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("del-table"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("del-table"),
		Item: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "to-delete"},
		},
	})
	require.NoError(t, err)

	_, err = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String("del-table"),
		Key: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "to-delete"},
		},
	})
	require.NoError(t, err)

	result, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("del-table"),
		Key: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "to-delete"},
		},
	})
	require.NoError(t, err)
	assert.Nil(t, result.Item)
}

func TestDDBDescribeTable(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("desc-table"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("sk"), KeyType: ddbtypes.KeyTypeRange},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("sk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	result, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String("desc-table"),
	})
	require.NoError(t, err)
	assert.Equal(t, "desc-table", aws.ToString(result.Table.TableName))
}

func TestDDBDescribeTableNotFound(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String("nonexistent-table"),
	})
	require.Error(t, err)
}

func TestDDBDuplicateTableReturnsError(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	input := &dynamodb.CreateTableInput{
		TableName: aws.String("dup-table"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	}

	_, err := client.CreateTable(ctx, input)
	require.NoError(t, err)

	_, err = client.CreateTable(ctx, input)
	require.Error(t, err)
}

// S3: overwrite existing object and verify new data.
func TestS3OverwriteObject(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("ow-bucket")})
	require.NoError(t, err)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("ow-bucket"), Key: aws.String("file.txt"),
		Body: bytes.NewReader([]byte("version1")),
	})
	require.NoError(t, err)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("ow-bucket"), Key: aws.String("file.txt"),
		Body: bytes.NewReader([]byte("version2")),
	})
	require.NoError(t, err)

	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("ow-bucket"), Key: aws.String("file.txt"),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	got, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("version2"), got)
}

// S3: list empty bucket returns zero objects, not error.
func TestS3ListObjectsEmptyBucket(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("empty-list")})
	require.NoError(t, err)

	result, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String("empty-list"),
	})
	require.NoError(t, err)
	assert.Empty(t, result.Contents)
	assert.Equal(t, int32(0), aws.ToInt32(result.KeyCount))
}

// S3: copy object within same bucket.
func TestS3CopyObjectSameBucket(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("same-cp")})
	require.NoError(t, err)

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("same-cp"), Key: aws.String("a.txt"),
		Body: bytes.NewReader([]byte("hello")),
	})
	require.NoError(t, err)

	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String("same-cp"),
		Key:        aws.String("b.txt"),
		CopySource: aws.String("same-cp/a.txt"),
	})
	require.NoError(t, err)

	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("same-cp"), Key: aws.String("b.txt"),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	got, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), got)
}

// S3: head object not found returns error.
func TestS3HeadObjectNotFound(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("headnf")})
	require.NoError(t, err)

	_, err = client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String("headnf"), Key: aws.String("nope"),
	})
	require.Error(t, err)
}

// S3: multiple buckets listed correctly.
func TestS3MultipleBuckets(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	names := []string{"alpha", "beta", "gamma"}
	for _, name := range names {
		_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(name)})
		require.NoError(t, err)
	}

	result, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(result.Buckets), len(names))
}

// S3: list with prefix and delimiter together (folder simulation).
func TestS3ListPrefixAndDelimiter(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("pd-bucket")})
	require.NoError(t, err)

	keys := []string{"logs/2024/jan.log", "logs/2024/feb.log", "logs/2025/mar.log", "logs/readme.txt"}
	for _, k := range keys {
		_, err = client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String("pd-bucket"), Key: aws.String(k),
			Body: bytes.NewReader([]byte("x")),
		})
		require.NoError(t, err)
	}

	result, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    aws.String("pd-bucket"),
		Prefix:    aws.String("logs/"),
		Delimiter: aws.String("/"),
	})
	require.NoError(t, err)

	// Should have common prefixes for "logs/2024/" and "logs/2025/"
	// and one direct object "logs/readme.txt"
	assert.NotEmpty(t, result.CommonPrefixes)
}

// S3: large binary object round-trip.
func TestS3LargeBinaryObject(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("big-bucket")})
	require.NoError(t, err)

	// 1 MB of random-ish data.
	const size = 1 << 20
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256) //nolint:mnd // fill pattern
	}

	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String("big-bucket"),
		Key:         aws.String("big.bin"),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/octet-stream"),
	})
	require.NoError(t, err)

	result, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String("big-bucket"), Key: aws.String("big.bin"),
	})
	require.NoError(t, err)
	defer result.Body.Close()

	got, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	assert.Equal(t, size, len(got))
	assert.Equal(t, data, got)
}

// DynamoDB: put item with multiple attribute types (S, N, BOOL).
func TestDDBMultipleAttributeTypes(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("typed-table"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("typed-table"),
		Item: map[string]ddbtypes.AttributeValue{
			"pk":     &ddbtypes.AttributeValueMemberS{Value: "user-1"},
			"name":   &ddbtypes.AttributeValueMemberS{Value: "Alice"},
			"age":    &ddbtypes.AttributeValueMemberN{Value: "30"},
			"active": &ddbtypes.AttributeValueMemberBOOL{Value: true},
		},
	})
	require.NoError(t, err)

	result, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("typed-table"),
		Key: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "user-1"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Item)

	// Verify string attribute.
	nameVal, ok := result.Item["name"].(*ddbtypes.AttributeValueMemberS)
	require.True(t, ok)
	assert.Equal(t, "Alice", nameVal.Value)

	// Verify numeric attribute.
	ageVal, ok := result.Item["age"].(*ddbtypes.AttributeValueMemberN)
	require.True(t, ok)
	assert.Equal(t, "30", ageVal.Value)

	// Verify boolean attribute.
	activeVal, ok := result.Item["active"].(*ddbtypes.AttributeValueMemberBOOL)
	require.True(t, ok)
	assert.True(t, activeVal.Value)
}

// DynamoDB: overwrite existing item.
func TestDDBOverwriteItem(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("ow-table"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("ow-table"),
		Item: map[string]ddbtypes.AttributeValue{
			"pk":   &ddbtypes.AttributeValueMemberS{Value: "k1"},
			"data": &ddbtypes.AttributeValueMemberS{Value: "old"},
		},
	})
	require.NoError(t, err)

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("ow-table"),
		Item: map[string]ddbtypes.AttributeValue{
			"pk":   &ddbtypes.AttributeValueMemberS{Value: "k1"},
			"data": &ddbtypes.AttributeValueMemberS{Value: "new"},
		},
	})
	require.NoError(t, err)

	result, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("ow-table"),
		Key: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "k1"},
		},
	})
	require.NoError(t, err)

	dataVal, ok := result.Item["data"].(*ddbtypes.AttributeValueMemberS)
	require.True(t, ok)
	assert.Equal(t, "new", dataVal.Value)
}

// DynamoDB: composite key (partition + sort).
func TestDDBCompositeKey(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("composite"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("sk"), KeyType: ddbtypes.KeyTypeRange},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("sk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("composite"),
		Item: map[string]ddbtypes.AttributeValue{
			"pk":    &ddbtypes.AttributeValueMemberS{Value: "user-1"},
			"sk":    &ddbtypes.AttributeValueMemberS{Value: "profile"},
			"email": &ddbtypes.AttributeValueMemberS{Value: "alice@example.com"},
		},
	})
	require.NoError(t, err)

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("composite"),
		Item: map[string]ddbtypes.AttributeValue{
			"pk":   &ddbtypes.AttributeValueMemberS{Value: "user-1"},
			"sk":   &ddbtypes.AttributeValueMemberS{Value: "settings"},
			"lang": &ddbtypes.AttributeValueMemberS{Value: "en"},
		},
	})
	require.NoError(t, err)

	// Get specific item by composite key.
	result, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("composite"),
		Key: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "user-1"},
			"sk": &ddbtypes.AttributeValueMemberS{Value: "profile"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Item)

	emailVal, ok := result.Item["email"].(*ddbtypes.AttributeValueMemberS)
	require.True(t, ok)
	assert.Equal(t, "alice@example.com", emailVal.Value)

	// Get different sort key.
	result, err = client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("composite"),
		Key: map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "user-1"},
			"sk": &ddbtypes.AttributeValueMemberS{Value: "settings"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Item)

	langVal, ok := result.Item["lang"].(*ddbtypes.AttributeValueMemberS)
	require.True(t, ok)
	assert.Equal(t, "en", langVal.Value)
}

// DynamoDB: list tables when empty.
func TestDDBListTablesEmpty(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	result, err := client.ListTables(ctx, &dynamodb.ListTablesInput{})
	require.NoError(t, err)
	assert.Empty(t, result.TableNames)
}

// DynamoDB: delete table then verify it's gone.
func TestDDBDeleteTable(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("to-delete"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = client.DeleteTable(ctx, &dynamodb.DeleteTableInput{
		TableName: aws.String("to-delete"),
	})
	require.NoError(t, err)

	result, err := client.ListTables(ctx, &dynamodb.ListTablesInput{})
	require.NoError(t, err)
	assert.NotContains(t, result.TableNames, "to-delete")
}

// DynamoDB: query with key condition expression.
func TestDDBQueryWithExpression(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("orders"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("userId"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("orderId"), KeyType: ddbtypes.KeyTypeRange},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("userId"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("orderId"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	for _, oid := range []string{"order-1", "order-2", "order-3"} {
		_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String("orders"),
			Item: map[string]ddbtypes.AttributeValue{
				"userId":  &ddbtypes.AttributeValueMemberS{Value: "user-A"},
				"orderId": &ddbtypes.AttributeValueMemberS{Value: oid},
				"total":   &ddbtypes.AttributeValueMemberN{Value: "100"},
			},
		})
		require.NoError(t, err)
	}

	// Also add an order for a different user.
	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("orders"),
		Item: map[string]ddbtypes.AttributeValue{
			"userId":  &ddbtypes.AttributeValueMemberS{Value: "user-B"},
			"orderId": &ddbtypes.AttributeValueMemberS{Value: "order-99"},
		},
	})
	require.NoError(t, err)

	result, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("orders"),
		KeyConditionExpression: aws.String("userId = :uid"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":uid": &ddbtypes.AttributeValueMemberS{Value: "user-A"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(3), result.Count)
	assert.Len(t, result.Items, 3)
}

// S3: full lifecycle — create, put, list, get, head, copy, delete object, delete bucket.
func TestS3FullLifecycle(t *testing.T) {
	client := newS3Client(t)
	ctx := context.Background()

	bucket := "lifecycle-test"

	// Create.
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)

	// Put.
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("doc.pdf"),
		Body: bytes.NewReader([]byte("pdf-content")), ContentType: aws.String("application/pdf"),
	})
	require.NoError(t, err)

	// List.
	listResult, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	assert.Len(t, listResult.Contents, 1)
	assert.Equal(t, "doc.pdf", aws.ToString(listResult.Contents[0].Key))

	// Get.
	getResult, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("doc.pdf"),
	})
	require.NoError(t, err)

	data, _ := io.ReadAll(getResult.Body)
	getResult.Body.Close()
	assert.Equal(t, []byte("pdf-content"), data)
	assert.Equal(t, "application/pdf", aws.ToString(getResult.ContentType))

	// Head.
	headResult, err := client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("doc.pdf"),
	})
	require.NoError(t, err)
	assert.NotNil(t, headResult.ETag)

	// Copy.
	_, err = client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("doc-backup.pdf"),
		CopySource: aws.String(bucket + "/doc.pdf"),
	})
	require.NoError(t, err)

	// Delete original.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("doc.pdf"),
	})
	require.NoError(t, err)

	// Verify copy survives.
	copyResult, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("doc-backup.pdf"),
	})
	require.NoError(t, err)

	copyData, _ := io.ReadAll(copyResult.Body)
	copyResult.Body.Close()
	assert.Equal(t, []byte("pdf-content"), copyData)

	// Delete copy and bucket.
	_, err = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket), Key: aws.String("doc-backup.pdf"),
	})
	require.NoError(t, err)

	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)

	// Verify bucket is gone.
	buckets, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	require.NoError(t, err)

	for _, b := range buckets.Buckets {
		assert.NotEqual(t, bucket, aws.ToString(b.Name))
	}
}

// DynamoDB: full lifecycle — create table, put items, query, delete items, delete table.
func TestDDBFullLifecycle(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	// Create table.
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("products"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("category"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("productId"), KeyType: ddbtypes.KeyTypeRange},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("category"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("productId"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	// Put items.
	items := []map[string]ddbtypes.AttributeValue{
		{
			"category":  &ddbtypes.AttributeValueMemberS{Value: "electronics"},
			"productId": &ddbtypes.AttributeValueMemberS{Value: "laptop-1"},
			"price":     &ddbtypes.AttributeValueMemberN{Value: "999"},
		},
		{
			"category":  &ddbtypes.AttributeValueMemberS{Value: "electronics"},
			"productId": &ddbtypes.AttributeValueMemberS{Value: "phone-1"},
			"price":     &ddbtypes.AttributeValueMemberN{Value: "699"},
		},
		{
			"category":  &ddbtypes.AttributeValueMemberS{Value: "books"},
			"productId": &ddbtypes.AttributeValueMemberS{Value: "go-book-1"},
			"price":     &ddbtypes.AttributeValueMemberN{Value: "49"},
		},
	}

	for _, item := range items {
		_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String("products"), Item: item,
		})
		require.NoError(t, err)
	}

	// Query electronics.
	qResult, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("products"),
		KeyConditionExpression: aws.String("category = :cat"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":cat": &ddbtypes.AttributeValueMemberS{Value: "electronics"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), qResult.Count)

	// Delete one item.
	_, err = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String("products"),
		Key: map[string]ddbtypes.AttributeValue{
			"category":  &ddbtypes.AttributeValueMemberS{Value: "electronics"},
			"productId": &ddbtypes.AttributeValueMemberS{Value: "phone-1"},
		},
	})
	require.NoError(t, err)

	// Query again — should be 1 now.
	qResult, err = client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("products"),
		KeyConditionExpression: aws.String("category = :cat"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":cat": &ddbtypes.AttributeValueMemberS{Value: "electronics"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), qResult.Count)

	// Describe table.
	desc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String("products"),
	})
	require.NoError(t, err)
	assert.Equal(t, "products", aws.ToString(desc.Table.TableName))

	// Delete table.
	_, err = client.DeleteTable(ctx, &dynamodb.DeleteTableInput{
		TableName: aws.String("products"),
	})
	require.NoError(t, err)

	// Verify gone.
	tables, err := client.ListTables(ctx, &dynamodb.ListTablesInput{})
	require.NoError(t, err)
	assert.NotContains(t, tables.TableNames, "products")
}

func TestDDBUpdateItem(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("counters"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("counters"),
		Item: map[string]ddbtypes.AttributeValue{
			"id":    &ddbtypes.AttributeValueMemberS{Value: "c1"},
			"count": &ddbtypes.AttributeValueMemberN{Value: "0"},
		},
	})
	require.NoError(t, err)

	_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String("counters"),
		Key: map[string]ddbtypes.AttributeValue{
			"id": &ddbtypes.AttributeValueMemberS{Value: "c1"},
		},
		UpdateExpression: aws.String("SET #c = :v"),
		ExpressionAttributeNames: map[string]string{
			"#c": "count",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":v": &ddbtypes.AttributeValueMemberN{Value: "42"},
		},
	})
	require.NoError(t, err)

	got, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("counters"),
		Key: map[string]ddbtypes.AttributeValue{
			"id": &ddbtypes.AttributeValueMemberS{Value: "c1"},
		},
	})
	require.NoError(t, err)
	count := got.Item["count"].(*ddbtypes.AttributeValueMemberN).Value
	assert.Equal(t, "42", count)
}

func TestDDBUpdateItemRemove(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("rm"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("rm"),
		Item: map[string]ddbtypes.AttributeValue{
			"id":   &ddbtypes.AttributeValueMemberS{Value: "k1"},
			"temp": &ddbtypes.AttributeValueMemberS{Value: "delete-me"},
			"keep": &ddbtypes.AttributeValueMemberS{Value: "stay"},
		},
	})
	require.NoError(t, err)

	_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String("rm"),
		Key: map[string]ddbtypes.AttributeValue{
			"id": &ddbtypes.AttributeValueMemberS{Value: "k1"},
		},
		UpdateExpression: aws.String("REMOVE temp"),
	})
	require.NoError(t, err)

	got, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("rm"),
		Key: map[string]ddbtypes.AttributeValue{
			"id": &ddbtypes.AttributeValueMemberS{Value: "k1"},
		},
	})
	require.NoError(t, err)
	_, hasTemp := got.Item["temp"]
	assert.False(t, hasTemp, "temp should be removed")
	assert.Contains(t, got.Item, "keep")
}

func TestDDBScan(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("products"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("sku"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("sku"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	for _, p := range []struct{ sku, cat string }{
		{"a", "electronics"}, {"b", "books"}, {"c", "electronics"},
	} {
		_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String("products"),
			Item: map[string]ddbtypes.AttributeValue{
				"sku":      &ddbtypes.AttributeValueMemberS{Value: p.sku},
				"category": &ddbtypes.AttributeValueMemberS{Value: p.cat},
			},
		})
		require.NoError(t, err)
	}

	out, err := client.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String("products"),
	})
	require.NoError(t, err)
	assert.Equal(t, int32(3), out.Count)

	filtered, err := client.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String("products"),
		FilterExpression: aws.String("category = :c"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":c": &ddbtypes.AttributeValueMemberS{Value: "electronics"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), filtered.Count)
}

func TestDDBBatchWriteItem(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("batchw"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
		RequestItems: map[string][]ddbtypes.WriteRequest{
			"batchw": {
				{PutRequest: &ddbtypes.PutRequest{Item: map[string]ddbtypes.AttributeValue{
					"id": &ddbtypes.AttributeValueMemberS{Value: "x"},
				}}},
				{PutRequest: &ddbtypes.PutRequest{Item: map[string]ddbtypes.AttributeValue{
					"id": &ddbtypes.AttributeValueMemberS{Value: "y"},
				}}},
				{PutRequest: &ddbtypes.PutRequest{Item: map[string]ddbtypes.AttributeValue{
					"id": &ddbtypes.AttributeValueMemberS{Value: "z"},
				}}},
			},
		},
	})
	require.NoError(t, err)

	// Delete one via batch.
	_, err = client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
		RequestItems: map[string][]ddbtypes.WriteRequest{
			"batchw": {
				{DeleteRequest: &ddbtypes.DeleteRequest{Key: map[string]ddbtypes.AttributeValue{
					"id": &ddbtypes.AttributeValueMemberS{Value: "y"},
				}}},
			},
		},
	})
	require.NoError(t, err)

	scan, err := client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("batchw")})
	require.NoError(t, err)
	assert.Equal(t, int32(2), scan.Count)
}

func TestDDBBatchGetItem(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("batchr"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	for _, id := range []string{"a", "b", "c"} {
		_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String("batchr"),
			Item: map[string]ddbtypes.AttributeValue{
				"id": &ddbtypes.AttributeValueMemberS{Value: id},
			},
		})
		require.NoError(t, err)
	}

	out, err := client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
		RequestItems: map[string]ddbtypes.KeysAndAttributes{
			"batchr": {
				Keys: []map[string]ddbtypes.AttributeValue{
					{"id": &ddbtypes.AttributeValueMemberS{Value: "a"}},
					{"id": &ddbtypes.AttributeValueMemberS{Value: "c"}},
				},
			},
		},
	})
	require.NoError(t, err)
	assert.Len(t, out.Responses["batchr"], 2)
}

func TestDDBTransactWriteItems(t *testing.T) {
	client := newDDBClient(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String("txn"),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	_, err = client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{
				TableName: aws.String("txn"),
				Item: map[string]ddbtypes.AttributeValue{
					"id": &ddbtypes.AttributeValueMemberS{Value: "t1"},
				},
			}},
			{Put: &ddbtypes.Put{
				TableName: aws.String("txn"),
				Item: map[string]ddbtypes.AttributeValue{
					"id": &ddbtypes.AttributeValueMemberS{Value: "t2"},
				},
			}},
		},
	})
	require.NoError(t, err)

	scan, err := client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("txn")})
	require.NoError(t, err)
	assert.Equal(t, int32(2), scan.Count)
}
