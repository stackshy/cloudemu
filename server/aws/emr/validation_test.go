package emr_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/emr"
	emrtypes "github.com/aws/aws-sdk-go-v2/service/emr/types"
	"github.com/aws/smithy-go"

	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func requireEMRCode(t *testing.T, err error, want string) {
	t.Helper()

	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != want {
		t.Fatalf("err = %v, want code %s", err, want)
	}
}

// postRunJobFlow sends a raw RunJobFlow body, skipping the SDK's own required
// field checks.
func postRunJobFlow(t *testing.T, url, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("X-Amz-Target", "ElasticMapReduce.RunJobFlow")
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	var out struct {
		Type string `json:"__type"`
	}

	_ = json.NewDecoder(resp.Body).Decode(&out)

	return resp.StatusCode, out.Type
}

func TestRunJobFlowRequiresNameAndInstances(t *testing.T) {
	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{EMR: true, Region: "us-east-1"}))
	t.Cleanup(ts.Close)

	for _, body := range []string{
		`{}`,
		`{"Name":"c"}`,
		`{"Instances":{"InstanceCount":1}}`,
		`{"Name":"c","Instances":{"InstanceCount":-5,"MasterInstanceType":"m5.xlarge"}}`,
	} {
		status, code := postRunJobFlow(t, ts.URL, body)
		if status != http.StatusBadRequest || !strings.HasSuffix(code, "ValidationException") {
			t.Fatalf("body %s: status %d code %q, want 400 ValidationException", body, status, code)
		}
	}

	if status, _ := postRunJobFlow(t, ts.URL, `{"Name":"","Instances":{}}`); status != http.StatusOK {
		t.Fatalf("empty name with instances: status %d, want 200", status)
	}
}

func TestRunJobFlowRejectsBadReleaseLabelAndCounts(t *testing.T) {
	c := newEMRClient(t)
	ctx := context.Background()

	_, err := c.RunJobFlow(ctx, &emr.RunJobFlowInput{
		Name: aws.String("c"), ReleaseLabel: aws.String("not-a-real-label"),
		Instances: &emrtypes.JobFlowInstancesConfig{InstanceCount: aws.Int32(1), MasterInstanceType: aws.String("m5.xlarge")},
	})
	requireEMRCode(t, err, "ValidationException")

	_, err = c.RunJobFlow(ctx, &emr.RunJobFlowInput{
		Name: aws.String("c"),
		Instances: &emrtypes.JobFlowInstancesConfig{InstanceGroups: []emrtypes.InstanceGroupConfig{{
			InstanceRole: emrtypes.InstanceRoleTypeMaster, InstanceType: aws.String("m5.xlarge"), InstanceCount: aws.Int32(0),
		}}},
	})
	requireEMRCode(t, err, "ValidationException")

	id := runCluster(t, c)

	desc, err := c.ListInstanceGroups(ctx, &emr.ListInstanceGroupsInput{ClusterId: aws.String(id)})
	if err != nil || len(desc.InstanceGroups) == 0 {
		t.Fatalf("ListInstanceGroups: %v", err)
	}

	_, err = c.ModifyInstanceGroups(ctx, &emr.ModifyInstanceGroupsInput{
		InstanceGroups: []emrtypes.InstanceGroupModifyConfig{{
			InstanceGroupId: desc.InstanceGroups[0].Id, InstanceCount: aws.Int32(-1),
		}},
	})
	requireEMRCode(t, err, "ValidationException")

	_, err = c.AddInstanceGroups(ctx, &emr.AddInstanceGroupsInput{
		JobFlowId: aws.String(id),
		InstanceGroups: []emrtypes.InstanceGroupConfig{{
			InstanceRole: emrtypes.InstanceRoleTypeTask, InstanceType: aws.String("m5.large"), InstanceCount: aws.Int32(-2),
		}},
	})
	requireEMRCode(t, err, "ValidationException")
}
