package cognito

import (
	"context"
	"regexp"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// TestTokenValidityHugeValuesRejected pins that a value whose nanosecond
// duration overflows int64 is rejected instead of wrapping to a short token.
// 213504 days is just past the largest time.Duration.
func TestTokenValidityHugeValuesRejected(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)

	for _, tc := range []struct {
		name  string
		in    driver.CreateUserPoolClientInput
		units *driver.TokenValidityUnits
	}{
		{"access days", driver.CreateUserPoolClientInput{AccessTokenValidity: int32Ptr(213504)},
			&driver.TokenValidityUnits{AccessToken: driver.TimeUnitDays}},
		{"id max int32 days", driver.CreateUserPoolClientInput{IDTokenValidity: int32Ptr(2147483647)},
			&driver.TokenValidityUnits{IDToken: driver.TimeUnitDays}},
		{"refresh max int32 days", driver.CreateUserPoolClientInput{RefreshTokenValidity: int32Ptr(2147483647)}, nil},
		{"access min int32 days", driver.CreateUserPoolClientInput{AccessTokenValidity: int32Ptr(-2147483648)},
			&driver.TokenValidityUnits{AccessToken: driver.TimeUnitDays}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.UserPoolID, in.ClientName, in.TokenValidityUnits = pool.ID, "huge", tc.units

			_, err := m.CreateUserPoolClient(ctx, in)
			assertException(t, err, driver.ExInvalidParameter, "Invalid range for token validity.")
		})
	}

	if d := unitDuration(213504, driver.TimeUnitDays, ""); d < maxAccessValidity {
		t.Fatalf("unitDuration(213504 days) = %v, want a saturated (huge) duration", d)
	}
}

func TestSimulatedDeliveryFollowsPoolAttribute(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()

	resend := func(verified []string, username string) *driver.CodeDeliveryDetails {
		t.Helper()

		pool, err := m.CreateUserPool(ctx, driver.CreateUserPoolInput{Name: "sim", AutoVerifiedAttributes: verified})
		requireNoError(t, err, "CreateUserPool")

		client, err := m.CreateUserPoolClient(ctx, driver.CreateUserPoolClientInput{
			UserPoolID: pool.ID, ClientName: "hidden", PreventUserExistenceErrors: "ENABLED",
		})
		requireNoError(t, err, "CreateUserPoolClient")

		d, err := m.ResendConfirmationCode(ctx, driver.ClientUserInput{ClientID: client.ClientID, Username: username})
		requireNoError(t, err, "ResendConfirmationCode")

		return d
	}

	d := resend([]string{"email"}, "ghost")
	if d.DeliveryMedium != driver.DeliveryMediumEmail || d.AttributeName != "email" || d.Destination != "g***@e***" {
		t.Fatalf("email pool, plain username = %+v", d)
	}

	d = resend([]string{"email"}, "casper@example.org")
	if d.Destination != "c***@e***" {
		t.Fatalf("email pool, email username = %+v", d)
	}

	phone := regexp.MustCompile(`^\+\*+\d{4}$`)

	d = resend([]string{"phone_number"}, "ghost")
	if d.DeliveryMedium != driver.DeliveryMediumSMS || d.AttributeName != "phone_number" || !phone.MatchString(d.Destination) {
		t.Fatalf("phone pool, plain username = %+v", d)
	}

	if again := resend([]string{"phone_number"}, "ghost"); again.Destination != d.Destination {
		t.Fatalf("simulated phone not stable: %q then %q", d.Destination, again.Destination)
	}

	d = resend([]string{"phone_number"}, "+15551231234")
	if d.Destination != "+*******1234" {
		t.Fatalf("phone pool, phone username = %+v", d)
	}

	if d = resend(nil, "ghost"); d != nil {
		t.Fatalf("pool without verification = %+v, want no delivery", d)
	}
}
