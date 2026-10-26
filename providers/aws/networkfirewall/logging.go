package networkfirewall

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	nfdriver "github.com/stackshy/cloudemu/v2/services/networkfirewall/driver"
)

const (
	logDestS3         = "S3"
	logDestCloudWatch = "CloudWatchLogs"
	logDestFirehose   = "KinesisDataFirehose"
	maxLogDestConfigs = 3

	// typeChangeCost counts a LogDestinationType change as the remove plus the add it is.
	typeChangeCost = 2
)

// Enum values copied verbatim from the AWS SDK (LogType, LogDestinationType).
//
//nolint:gochecknoglobals // immutable enum value sets.
var (
	logTypes     = []string{"ALERT", "FLOW", "TLS"}
	logDestTypes = []string{logDestS3, logDestCloudWatch, logDestFirehose}

	// logDestKeys lists, per destination type, the required key and the
	// optional keys of LogDestination.
	logDestRequired = map[string]string{logDestS3: "bucketName", logDestCloudWatch: "logGroup", logDestFirehose: "deliveryStream"}
	logDestOptional = map[string]string{logDestS3: "prefix"}
)

// UpdateLoggingConfiguration sets the firewall's log destinations. As in AWS,
// a single call may add one destination, remove one, or change the destination
// of one entry; an omitted configuration disables logging.
func (m *Mock) UpdateLoggingConfiguration(_ context.Context, firewallName string, cfg *nfdriver.LoggingConfiguration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateName("FirewallName", firewallName); err != nil {
		return err
	}

	if !m.firewalls.Has(firewallName) {
		return cerrors.Newf(cerrors.NotFound, "firewall %q not found", firewallName)
	}

	var next nfdriver.LoggingConfiguration
	if cfg != nil {
		next = *cfg
	}

	if err := validateLogDestinations(next.Destinations); err != nil {
		return err
	}

	current := m.logging[firewallName]
	if current == nil {
		current = &nfdriver.LoggingConfiguration{}
	}

	if err := checkSingleLogChange(current.Destinations, next.Destinations); err != nil {
		return err
	}

	dashboard := current.EnableMonitoringDashboard
	if next.EnableMonitoringDashboard != nil {
		dashboard = next.EnableMonitoringDashboard
	}

	if len(next.Destinations) == 0 && dashboard == nil {
		delete(m.logging, firewallName)

		return nil
	}

	m.logging[firewallName] = &nfdriver.LoggingConfiguration{
		Destinations:              cloneLogConfigs(next.Destinations),
		EnableMonitoringDashboard: cloneBool(dashboard),
	}

	return nil
}

// DescribeLoggingConfiguration returns the firewall's logging configuration.
func (m *Mock) DescribeLoggingConfiguration(_ context.Context, firewallName string) (*nfdriver.LoggingConfiguration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := validateName("FirewallName", firewallName); err != nil {
		return nil, err
	}

	if !m.firewalls.Has(firewallName) {
		return nil, cerrors.Newf(cerrors.NotFound, "firewall %q not found", firewallName)
	}

	cur := m.logging[firewallName]
	if cur == nil {
		return &nfdriver.LoggingConfiguration{}, nil
	}

	return &nfdriver.LoggingConfiguration{
		Destinations:              cloneLogConfigs(cur.Destinations),
		EnableMonitoringDashboard: cloneBool(cur.EnableMonitoringDashboard),
	}, nil
}

// validateLogDestinations checks every entry against the SDK enums and the
// per-type destination keys, and enforces one entry per log type, max three.
func validateLogDestinations(cfgs []nfdriver.LogDestinationConfig) error {
	if len(cfgs) > maxLogDestConfigs {
		return invalid("at most %d log destination configs are allowed, got %d", maxLogDestConfigs, len(cfgs))
	}

	seen := make(map[string]bool, len(cfgs))

	for _, c := range cfgs {
		if !oneOf(c.LogType, logTypes...) {
			return invalid("LogType %q is not valid (want one of %v)", c.LogType, logTypes)
		}

		if seen[c.LogType] {
			return invalid("more than one log destination config for LogType %s", c.LogType)
		}

		seen[c.LogType] = true

		if !oneOf(c.LogDestinationType, logDestTypes...) {
			return invalid("LogDestinationType %q is not valid (want one of %v)", c.LogDestinationType, logDestTypes)
		}

		if err := validateLogDestinationKeys(c); err != nil {
			return err
		}
	}

	return nil
}

func validateLogDestinationKeys(c nfdriver.LogDestinationConfig) error {
	required := logDestRequired[c.LogDestinationType]
	if c.LogDestination[required] == "" {
		return invalid("LogDestination for %s requires %q", c.LogDestinationType, required)
	}

	for k := range c.LogDestination {
		if k != required && k != logDestOptional[c.LogDestinationType] {
			return invalid("LogDestination key %q is not valid for %s", k, c.LogDestinationType)
		}
	}

	return nil
}

// checkSingleLogChange enforces AWS's "one change per call" rule: add one entry,
// remove one entry, or change the LogDestination of one entry. Changing the
// LogDestinationType (or LogType) of an entry is a remove plus an add, so it
// needs two calls.
func checkSingleLogChange(current, next []nfdriver.LogDestinationConfig) error {
	have := make(map[string]nfdriver.LogDestinationConfig, len(current))
	for _, c := range current {
		have[c.LogType] = c
	}

	changes := 0

	for _, n := range next {
		c, ok := have[n.LogType]

		switch {
		case !ok:
			changes++
		case c.LogDestinationType != n.LogDestinationType:
			changes += typeChangeCost
		case !sameDestination(c.LogDestination, n.LogDestination):
			changes++
		}

		delete(have, n.LogType)
	}

	changes += len(have) // entries dropped from the configuration

	if changes > 1 {
		return invalid("UpdateLoggingConfiguration accepts one change per call (add one destination, remove one, or change one destination)")
	}

	return nil
}

func sameDestination(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if b[k] != v {
			return false
		}
	}

	return true
}

func cloneLogConfigs(cfgs []nfdriver.LogDestinationConfig) []nfdriver.LogDestinationConfig {
	if len(cfgs) == 0 {
		return nil
	}

	out := make([]nfdriver.LogDestinationConfig, len(cfgs))
	for i, c := range cfgs {
		out[i] = nfdriver.LogDestinationConfig{
			LogType:            c.LogType,
			LogDestinationType: c.LogDestinationType,
			LogDestination:     copyTags(c.LogDestination),
		}
	}

	return out
}
