package ssm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
)

const docJSON = `{"schemaVersion":"2.2","description":"hello","parameters":{"msg":{"type":"String","default":"hi"}},` +
	`"mainSteps":[{"action":"aws:runShellScript","name":"say","inputs":{"runCommand":["echo {{ msg }}"]}}]}`

const docYAML = "schemaVersion: '2.2'\ndescription: from yaml\nmainSteps:\n" +
	"  - action: aws:runShellScript\n    name: say\n    inputs:\n      runCommand:\n        - echo yaml\n"

func wantAPIError(t *testing.T, err error, code string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not an API error, want %s", err, code)
	}

	if apiErr.ErrorCode() != code {
		t.Fatalf("error code = %s (%s), want %s", apiErr.ErrorCode(), apiErr.ErrorMessage(), code)
	}
}

func TestDocumentsLifecycleOverSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := newRunCommandClient(t)

	created, err := c.CreateDocument(ctx, &awsssm.CreateDocumentInput{
		Name: aws.String("my-doc"), Content: aws.String(docJSON), VersionName: aws.String("first"),
		Tags: []ssmtypes.Tag{{Key: aws.String("team"), Value: aws.String("core")}},
	})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	d := created.DocumentDescription
	if aws.ToString(d.DocumentVersion) != "1" || d.DocumentType != ssmtypes.DocumentTypeCommand ||
		d.Status != ssmtypes.DocumentStatusActive || d.HashType != ssmtypes.DocumentHashTypeSha256 ||
		len(d.Parameters) != 1 || aws.ToString(d.Parameters[0].DefaultValue) != "hi" || len(d.Tags) != 1 {
		t.Fatalf("created = %+v", d)
	}

	_, err = c.CreateDocument(ctx, &awsssm.CreateDocumentInput{Name: aws.String("my-doc"), Content: aws.String(docJSON)})

	var exists *ssmtypes.DocumentAlreadyExists
	if !errors.As(err, &exists) {
		t.Fatalf("duplicate create: %v, want DocumentAlreadyExists", err)
	}

	up, err := c.UpdateDocument(ctx, &awsssm.UpdateDocumentInput{
		Name: aws.String("my-doc"), Content: aws.String(docYAML), DocumentFormat: ssmtypes.DocumentFormatYaml,
		DocumentVersion: aws.String("$LATEST"),
	})
	if err != nil || aws.ToString(up.DocumentDescription.DocumentVersion) != "2" ||
		aws.ToString(up.DocumentDescription.DefaultVersion) != "1" {
		t.Fatalf("UpdateDocument = %+v, %v", up, err)
	}

	_, err = c.UpdateDocument(ctx, &awsssm.UpdateDocumentInput{
		Name: aws.String("my-doc"), Content: aws.String(docYAML), DocumentFormat: ssmtypes.DocumentFormatYaml,
	})

	var dup *ssmtypes.DuplicateDocumentContent
	if !errors.As(err, &dup) {
		t.Fatalf("same content update: %v, want DuplicateDocumentContent", err)
	}

	def, err := c.UpdateDocumentDefaultVersion(ctx, &awsssm.UpdateDocumentDefaultVersionInput{
		Name: aws.String("my-doc"), DocumentVersion: aws.String("2"),
	})
	if err != nil || aws.ToString(def.Description.DefaultVersion) != "2" {
		t.Fatalf("UpdateDocumentDefaultVersion = %+v, %v", def, err)
	}

	got, err := c.GetDocument(ctx, &awsssm.GetDocumentInput{Name: aws.String("my-doc"), DocumentFormat: ssmtypes.DocumentFormatJson})
	if err != nil || got.DocumentFormat != ssmtypes.DocumentFormatJson || !strings.Contains(aws.ToString(got.Content), `"from yaml"`) {
		t.Fatalf("GetDocument JSON = %+v, %v", got, err)
	}

	v1, err := c.GetDocument(ctx, &awsssm.GetDocumentInput{Name: aws.String("my-doc"), VersionName: aws.String("first")})
	if err != nil || aws.ToString(v1.Content) != docJSON {
		t.Fatalf("GetDocument by VersionName = %+v, %v", v1, err)
	}

	_, err = c.DescribeDocument(ctx, &awsssm.DescribeDocumentInput{Name: aws.String("my-doc"), DocumentVersion: aws.String("5")})

	var badVersion *ssmtypes.InvalidDocumentVersion
	if !errors.As(err, &badVersion) {
		t.Fatalf("describe missing version: %v, want InvalidDocumentVersion", err)
	}

	versions, err := c.ListDocumentVersions(ctx, &awsssm.ListDocumentVersionsInput{Name: aws.String("my-doc")})
	if err != nil || len(versions.DocumentVersions) != 2 || !versions.DocumentVersions[1].IsDefaultVersion {
		t.Fatalf("ListDocumentVersions = %+v, %v", versions, err)
	}

	if _, err := c.DeleteDocument(ctx, &awsssm.DeleteDocumentInput{Name: aws.String("my-doc")}); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}

	_, err = c.DescribeDocument(ctx, &awsssm.DescribeDocumentInput{Name: aws.String("my-doc")})

	var missing *ssmtypes.InvalidDocument
	if !errors.As(err, &missing) {
		t.Fatalf("describe deleted: %v, want InvalidDocument", err)
	}
}

func TestListDocumentsFiltersAndPagingOverSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := newRunCommandClient(t)

	for _, name := range []string{"app-one", "app-two", "db-one"} {
		if _, err := c.CreateDocument(ctx, &awsssm.CreateDocumentInput{
			Name: aws.String(name), Content: aws.String(docJSON),
		}); err != nil {
			t.Fatalf("CreateDocument %s: %v", name, err)
		}
	}

	page1, err := c.ListDocuments(ctx, &awsssm.ListDocumentsInput{
		Filters:    []ssmtypes.DocumentKeyValuesFilter{{Key: aws.String("Owner"), Values: []string{"Self"}}},
		MaxResults: aws.Int32(2),
	})
	if err != nil || len(page1.DocumentIdentifiers) != 2 || page1.NextToken == nil {
		t.Fatalf("page 1 = %+v, %v", page1, err)
	}

	page2, err := c.ListDocuments(ctx, &awsssm.ListDocumentsInput{
		Filters:   []ssmtypes.DocumentKeyValuesFilter{{Key: aws.String("Owner"), Values: []string{"Self"}}},
		NextToken: page1.NextToken,
	})
	if err != nil || len(page2.DocumentIdentifiers) != 1 || aws.ToString(page2.DocumentIdentifiers[0].Name) != "db-one" {
		t.Fatalf("page 2 = %+v, %v", page2, err)
	}

	legacy, err := c.ListDocuments(ctx, &awsssm.ListDocumentsInput{
		DocumentFilterList: []ssmtypes.DocumentFilter{{Key: ssmtypes.DocumentFilterKeyName, Value: aws.String("app-")}},
	})
	if err != nil || len(legacy.DocumentIdentifiers) != 2 {
		t.Fatalf("legacy filter = %+v, %v", legacy, err)
	}

	amazon, err := c.ListDocuments(ctx, &awsssm.ListDocumentsInput{
		Filters: []ssmtypes.DocumentKeyValuesFilter{{Key: aws.String("Name"), Values: []string{"AWS-RunShell"}}},
	})
	if err != nil || len(amazon.DocumentIdentifiers) != 1 || aws.ToString(amazon.DocumentIdentifiers[0].Owner) != "Amazon" {
		t.Fatalf("AWS-owned lookup = %+v, %v", amazon, err)
	}

	_, err = c.ListDocuments(ctx, &awsssm.ListDocumentsInput{
		Filters: []ssmtypes.DocumentKeyValuesFilter{{Key: aws.String("Bogus"), Values: []string{"x"}}},
	})
	wantAPIError(t, err, "InvalidFilterKey")
}

