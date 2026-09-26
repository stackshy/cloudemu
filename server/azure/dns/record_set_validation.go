package dns

import (
	"errors"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// writeAddressError writes the provider's bad A or AAAA value error as the
// 400 BadRequest real Azure DNS returns. It reports whether err was one.
func writeAddressError(w http.ResponseWriter, err error) bool {
	var ae *dnsdriver.InvalidAddressError
	if !errors.As(err, &ae) {
		return false
	}

	azurearm.WriteError(w, http.StatusBadRequest, "BadRequest", cerrors.Message(err))

	return true
}

// addressValues returns the address of every A or AAAA entry, empty ones
// included, so the provider can reject an entry with no address.
func addressValues[T any](in []T, f func(T) string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}

	return out
}
