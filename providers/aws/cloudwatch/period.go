package cloudwatch

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/monitoring/metricmath"
)

// defaultPeriodSeconds is the period of a metric read that sets none.
const defaultPeriodSeconds = 60

// ValidatePeriod returns an InvalidArgument error when period is not a valid
// metric period. param names the request field in the message, such as
// "MetricDataQueries.member.1.MetricStat.Period".
func (*Mock) ValidatePeriod(param string, period int) error {
	if metricmath.ValidPeriod(period) {
		return nil
	}

	return errors.Newf(errors.InvalidArgument,
		"The value %d for parameter %s is not a valid period. Valid periods are 1, 5, 10, 20, 30, 60 and any multiple of 60.",
		period, param)
}