func TestDocumentPermissionsAndTagsOverSDK(t *testing.T) {
	ctx := context.Background()
	c, _ := newRunCommandClient(t)

	if _, err := c.CreateDocument(ctx, &awsssm.CreateDocumentInput{
		Name: aws.String("shared"), Content: aws.String(docJSON),
	}); err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	if _, err := c.ModifyDocumentPermission(ctx, &awsssm.ModifyDocumentPermissionInput{
		Name: aws.String("shared"), PermissionType: ssmtypes.DocumentPermissionTypeShare,
		AccountIdsToAdd: []string{"111122223333"},
	}); err != nil {
		t.Fatalf("ModifyDocumentPermission: %v", err)
	}

	perm, err := c.DescribeDocumentPermission(ctx, &awsssm.DescribeDocumentPermissionInput{
		Name: aws.String("shared"), PermissionType: ssmtypes.DocumentPermissionTypeShare,
	})
	if err != nil || len(perm.AccountIds) != 1 || aws.ToString(perm.AccountSharingInfoList[0].SharedDocumentVersion) != "$DEFAULT" {
		t.Fatalf("DescribeDocumentPermission = %+v, %v", perm, err)
	}

	_, err = c.DeleteDocument(ctx, &awsssm.DeleteDocumentInput{Name: aws.String("shared")})
	wantAPIError(t, err, "InvalidDocumentOperation")

	if _, err := c.AddTagsToResource(ctx, &awsssm.AddTagsToResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingDocument, ResourceId: aws.String("shared"),
		Tags: []ssmtypes.Tag{{Key: aws.String("env"), Value: aws.String("dev")}},
	}); err != nil {
		t.Fatalf("AddTagsToResource Document: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &awsssm.ListTagsForResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingDocument, ResourceId: aws.String("shared"),
	})
	if err != nil || len(tags.TagList) != 1 || aws.ToString(tags.TagList[0].Value) != "dev" {
		t.Fatalf("ListTagsForResource Document = %+v, %v", tags, err)
	}

	// A document and a parameter with the same id are separate resources.
	_, err = c.ListTagsForResource(ctx, &awsssm.ListTagsForResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingParameter, ResourceId: aws.String("shared"),
	})
	wantAPIError(t, err, "InvalidResourceId")

	_, err = c.ListTagsForResource(ctx, &awsssm.ListTagsForResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTagging("Bucket"), ResourceId: aws.String("shared"),
	})

	var badType *ssmtypes.InvalidResourceType
	if !errors.As(err, &badType) {
		t.Fatalf("unknown resource type: %v, want InvalidResourceType", err)
	}
}

func TestAWSOwnedDocumentsOverSDK(t *testing.T) {
	ctx := context.Background()
	c, ec2c := newRunCommandClient(t)

	d, err := c.DescribeDocument(ctx, &awsssm.DescribeDocumentInput{Name: aws.String("AWS-RunShellScript")})
	if err != nil || aws.ToString(d.Document.Owner) != "Amazon" || aws.ToString(d.Document.SchemaVersion) != "1.2" {
		t.Fatalf("describe AWS-RunShellScript = %+v, %v", d, err)
	}

	_, err = c.DeleteDocument(ctx, &awsssm.DeleteDocumentInput{Name: aws.String("AWS-RunShellScript")})

	var op *ssmtypes.InvalidDocumentOperation
	if !errors.As(err, &op) {
		t.Fatalf("delete AWS-owned: %v, want InvalidDocumentOperation", err)
	}

	ids := runInstances(t, ec2c, 1)

	_, err = c.SendCommand(ctx, &awsssm.SendCommandInput{InstanceIds: ids, DocumentName: aws.String("No-Such-Document")})

	var invalid *ssmtypes.InvalidDocument
	if !errors.As(err, &invalid) {
		t.Fatalf("SendCommand unknown document: %v, want InvalidDocument", err)
	}
}
