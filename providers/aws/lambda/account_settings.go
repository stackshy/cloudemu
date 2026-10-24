package lambda

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// AWS Lambda default account limits, in bytes, from the Lambda quotas page
// (https://docs.aws.amazon.com/lambda/latest/dg/gettingstarted-limits.html):
// 300 GB of code storage for .zip functions and layers, a 50 MB zipped
// deployment package uploaded directly, and 250 MB unzipped including layers.
// The concurrency budget is accountConcurrentExecutionLimit.
const (
	accountTotalCodeSizeLimit int64 = 300 * 1024 * 1024 * 1024
	accountCodeSizeZipped     int64 = 50 * 1024 * 1024
	accountCodeSizeUnzipped   int64 = 250 * 1024 * 1024
)

// GetAccountSettings reports the account's Lambda limits and current usage
// (GetAccountSettings). UnreservedConcurrentExecutions is the concurrency pool
// minus every function's reserved concurrency; FunctionCount counts functions,
// and the used TotalCodeSize sums the deployment packages of every function's
// $LATEST and published versions plus every layer version, since "each
// function version and layer version consumes storage". It is an AWS-only
// capability the Lambda server handler type-asserts for.
func (m *Mock) GetAccountSettings(_ context.Context) (*driver.AccountSettings, error) {
	var (
		functions int64
		codeSize  int64
	)

	all := m.funcs.All()
	for name := range all {
		functions++
		codeSize += all[name].info.CodeSize

		for _, v := range all[name].versions {
			codeSize += v.codeSize
		}
	}

	for _, ld := range m.layers.All() {
		for _, lv := range ld.versions.All() {
			codeSize += lv.ContentSize
		}
	}

	return &driver.AccountSettings{
		TotalCodeSizeLimit:             accountTotalCodeSizeLimit,
		CodeSizeUnzipped:               accountCodeSizeUnzipped,
		CodeSizeZipped:                 accountCodeSizeZipped,
		ConcurrentExecutions:           accountConcurrentExecutionLimit,
		UnreservedConcurrentExecutions: accountConcurrentExecutionLimit - m.sumReservedConcurrencyExcept(""),
		FunctionCount:                  functions,
		TotalCodeSizeUsed:              codeSize,
	}, nil
}
