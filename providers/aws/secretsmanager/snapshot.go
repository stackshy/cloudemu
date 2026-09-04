package secretsmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// secretsSnapshot is the full serialized state of the Secrets Manager mock:
// every secret keyed by name, each with its metadata, version history, and
// soft-delete state, so identifiers (ARN, version ids) restore unchanged.
type secretsSnapshot struct {
	Secrets map[string]*secretSnapshot `json:"secrets,omitempty"`
}

type secretSnapshot struct {
	Info     driver.SecretInfo      `json:"info"`
	Versions []driver.SecretVersion `json:"versions,omitempty"`
	// Stages maps each staging label to the version id holding it, preserving the
	// AWSCURRENT/AWSPREVIOUS/custom assignment across snapshot/restore.
	Stages         map[string]string `json:"stages,omitempty"`
	DeletedAt      time.Time         `json:"deletedAt,omitempty"`
	RecoveryWindow int               `json:"recoveryWindow,omitempty"`
	// ResourcePolicy is the JSON resource-based policy attached to the secret,
	// preserved across snapshot/restore.
	ResourcePolicy string `json:"resourcePolicy,omitempty"`
	// Rotation* preserve the RotateSecret/CancelRotateSecret configuration
	// across snapshot/restore.
	RotationEnabled      bool                       `json:"rotationEnabled,omitempty"`
	RotationLambdaARN    string                     `json:"rotationLambdaARN,omitempty"`
	RotationRules        driver.SecretRotationRules `json:"rotationRules,omitempty"`
	RotationConfiguredAt time.Time                  `json:"rotationConfiguredAt,omitempty"`
	LastRotatedDate      time.Time                  `json:"lastRotatedDate,omitempty"`
}

// Snapshot captures every secret's full state as JSON. includeAssets is unused —
// a secret without its value cannot be restored usefully, so values are always
// captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := secretsSnapshot{Secrets: make(map[string]*secretSnapshot)}

	for name, sd := range m.secrets.All() {
		sd.mu.RLock()
		snap.Secrets[name] = &secretSnapshot{
			Info:                 sd.info,
			Versions:             sd.versions,
			Stages:               sd.stages,
			DeletedAt:            sd.deletedAt,
			RecoveryWindow:       sd.recoveryWindow,
			ResourcePolicy:       sd.resourcePolicy,
			RotationEnabled:      sd.rotationEnabled,
			RotationLambdaARN:    sd.rotationLambdaARN,
			RotationRules:        sd.rotationRules,
			RotationConfiguredAt: sd.rotationConfiguredAt,
			LastRotatedDate:      sd.lastRotatedDate,
		}
		sd.mu.RUnlock()
	}

	return json.Marshal(snap)
}

// Restore rebuilds every secret under its original name with its metadata and
// version history intact.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap secretsSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("secretsmanager: parse snapshot: %w", err)
	}

	for name, ss := range snap.Secrets {
		m.secrets.Set(name, &secretData{
			info:                 ss.Info,
			versions:             ss.Versions,
			stages:               ss.Stages,
			deletedAt:            ss.DeletedAt,
			recoveryWindow:       ss.RecoveryWindow,
			resourcePolicy:       ss.ResourcePolicy,
			rotationEnabled:      ss.RotationEnabled,
			rotationLambdaARN:    ss.RotationLambdaARN,
			rotationRules:        ss.RotationRules,
			rotationConfiguredAt: ss.RotationConfiguredAt,
			lastRotatedDate:      ss.LastRotatedDate,
		})
	}

	return nil
}
