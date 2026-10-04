package awsauthz

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopeARNs(t *testing.T) {
	s := Scope{AccountID: "123456789012", Region: "us-east-1", Partition: "aws"}

	assert.Equal(t, "arn:aws:sqs:us-east-1:123456789012:q1", s.ARN("sqs", "q1"))
	assert.Equal(t, "arn:aws:iam::123456789012:user/a", s.GlobalARN("iam", "user/a"))
}

func TestJSONBody(t *testing.T) {
	var v struct {
		TableName string `json:"TableName"`
	}

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"TableName":"t1"} trailing`))
	require.True(t, JSONBody(r, &v))
	assert.Equal(t, "t1", v.TableName)

	rest, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"TableName":"t1"} trailing`, string(rest), "the body is put back whole")

	assert.False(t, JSONBody(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{`)), &v))

	r = httptest.NewRequest(http.MethodPost, "/", nil)
	r.Body = nil
	assert.False(t, JSONBody(r, &v))
}
