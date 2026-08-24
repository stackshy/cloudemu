// dynamodb_list_tables_pagination_test.go — real aws-sdk-go-v2 journeys
// asserting ListTables pagination matches DynamoDB: Limit caps the page,
// LastEvaluatedTableName is returned when more names remain, and the
// ExclusiveStartTableName cursor walks a stable (sorted) order to completion.
package dynamodb_test

import (
	"context"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDDBListTablesPaginationCursor(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	// Create in a deliberately non-sorted order to prove the response order does
	// not depend on creation/map-iteration order.
	created := []string{"t03", "t01", "t04", "t00", "t02"}
	for _, name := range created {
		suiteDDBCreateTable(t, client, name, "pk", "")
	}

	want := append([]string(nil), created...)
	sort.Strings(want)

	// First page: Limit=2 returns exactly two names and a cursor because more
	// tables remain.
	page1, err := client.ListTables(ctx, &dynamodb.ListTablesInput{
		Limit: aws.Int32(2),
	})
	require.NoError(t, err)
	assert.Equal(t, want[:2], page1.TableNames)
	require.NotNil(t, page1.LastEvaluatedTableName)
	assert.Equal(t, want[1], aws.ToString(page1.LastEvaluatedTableName))

	// Second page resumes strictly after the cursor (exclusive).
	page2, err := client.ListTables(ctx, &dynamodb.ListTablesInput{
		Limit:                   aws.Int32(2),
		ExclusiveStartTableName: page1.LastEvaluatedTableName,
	})
	require.NoError(t, err)
	assert.Equal(t, want[2:4], page2.TableNames)
	require.NotNil(t, page2.LastEvaluatedTableName)
	assert.Equal(t, want[3], aws.ToString(page2.LastEvaluatedTableName))

	// Final page: the last name, and no cursor because nothing remains.
	page3, err := client.ListTables(ctx, &dynamodb.ListTablesInput{
		Limit:                   aws.Int32(2),
		ExclusiveStartTableName: page2.LastEvaluatedTableName,
	})
	require.NoError(t, err)
	assert.Equal(t, want[4:], page3.TableNames)
	assert.Nil(t, page3.LastEvaluatedTableName)
}

func TestDDBListTablesPaginatorWalksAll(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	want := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for _, name := range want {
		suiteDDBCreateTable(t, client, name, "pk", "")
	}

	var got []string

	p := dynamodb.NewListTablesPaginator(client, &dynamodb.ListTablesInput{
		Limit: aws.Int32(2),
	})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		require.NoError(t, err)
		require.LessOrEqual(t, len(out.TableNames), 2)
		got = append(got, out.TableNames...)
	}

	assert.Equal(t, want, got)
}

func TestDDBListTablesNoCursorWhenUnderLimit(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "only-one", "pk", "")

	out, err := client.ListTables(ctx, &dynamodb.ListTablesInput{})
	require.NoError(t, err)
	assert.Equal(t, []string{"only-one"}, out.TableNames)
	assert.Nil(t, out.LastEvaluatedTableName)
}
