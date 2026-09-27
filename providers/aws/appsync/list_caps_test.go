package appsync_test

import (
	"context"
	"fmt"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/appsync"
	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

// listFn runs one AppSync List operation and returns the page length and token.
type listFn func(ctx context.Context, page driver.Page) (int, string, error)

// listOps builds every List operation against one API that already holds n
// data sources and n API keys, with n APIs in total.
func listOps(t *testing.T, n int) (*appsync.Mock, map[string]listFn) {
	t.Helper()

	m := newMock(t)
	ctx := context.Background()
	api := createAPI(t, m, "api-0")

	for i := 1; i < n; i++ {
		createAPI(t, m, fmt.Sprintf("api-%d", i))
	}

	for i := 0; i < n; i++ {
		if _, err := m.CreateDataSource(ctx, &driver.CreateDataSourceInput{
			APIID: api.APIID, Name: fmt.Sprintf("src%d", i), Type: driver.DataSourceNone,
		}); err != nil {
			t.Fatalf("CreateDataSource: %v", err)
		}

		if _, err := m.CreateAPIKey(ctx, &driver.CreateAPIKeyInput{APIID: api.APIID}); err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
	}

	return m, map[string]listFn{
		"ListGraphqlApis": func(ctx context.Context, p driver.Page) (int, string, error) {
			out, next, err := m.ListGraphqlAPIs(ctx, p)
			return len(out), next, err
		},
		"ListDataSources": func(ctx context.Context, p driver.Page) (int, string, error) {
			out, next, err := m.ListDataSources(ctx, api.APIID, p)
			return len(out), next, err
		},
		"ListApiKeys": func(ctx context.Context, p driver.Page) (int, string, error) {
			out, next, err := m.ListAPIKeys(ctx, api.APIID, p)
			return len(out), next, err
		},
	}
}

func TestListOpsRejectMaxResultsOver25(t *testing.T) {
	_, ops := listOps(t, 1)

	const want = "1 validation error detected: Value '26' at 'maxResults' failed to satisfy constraint: " +
		"Member must have value less than or equal to 25"

	for name, list := range ops {
		t.Run(name, func(t *testing.T) {
			_, _, err := list(context.Background(), driver.Page{MaxResults: 26})
			assertException(t, err, driver.ExBadRequest)

			if got := cerrors.Message(err); got != want {
				t.Fatalf("message = %q, want %q", got, want)
			}

			if _, _, err = list(context.Background(), driver.Page{MaxResults: 25}); err != nil {
				t.Fatalf("maxResults=25 rejected: %v", err)
			}
		})
	}
}

func TestListOpsRejectNegativeMaxResults(t *testing.T) {
	_, ops := listOps(t, 1)

	for name, list := range ops {
		t.Run(name, func(t *testing.T) {
			_, _, err := list(context.Background(), driver.Page{MaxResults: -1})
			assertException(t, err, driver.ExBadRequest)
		})
	}
}

func TestListOpsDefaultPageIs25(t *testing.T) {
	_, ops := listOps(t, 30)

	for name, list := range ops {
		t.Run(name, func(t *testing.T) {
			n, next, err := list(context.Background(), driver.Page{})
			if err != nil {
				t.Fatalf("list: %v", err)
			}

			if n != 25 || next == "" {
				t.Fatalf("default page len=%d next=%q, want 25 and a token", n, next)
			}

			n, next, err = list(context.Background(), driver.Page{NextToken: next})
			if err != nil || n != 5 || next != "" {
				t.Fatalf("second page len=%d next=%q err=%v, want 5 and no token", n, next, err)
			}
		})
	}
}
