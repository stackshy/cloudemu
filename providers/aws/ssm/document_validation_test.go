package ssm_test

import (
	"context"
	"strings"
	"testing"

	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
)

func TestSendCommandAcceptsAWSOwnedNames(t *testing.T) {
	m := newMock()

	names := []string{
		"AWS-RunRemoteScript", "AWS-ApplyAnsiblePlaybooks", "AWS-ConfigureDocker",
		"AmazonCloudWatch-ManageAgent", "AWS-RunDocument", "AWSSupport-CollectEKSInstanceLogs",
		"arn:aws:ssm:us-east-1::document/AWS-RunInspecChecks",
	}

	for _, doc := range names {
		if _, err := m.SendCommand(context.Background(), ssmdriver.CommandConfig{
			InstanceIDs: []string{"i-0123"}, DocumentName: doc,
		}); err != nil {
			t.Errorf("SendCommand %s: %v", doc, err)
		}
	}

	_, err := m.SendCommand(context.Background(), ssmdriver.CommandConfig{
		InstanceIDs: []string{"i-0123"}, DocumentName: "my-missing-doc",
	})
	wantException(t, err, "InvalidDocument")

	for _, doc := range []string{"AWS-RunRemoteScript", "AmazonCloudWatch-ManageAgent"} {
		d, err := m.DescribeDocument(context.Background(), ssmdriver.DocumentRef{Name: doc})
		if err != nil || d.Owner != "Amazon" || d.DocumentType != "Command" {
			t.Errorf("catalog %s = %+v, %v", doc, d, err)
		}
	}
}

func TestDocumentRequestValidation(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	create := func(in ssmdriver.CreateDocumentInput) error {
		if in.Name == "" {
			in.Name = "validated"
		}

		if in.Content == "" {
			in.Content = shellDocJSON
		}

		_, err := m.CreateDocument(ctx, &in)

		return err
	}

	err := create(ssmdriver.CreateDocumentInput{DocumentType: "Bogus"})
	wantException(t, err, "ValidationException")

	if !strings.HasPrefix(err.Error(), "InvalidArgument: 1 validation error detected: Value 'Bogus' at 'documentType'") {
		t.Errorf("message = %q", err.Error())
	}

	wantException(t, create(ssmdriver.CreateDocumentInput{DocumentFormat: "XML"}), "ValidationException")
	wantException(t, create(ssmdriver.CreateDocumentInput{TargetType: "AWS::EC2::Instance"}), "ValidationException")
	wantException(t, create(ssmdriver.CreateDocumentInput{Content: shellDocYAML}), "InvalidDocumentContent")
	wantException(t, create(ssmdriver.CreateDocumentInput{Content: shellDocYAML, DocumentFormat: "JSON"}), "InvalidDocumentContent")

	if err := create(ssmdriver.CreateDocumentInput{TargetType: "/AWS::EC2::Instance"}); err != nil {
		t.Fatalf("valid create: %v", err)
	}

	_, err = m.UpdateDocument(ctx, &ssmdriver.UpdateDocumentInput{Name: "validated", Content: shellDocYAML, DocumentFormat: "XML"})
	wantException(t, err, "ValidationException")

	_, err = m.UpdateDocument(ctx, &ssmdriver.UpdateDocumentInput{Name: "validated", Content: shellDocYAML})
	wantException(t, err, "InvalidDocumentContent")

	_, err = m.GetDocument(ctx, ssmdriver.DocumentRef{Name: "validated"}, "XML")
	wantException(t, err, "ValidationException")

	for _, v := range []string{"$LATEST", "$DEFAULT", "0", ""} {
		_, err = m.UpdateDocumentDefaultVersion(ctx, "validated", v)
		wantException(t, err, "ValidationException")
	}

	// DocumentVersion (unlike DocumentVersionNumber) takes the selectors.
	for _, v := range []string{"$LATEST", "$DEFAULT", "1"} {
		if _, err := m.DescribeDocument(ctx, ssmdriver.DocumentRef{Name: "validated", Version: v}); err != nil {
			t.Errorf("DescribeDocument version %s: %v", v, err)
		}
	}
}
