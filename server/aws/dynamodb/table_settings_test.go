// table_settings_test.go — real aws-sdk-go-v2 journeys covering the
// DeletionProtectionEnabled and TableClass round-trip that IaC clients read
// back on every refresh, plus the UpdateTable mutation of both and the
// deletion-protection guard on DeleteTable.
package dynamodb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func settingsTable(name string) *dynamodb.CreateTableInput {
	return &dynamodb.CreateTableInput{
		TableName:   aws.String(name),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
	}
}

func TestTableSettingsCreateRoundTrip(t *testing.T) {
	t.Parallel()

	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	in := settingsTable("settings-rt")
	in.DeletionProtectionEnabled = aws.Bool(true)
	in.TableClass = ddbtypes.TableClassStandardInfrequentAccess

	_, err := client.CreateTable(ctx, in)
	require.NoError(t, err)

	desc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("settings-rt")})
	require.NoError(t, err)
	assert.True(t, aws.ToBool(desc.Table.DeletionProtectionEnabled))
	require.NotNil(t, desc.Table.TableClassSummary)
	assert.Equal(t, ddbtypes.TableClassStandardInfrequentAccess, desc.Table.TableClassSummary.TableClass)
}

func TestTableSettingsDefaults(t *testing.T) {
	t.Parallel()

	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, settingsTable("settings-default"))
	require.NoError(t, err)

	desc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("settings-default")})
	require.NoError(t, err)
	// Real DynamoDB reports these on every table, defaulted; an absent value
	// gives IaC a perpetual diff against a config that sets the default.
	assert.False(t, aws.ToBool(desc.Table.DeletionProtectionEnabled))
	require.NotNil(t, desc.Table.TableClassSummary)
	assert.Equal(t, ddbtypes.TableClassStandard, desc.Table.TableClassSummary.TableClass)
}

func TestTableSettingsUpdate(t *testing.T) {
	t.Parallel()

	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, settingsTable("settings-upd"))
	require.NoError(t, err)

	_, err = client.UpdateTable(ctx, &dynamodb.UpdateTableInput{
		TableName:                 aws.String("settings-upd"),
		TableClass:                ddbtypes.TableClassStandardInfrequentAccess,
		DeletionProtectionEnabled: aws.Bool(true),
	})
	require.NoError(t, err)

	desc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("settings-upd")})
	require.NoError(t, err)
	assert.True(t, aws.ToBool(desc.Table.DeletionProtectionEnabled))
	require.NotNil(t, desc.Table.TableClassSummary)
	assert.Equal(t, ddbtypes.TableClassStandardInfrequentAccess, desc.Table.TableClassSummary.TableClass)

	// An UpdateTable that touches only an unrelated field must not reset either
	// setting.
	_, err = client.UpdateTable(ctx, &dynamodb.UpdateTableInput{
		TableName:   aws.String("settings-upd"),
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	desc, err = client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("settings-upd")})
	require.NoError(t, err)
	assert.True(t, aws.ToBool(desc.Table.DeletionProtectionEnabled))
	assert.Equal(t, ddbtypes.TableClassStandardInfrequentAccess, desc.Table.TableClassSummary.TableClass)
}

func TestTableSettingsDeletionProtectionGuard(t *testing.T) {
	t.Parallel()

	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	in := settingsTable("settings-guard")
	in.DeletionProtectionEnabled = aws.Bool(true)
	_, err := client.CreateTable(ctx, in)
	require.NoError(t, err)

	_, err = client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String("settings-guard")})
	require.Error(t, err)

	var apiErr smithy.APIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, "ValidationException", apiErr.ErrorCode())

	// Disabling protection must unblock the delete.
	_, err = client.UpdateTable(ctx, &dynamodb.UpdateTableInput{
		TableName:                 aws.String("settings-guard"),
		DeletionProtectionEnabled: aws.Bool(false),
	})
	require.NoError(t, err)

	_, err = client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String("settings-guard")})
	require.NoError(t, err)
}
