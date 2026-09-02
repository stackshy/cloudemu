// dynamodb_backup_test.go — real-user-journey tests for DynamoDB on-demand
// backups and PITR restore, driving the genuine aws-sdk-go-v2 DynamoDB client
// against the emulator's HTTP server (httptest). Assertions are made on
// SDK-decoded responses and SDK-visible typed errors.
package dynamodb_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDDBBackupLifecycle: create a table with items, back it up, describe/list
// the backup, restore it into a new table with identical schema + items, then
// delete the backup and observe the typed 404.
func TestDDBBackupLifecycle(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "orders", "id", "")
	suiteDDBPut(t, client, "orders", map[string]ddbtypes.AttributeValue{
		"id": sAttr("o-1"), "total": nAttr("42"),
	})
	suiteDDBPut(t, client, "orders", map[string]ddbtypes.AttributeValue{
		"id": sAttr("o-2"), "total": nAttr("7"),
	})

	backup, err := client.CreateBackup(ctx, &dynamodb.CreateBackupInput{
		TableName:  aws.String("orders"),
		BackupName: aws.String("nightly"),
	})
	require.NoError(t, err)
	arn := aws.ToString(backup.BackupDetails.BackupArn)
	require.NotEmpty(t, arn)
	assert.Equal(t, ddbtypes.BackupStatusAvailable, backup.BackupDetails.BackupStatus)
	assert.Equal(t, "nightly", aws.ToString(backup.BackupDetails.BackupName))

	desc, err := client.DescribeBackup(ctx, &dynamodb.DescribeBackupInput{BackupArn: aws.String(arn)})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.BackupStatusAvailable, desc.BackupDescription.BackupDetails.BackupStatus)
	assert.Equal(t, "orders", aws.ToString(desc.BackupDescription.SourceTableDetails.TableName))
	assert.Equal(t, int64(2), aws.ToInt64(desc.BackupDescription.SourceTableDetails.ItemCount))

	list, err := client.ListBackups(ctx, &dynamodb.ListBackupsInput{TableName: aws.String("orders")})
	require.NoError(t, err)
	require.Len(t, list.BackupSummaries, 1)
	assert.Equal(t, arn, aws.ToString(list.BackupSummaries[0].BackupArn))

	_, err = client.RestoreTableFromBackup(ctx, &dynamodb.RestoreTableFromBackupInput{
		BackupArn:       aws.String(arn),
		TargetTableName: aws.String("orders-restored"),
	})
	require.NoError(t, err)

	rdesc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("orders-restored")})
	require.NoError(t, err)
	require.Len(t, rdesc.Table.KeySchema, 1)
	assert.Equal(t, "id", aws.ToString(rdesc.Table.KeySchema[0].AttributeName))

	got := suiteDDBGet(t, client, "orders-restored", map[string]ddbtypes.AttributeValue{"id": sAttr("o-1")})
	require.NotNil(t, got.Item)
	assert.Equal(t, "42", attrN(t, got.Item, "total"))

	// The restored table is independent: mutating the source doesn't touch it.
	suiteDDBPut(t, client, "orders", map[string]ddbtypes.AttributeValue{"id": sAttr("o-3")})
	absent := suiteDDBGet(t, client, "orders-restored", map[string]ddbtypes.AttributeValue{"id": sAttr("o-3")})
	assert.Nil(t, absent.Item)

	_, err = client.DeleteBackup(ctx, &dynamodb.DeleteBackupInput{BackupArn: aws.String(arn)})
	require.NoError(t, err)

	_, err = client.DescribeBackup(ctx, &dynamodb.DescribeBackupInput{BackupArn: aws.String(arn)})
	var bnf *ddbtypes.BackupNotFoundException
	require.ErrorAs(t, err, &bnf, "DescribeBackup on a deleted backup should be BackupNotFoundException")
}

// TestDDBCreateBackupMissingTable: backing up a table that does not exist is a
// typed TableNotFoundException.
func TestDDBCreateBackupMissingTable(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)

	_, err := client.CreateBackup(context.Background(), &dynamodb.CreateBackupInput{
		TableName:  aws.String("ghost"),
		BackupName: aws.String("bkp"),
	})

	var tnf *ddbtypes.TableNotFoundException
	require.ErrorAs(t, err, &tnf)
}

