package aws_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCloudWatchJSONDispatch checks that the full AWS server routes a
// GraniteServiceVersion20100801 awsJson1_0 request to CloudWatch, and that
// CloudWatch does not take the requests of other awsJson services.
func TestCloudWatchJSONDispatch(t *testing.T) {
	ts := fullAWSServer(t)

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{name: "cloudwatch", target: "GraniteServiceVersion20100801.DescribeAlarms", want: `"MetricAlarms":[]`},
		{name: "dynamodb", target: "DynamoDB_20120810.ListTables", want: `"TableNames"`},
		{name: "cloudwatch logs", target: "Logs_20140328.DescribeLogGroups", want: `"logGroups"`},
		{name: "kinesis", target: "Kinesis_20131202.ListStreams", want: `"StreamNames"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := doRequest(t, ts, http.MethodPost, "/", "{}", map[string]string{
				"Content-Type": "application/x-amz-json-1.0",
				"X-Amz-Target": tt.target,
			})

			assert.Equal(t, http.StatusOK, status, body)
			assert.True(t, strings.Contains(body, tt.want), "want %s in %s", tt.want, body)
		})
	}
}
