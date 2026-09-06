package cognito

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// GetUserPoolMfaConfig returns a pool's MFA configuration. The Terraform AWS
// provider reads this on every user-pool refresh.
func (m *Mock) GetUserPoolMfaConfig(_ context.Context, id string) (*driver.UserPoolMfaConfig, error) {
	pool, ok := m.userPools.Get(id)
	if !ok {
		return nil, resourceNotFound("User pool %s does not exist.", id)
	}

	return &driver.UserPoolMfaConfig{
		MFAConfiguration:              pool.MFAConfiguration,
		SoftwareTokenMfaConfiguration: copySoftwareTokenMfa(pool.SoftwareTokenMfaConfig),
		SmsMfaConfiguration:           copySmsMfa(pool.SmsMfaConfig),
	}, nil
}

// SetUserPoolMfaConfig replaces a pool's MFA configuration and returns the
// stored result.
func (m *Mock) SetUserPoolMfaConfig(_ context.Context, in driver.SetUserPoolMfaConfigInput) (*driver.UserPoolMfaConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.userPools.Get(in.UserPoolID)
	if !ok {
		return nil, resourceNotFound("User pool %s does not exist.", in.UserPoolID)
	}

	pool = copyUserPool(pool)

	if in.MFAConfiguration != "" {
		pool.MFAConfiguration = in.MFAConfiguration
	}

	pool.SoftwareTokenMfaConfig = copySoftwareTokenMfa(in.SoftwareTokenMfaConfiguration)
	pool.SmsMfaConfig = copySmsMfa(in.SmsMfaConfiguration)
	pool.LastModifiedDate = m.now()

	m.userPools.Set(in.UserPoolID, pool)

	return &driver.UserPoolMfaConfig{
		MFAConfiguration:              pool.MFAConfiguration,
		SoftwareTokenMfaConfiguration: copySoftwareTokenMfa(pool.SoftwareTokenMfaConfig),
		SmsMfaConfiguration:           copySmsMfa(pool.SmsMfaConfig),
	}, nil
}

func copySoftwareTokenMfa(in *driver.SoftwareTokenMfaConfig) *driver.SoftwareTokenMfaConfig {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func copySmsMfa(in *driver.SmsMfaConfig) *driver.SmsMfaConfig {
	if in == nil {
		return nil
	}

	out := *in

	if in.SmsConfiguration != nil {
		cfg := *in.SmsConfiguration
		out.SmsConfiguration = &cfg
	}

	return &out
}