// TestDDBRestoreFromBackupTargetExists: restoring onto an existing table name is
// a typed TableAlreadyExistsException.
func TestDDBRestoreFromBackupTargetExists(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "src", "id", "")
	suiteDDBCreateTable(t, client, "existing", "id", "")

	backup, err := client.CreateBackup(ctx, &dynamodb.CreateBackupInput{
		TableName:  aws.String("src"),
		BackupName: aws.String("bkp"),
	})
	require.NoError(t, err)

	_, err = client.RestoreTableFromBackup(ctx, &dynamodb.RestoreTableFromBackupInput{
		BackupArn:       backup.BackupDetails.BackupArn,
		TargetTableName: aws.String("existing"),
	})

	var exists *ddbtypes.TableAlreadyExistsException
	require.ErrorAs(t, err, &exists)
}

// TestDDBRestoreFromMissingBackup: restoring an unknown BackupArn is a typed
// BackupNotFoundException.
func TestDDBRestoreFromMissingBackup(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)

	_, err := client.RestoreTableFromBackup(context.Background(), &dynamodb.RestoreTableFromBackupInput{
		BackupArn:       aws.String("arn:aws:dynamodb:us-east-1:000000000000:table/x/backup/nope"),
		TargetTableName: aws.String("t"),
	})

	var bnf *ddbtypes.BackupNotFoundException
	require.ErrorAs(t, err, &bnf)
}

// TestDDBPITRRestore: enabling continuous backups surfaces the restorable
// window, RestoreTableToPointInTime clones the current data into a new table,
// and restoring without PITR enabled is a typed PointInTimeRecoveryUnavailable
// error.
func TestDDBPITRRestore(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "ledger", "id", "")
	suiteDDBPut(t, client, "ledger", map[string]ddbtypes.AttributeValue{"id": sAttr("a"), "v": sAttr("1")})

	// PITR off: RestoreTableToPointInTime is rejected.
	_, err := client.RestoreTableToPointInTime(ctx, &dynamodb.RestoreTableToPointInTimeInput{
		SourceTableName:         aws.String("ledger"),
		TargetTableName:         aws.String("ledger-pit"),
		UseLatestRestorableTime: aws.Bool(true),
	})
	var unavailable *ddbtypes.PointInTimeRecoveryUnavailableException
	require.ErrorAs(t, err, &unavailable, "restore without PITR should be PointInTimeRecoveryUnavailableException")

	_, err = client.UpdateContinuousBackups(ctx, &dynamodb.UpdateContinuousBackupsInput{
		TableName: aws.String("ledger"),
		PointInTimeRecoverySpecification: &ddbtypes.PointInTimeRecoverySpecification{
			PointInTimeRecoveryEnabled: aws.Bool(true),
		},
	})
	require.NoError(t, err)

	cont, err := client.DescribeContinuousBackups(ctx, &dynamodb.DescribeContinuousBackupsInput{
		TableName: aws.String("ledger"),
	})
	require.NoError(t, err)
	pitr := cont.ContinuousBackupsDescription.PointInTimeRecoveryDescription
	assert.Equal(t, ddbtypes.PointInTimeRecoveryStatusEnabled, pitr.PointInTimeRecoveryStatus)
	require.NotNil(t, pitr.EarliestRestorableDateTime)
	require.NotNil(t, pitr.LatestRestorableDateTime)

	_, err = client.RestoreTableToPointInTime(ctx, &dynamodb.RestoreTableToPointInTimeInput{
		SourceTableName:         aws.String("ledger"),
		TargetTableName:         aws.String("ledger-pit"),
		UseLatestRestorableTime: aws.Bool(true),
	})
	require.NoError(t, err)

	got := suiteDDBGet(t, client, "ledger-pit", map[string]ddbtypes.AttributeValue{"id": sAttr("a")})
	require.NotNil(t, got.Item)
	assert.Equal(t, "1", attrS(t, got.Item, "v"))

	// Restoring onto an existing table name is rejected.
	_, err = client.RestoreTableToPointInTime(ctx, &dynamodb.RestoreTableToPointInTimeInput{
		SourceTableName:         aws.String("ledger"),
		TargetTableName:         aws.String("ledger-pit"),
		UseLatestRestorableTime: aws.Bool(true),
	})
	var exists *ddbtypes.TableAlreadyExistsException
	require.ErrorAs(t, err, &exists)
}
