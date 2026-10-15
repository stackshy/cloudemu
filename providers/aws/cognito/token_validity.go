package cognito

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Token-validity bounds Cognito enforces on an app client: access and ID
// tokens last between 5 minutes and 1 day, refresh tokens between 60 minutes
// and 10 years.
const (
	minAccessValidity  = 5 * time.Minute
	maxAccessValidity  = 24 * time.Hour
	minRefreshValidity = 60 * time.Minute
	maxRefreshValidity = 3650 * 24 * time.Hour
)

// checkTokenValidity validates an app client's token-validity values and
// units. A zero or unset value means the default and is not range checked.
func checkTokenValidity(in *driver.CreateUserPoolClientInput) error {
	units := driver.TokenValidityUnits{}
	if in.TokenValidityUnits != nil {
		units = *in.TokenValidityUnits
	}

	for _, u := range []struct{ field, value string }{
		{"accessToken", units.AccessToken}, {"idToken", units.IDToken}, {"refreshToken", units.RefreshToken},
	} {
		if !validUnit(u.value) {
			return invalidParameter("1 validation error detected: Value '%s' at 'tokenValidityUnits.%s' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [seconds, minutes, hours, days]", u.value, u.field)
		}
	}

	checks := []struct {
		value    *int32
		unit     string
		defUnit  string
		min, max time.Duration
	}{
		{in.AccessTokenValidity, units.AccessToken, driver.TimeUnitHours, minAccessValidity, maxAccessValidity},
		{in.IDTokenValidity, units.IDToken, driver.TimeUnitHours, minAccessValidity, maxAccessValidity},
		{in.RefreshTokenValidity, units.RefreshToken, driver.TimeUnitDays, minRefreshValidity, maxRefreshValidity},
	}

	for _, c := range checks {
		if c.value == nil || *c.value == 0 {
			continue
		}

		d := unitDuration(*c.value, c.unit, c.defUnit)
		if *c.value < 0 || d < c.min || d > c.max {
			return invalidParameter("Invalid range for token validity.")
		}
	}

	return nil
}

func validUnit(u string) bool {
	switch u {
	case "", driver.TimeUnitSeconds, driver.TimeUnitMinutes, driver.TimeUnitHours, driver.TimeUnitDays:
		return true
	default:
		return false
	}
}

// setValidity returns a client token-validity value, treating zero as unset.
func setValidity(v *int32) *int32 {
	if v == nil || *v == 0 {
		return nil
	}

	return copyInt32Ptr(v)
}
