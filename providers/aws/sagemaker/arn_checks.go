package sagemaker

import (
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// familyArnChecks registers the liveness checks of the resource families added
// after the original service: each entry maps an ARN resource type
// ("project") to a check over the part after "<type>/". byName and byARNKey are
// the helpers arnChecks builds for the common key shapes (name-keyed and
// ARN-keyed stores).
func (m *Mock) familyArnChecks(
	byName func(has func(string) bool) func(string, string) bool,
	byARNKey func(has func(string) bool) func(string, string) bool,
) map[string]func(arn, rest string) bool {
	monDef := func(kind string) func(string, string) bool {
		return func(_, rest string) bool { return m.monDefs.Has(defKey(kind, rest)) }
	}

	return map[string]func(string, string) bool{
		"project":                             byName(m.projects.Has),
		"workforce":                           byName(m.workforces.Has),
		"image":                               byName(m.images.Has),
		"app-image-config":                    byName(m.appImageConfigs.Has),
		"monitoring-schedule":                 byName(m.monSchedules.Has),
		"data-quality-job-definition":         monDef(driver.MonitoringDataQuality),
		"model-quality-job-definition":        monDef(driver.MonitoringModelQuality),
		"model-bias-job-definition":           monDef(driver.MonitoringModelBias),
		"model-explainability-job-definition": monDef(driver.MonitoringModelExplainability),
		"experiment-trial-component":          func(_, rest string) bool { return m.trialComponents.Has(tcKey(rest)) },
		"artifact":                            byARNKey(m.lineageEntities.Has),
		"action":                              byARNKey(m.lineageEntities.Has),
		"context":                             byARNKey(m.lineageEntities.Has),
		"image-version": func(_, rest string) bool {
			name, ver, ok := strings.Cut(rest, "/")
			v, err := strconv.Atoi(ver)

			return ok && err == nil && m.imageVersions.Has(imageVersionKey(name, v))
		},
		"workteam": func(_, rest string) bool {
			_, name, ok := strings.Cut(rest, "/")

			return ok && m.workteams.Has(name)
		},
	}
}
