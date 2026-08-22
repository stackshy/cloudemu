package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestAWSDatabaseCompat drives a DynamoDB table + item lifecycle through the
// real aws-sdk-go-v2 client. Operation names match the portable "database"
// driver in docs/coverage/coverage.json (e.g. the BatchWriteItem SDK call maps
// to the "BatchPutItems" driver op, BatchGetItem to "BatchGetItems").
func TestAWSDatabaseCompat(t *testing.T) {
	provider := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{DynamoDB: provider.DynamoDB})
	client := sess.DynamoDBClient()
	ctx := context.Background()

	const (
		svc   = "database"
		table = "compat-items"
	)

	pk := func(v string) map[string]ddbtypes.AttributeValue {
		return map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: v}}
	}

	sess.Op(svc, "CreateTable", func() error {
		_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
			TableName:   aws.String(table),
			BillingMode: ddbtypes.BillingModePayPerRequest,
			KeySchema: []ddbtypes.KeySchemaElement{
				{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
			},
			AttributeDefinitions: []ddbtypes.AttributeDefinition{
				{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			},
		})

		return err
	})

	sess.Op(svc, "ListTables", func() error {
		out, err := client.ListTables(ctx, &dynamodb.ListTablesInput{})
		if err != nil {
			return err
		}

		for _, n := range out.TableNames {
			if n == table {
				return nil
			}
		}

		return fmt.Errorf("table %q not found in ListTables", table)
	})

	sess.Op(svc, "DescribeTable", func() error {
		_, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
		return err
	})

	sess.Op(svc, "PutItem", func() error {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(table),
			Item: map[string]ddbtypes.AttributeValue{
				"id":   &ddbtypes.AttributeValueMemberS{Value: "a"},
				"name": &ddbtypes.AttributeValueMemberS{Value: "Widget"},
			},
		})

		return err
	})

	sess.Op(svc, "GetItem", func() error {
		out, err := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), Key: pk("a")})
		if err != nil {
			return err
		}

		if out.Item["name"].(*ddbtypes.AttributeValueMemberS).Value != "Widget" {
			return fmt.Errorf("item round-trip mismatch")
		}

		return nil
	})

	sess.Op(svc, "UpdateItem", func() error {
		_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:                 aws.String(table),
			Key:                       pk("a"),
			UpdateExpression:          aws.String("SET #n = :v"),
			ExpressionAttributeNames:  map[string]string{"#n": "name"},
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":v": &ddbtypes.AttributeValueMemberS{Value: "Gadget"}},
		})

		return err
	})

	sess.Op(svc, "Query", func() error {
		_, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String(table),
			KeyConditionExpression:    aws.String("id = :id"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":id": &ddbtypes.AttributeValueMemberS{Value: "a"}},
		})

		return err
	})

	sess.Op(svc, "Scan", func() error {
		out, err := client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(table)})
		if err != nil {
			return err
		}

		if out.Count != 1 {
			return fmt.Errorf("expected 1 item, got %d", out.Count)
		}

		return nil
	})

	sess.Op(svc, "BatchPutItems", func() error {
		_, err := client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]ddbtypes.WriteRequest{
				table: {
					{PutRequest: &ddbtypes.PutRequest{Item: pk("b")}},
					{PutRequest: &ddbtypes.PutRequest{Item: pk("c")}},
				},
			},
		})

		return err
	})

	sess.Op(svc, "BatchGetItems", func() error {
		_, err := client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
			RequestItems: map[string]ddbtypes.KeysAndAttributes{
				table: {Keys: []map[string]ddbtypes.AttributeValue{pk("b"), pk("c")}},
			},
		})

		return err
	})

	sess.Op(svc, "TransactWriteItems", func() error {
		_, err := client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
			TransactItems: []ddbtypes.TransactWriteItem{
				{Put: &ddbtypes.Put{TableName: aws.String(table), Item: pk("t1")}},
			},
		})

		return err
	})

	sess.Op(svc, "DeleteItem", func() error {
		_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(table), Key: pk("a")})
		return err
	})

	sess.Op(svc, "DeleteTable", func() error {
		_, err := client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)})
		return err
	})
}
