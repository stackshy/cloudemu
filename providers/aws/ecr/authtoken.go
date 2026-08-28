package ecr

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/regionctx"
)

// authTokenTTL is how long a GetAuthorizationToken response stays valid. Real
// ECR tokens last 12 hours.
const authTokenTTL = 12 * time.Hour

// GetAuthorizationToken returns a base64 "AWS:<password>" credential, the
// registry proxy endpoint, and an expiry — everything `docker login` and
// image push/pull need. The emulator does not validate the token on later
// requests; it exists so auth flows succeed.
func (m *Mock) GetAuthorizationToken(ctx context.Context) (token, proxyEndpoint string, expiresAt time.Time, err error) {
	token = base64.StdEncoding.EncodeToString([]byte("AWS:cloudemu"))
	proxyEndpoint = fmt.Sprintf("https://%s.dkr.ecr.%s.amazonaws.com", m.opts.AccountID, regionctx.RegionOr(ctx, m.opts.Region))
	expiresAt = m.opts.Clock.Now().Add(authTokenTTL).UTC()

	return token, proxyEndpoint, expiresAt, nil
}
