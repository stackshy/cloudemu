package iam

import (
	"net/url"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire"
)

// defaultMaxItems is the page size IAM applies when a list request omits
// MaxItems. Real IAM defaults to 100.
const defaultMaxItems = 100

// pageWindow computes the [start, end) slice bounds for a list request given
// the request's Marker/MaxItems parameters and the total number of (already
// sorted) items. It returns the next Marker and whether the result is
// truncated. The Marker is an opaque base64-encoded offset into the sorted
// list, which is stable as long as the caller sorts deterministically.
func pageWindow(total int, form url.Values) (start, end int, nextMarker string, truncated bool) {
	start = decodeMarker(form.Get("Marker"))
	if start > total {
		start = total
	}

	maxItems := defaultMaxItems
	if v := form.Get("MaxItems"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxItems = n
		}
	}

	end = start + maxItems
	if end >= total {
		return start, total, "", false
	}

	return start, end, wire.EncodeOffset(end), true
}

// decodeMarker parses an opaque Marker back into an offset. A missing or
// malformed marker resets to the start of the list (IAM tolerates bad markers).
func decodeMarker(marker string) int {
	n, _ := wire.DecodeOffset(marker)

	return n
}
