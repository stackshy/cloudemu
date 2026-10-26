package cloudwatch

import (
	"math"
	"slices"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Period rules from the MetricStat, MetricDataQuery and GetMetricStatistics
// API references. Period is an Integer of at least 1. A regular resolution
// period is a multiple of 60, and a high-resolution metric also allows 1, 5,
// 10, 20 and 30.
const (
	defaultPeriodSeconds  = 60
	maxPeriodSeconds      = math.MaxInt32
	periodSecondsInMinute = 60
)

// validPeriod reports whether p is 1, 5, 10, 20, 30 or a multiple of 60 that
// fits the API's Integer type.
func validPeriod(p int) bool {
	switch {
	case p < 1 || p > maxPeriodSeconds:
		return false
	case p%periodSecondsInMinute == 0:
		return true
	default:
		periods := highResolutionPeriods()

		return slices.Contains(periods[:], p)
	}
}

// highResolutionPeriods are the periods under a minute that a high-resolution
// metric allows.
func highResolutionPeriods() [5]int {
	return [5]int{1, 5, 10, 20, 30}
}

// ValidatePeriod returns an InvalidArgument error when period is not a valid
// metric period. param names the request field in the message, such as
// "MetricDataQueries.member.1.MetricStat.Period".
func (*Mock) ValidatePeriod(param string, period int) error {
	if validPeriod(period) {
		return nil
	}

	return errors.Newf(errors.InvalidArgument,
		"The value %d for parameter %s is not a valid period. Valid periods are 1, 5, 10, 20, 30, 60 and any multiple of 60.",
		period, param)
}
