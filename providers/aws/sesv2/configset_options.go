package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// withConfigSet locks the named config set and runs fn against it.
func (m *Mock) withConfigSet(name string, fn func(cs *driver.ConfigurationSet)) error {
	d, ok := m.configSets.Get(name)
	if !ok {
		return errConfigSetNotFound(name)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	fn(&d.cs)

	return nil
}

// CreateConfigurationSetEventDestination adds an event destination.
//
//nolint:gocritic // in is passed by value to match the driver interface.
func (m *Mock) CreateConfigurationSetEventDestination(_ context.Context, in driver.EventDestinationInput) error {
	return m.withConfigSet(in.ConfigurationSetName, func(cs *driver.ConfigurationSet) {
		cs.EventDestinations = append(cs.EventDestinations, eventDestinationFromInput(&in))
	})
}

// UpdateConfigurationSetEventDestination replaces an existing event destination.
//
//nolint:gocritic // in is passed by value to match the driver interface.
func (m *Mock) UpdateConfigurationSetEventDestination(_ context.Context, in driver.EventDestinationInput) error {
	found := false

	err := m.withConfigSet(in.ConfigurationSetName, func(cs *driver.ConfigurationSet) {
		for i := range cs.EventDestinations {
			if cs.EventDestinations[i].Name == in.EventDestinationName {
				cs.EventDestinations[i] = eventDestinationFromInput(&in)
				found = true

				return
			}
		}
	})
	if err != nil {
		return err
	}

	if !found {
		return cerrors.Newf(cerrors.NotFound, "event destination %q does not exist", in.EventDestinationName)
	}

	return nil
}

// DeleteConfigurationSetEventDestination removes an event destination.
func (m *Mock) DeleteConfigurationSetEventDestination(_ context.Context, configSet, name string) error {
	found := false

	err := m.withConfigSet(configSet, func(cs *driver.ConfigurationSet) {
		kept := cs.EventDestinations[:0]

		for _, ed := range cs.EventDestinations {
			if ed.Name == name {
				found = true
				continue
			}

			kept = append(kept, ed)
		}

		cs.EventDestinations = kept
	})
	if err != nil {
		return err
	}

	if !found {
		return cerrors.Newf(cerrors.NotFound, "event destination %q does not exist", name)
	}

	return nil
}

// GetConfigurationSetEventDestinations lists event destinations of a config set.
func (m *Mock) GetConfigurationSetEventDestinations(_ context.Context, configSet string) ([]driver.EventDestination, error) {
	d, ok := m.configSets.Get(configSet)
	if !ok {
		return nil, errConfigSetNotFound(configSet)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	return append([]driver.EventDestination(nil), d.cs.EventDestinations...), nil
}

func eventDestinationFromInput(in *driver.EventDestinationInput) driver.EventDestination {
	return driver.EventDestination{
		Name:                in.EventDestinationName,
		Enabled:             in.Enabled,
		MatchingEventTypes:  append([]string(nil), in.MatchingEventTypes...),
		KinesisFirehoseARN:  in.KinesisFirehoseARN,
		SNSTopicARN:         in.SNSTopicARN,
		CloudWatchNamespace: in.CloudWatchNamespace,
	}
}

// PutConfigurationSetArchivingOptions sets the archiving ARN.
func (m *Mock) PutConfigurationSetArchivingOptions(_ context.Context, configSet, archiveARN string) error {
	return m.withConfigSet(configSet, func(cs *driver.ConfigurationSet) { cs.ArchiveARN = archiveARN })
}

// PutConfigurationSetDeliveryOptions sets TLS policy and sending pool.
func (m *Mock) PutConfigurationSetDeliveryOptions(_ context.Context, configSet, tlsPolicy, sendingPool string) error {
	return m.withConfigSet(configSet, func(cs *driver.ConfigurationSet) {
		cs.TLSPolicy = tlsPolicy
		cs.SendingPoolN = sendingPool
	})
}

// PutConfigurationSetReputationOptions toggles reputation metrics.
func (m *Mock) PutConfigurationSetReputationOptions(_ context.Context, configSet string, reputationEnabled bool) error {
	return m.withConfigSet(configSet, func(cs *driver.ConfigurationSet) { cs.ReputationOn = reputationEnabled })
}

// PutConfigurationSetSendingOptions toggles sending.
func (m *Mock) PutConfigurationSetSendingOptions(_ context.Context, configSet string, sendingEnabled bool) error {
	return m.withConfigSet(configSet, func(cs *driver.ConfigurationSet) { cs.SendingEnabled = sendingEnabled })
}

// PutConfigurationSetSuppressionOptions sets suppressed reasons.
func (m *Mock) PutConfigurationSetSuppressionOptions(_ context.Context, configSet string, suppressedReasons []string) error {
	return m.withConfigSet(configSet, func(cs *driver.ConfigurationSet) {
		cs.SuppressedReasons = append([]string(nil), suppressedReasons...)
	})
}

// PutConfigurationSetTrackingOptions sets the custom redirect domain.
func (m *Mock) PutConfigurationSetTrackingOptions(_ context.Context, configSet, customRedirectDomain string) error {
	return m.withConfigSet(configSet, func(cs *driver.ConfigurationSet) { cs.CustomRedirectDom = customRedirectDomain })
}

// PutConfigurationSetVdmOptions enables VDM on the config set.
func (m *Mock) PutConfigurationSetVdmOptions(_ context.Context, configSet string) error {
	return m.withConfigSet(configSet, func(cs *driver.ConfigurationSet) { cs.VdmEnabled = true })
}
