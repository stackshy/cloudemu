package cloudformation_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

func TestMergeResources(t *testing.T) {
	t.Parallel()

	oldBody := "Resources:\n  A: {Type: T, Properties: {Name: a1}}\n  B: {Type: T, Properties: {Name: b1}}\n"
	newBody := `{"Resources":{"A":{"Type":"T","Properties":{"Name":"a2"}},` +
		`"B":{"Type":"T","Properties":{"Name":"taken"}},"C":{"Type":"T"}}}`

	merged, err := cfn.MergeResources(newBody, oldBody, map[string]bool{"B": true, "C": true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"Resources":{"A":{"Type":"T","Properties":{"Name":"a2"}},`+
		`"B":{"Type":"T","Properties":{"Name":"b1"}}}}`, merged)

	_, err = cfn.MergeResources("{", oldBody, nil)
	require.Error(t, err)
}
