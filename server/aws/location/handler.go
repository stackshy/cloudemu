// Package location implements the Amazon Location Service control-plane API
// (restJson1) as a server.Handler. Point the real
// aws-sdk-go-v2/service/location client (or the `aws location` CLI, or the
// aws_location_map / aws_location_place_index / aws_location_route_calculator /
// aws_location_geofence_collection / aws_location_tracker Terraform resources)
// at a Server registered with this handler and the map, place-index,
// route-calculator, geofence-collection, tracker and tagging operations work
// end-to-end against an in-memory driver.
//
// Location routes by HTTP verb + path under versioned roots (POST
// /maps/v0/maps, GET /maps/v0/maps/{MapName}, POST /places/v0/indexes,
// POST /geofencing/v0/collections, POST /tracking/v0/trackers, …); there is no
// X-Amz-Target header. Matches claims those versioned roots — which are
// distinctive to Location — and the shared /tags/{ResourceArn} path only when
// the ARN is a Location (:geo:) ARN, so it runs before the S3 catch-all and
// never shadows another service's tag operations.
//
// This is a control-plane-only surface: the geocoding, routing, geofence
// evaluation, device-position and map-tile data planes are out of scope.
package location

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

// Versioned path roots, one per resource type. The map key is the first path
// segment; the value carries the collection and list sub-paths plus the driver
// glue for that resource.
const (
	segMaps       = "maps"
	segPlaces     = "places"
	segRoutes     = "routes"
	segGeofencing = "geofencing"
	segTracking   = "tracking"
	segTags       = "tags"
	segVersion    = "v0"
)

// tagsPrefix and arnMarker scope the shared /tags path to Location ARNs.
const (
	tagsPrefix = "/tags/"
	arnMarker  = ":geo:"
)

// Handler serves Amazon Location Service requests against a driver.
type Handler struct {
	loc    driver.Location
	routes map[string]*resource
}

// New returns a Location handler backed by d.
func New(d driver.Location) *Handler {
	h := &Handler{loc: d}
	h.routes = map[string]*resource{
		segMaps:       h.mapsResource(),
		segPlaces:     h.placeIndexResource(),
		segRoutes:     h.routeCalculatorResource(),
		segGeofencing: h.geofenceCollectionResource(),
		segTracking:   h.trackerResource(),
	}

	return h
}

// isVersionedRoot reports whether segs names a claimed /{root}/v0/... path.
func (h *Handler) isVersionedRoot(segs []string) bool {
	if len(segs) < 2 || segs[1] != segVersion {
		return false
	}

	_, ok := h.routes[segs[0]]

	return ok
}

// Matches claims the Location path shapes. The versioned roots are unique to
// Location; the /tags root is shared, so it is claimed only for a Location ARN.
func (h *Handler) Matches(r *http.Request) bool {
	segs := splitPath(r.URL.Path)
	if len(segs) == 0 {
		return false
	}

	if segs[0] == segTags {
		return strings.HasPrefix(r.URL.Path, tagsPrefix) && strings.Contains(r.URL.Path, arnMarker)
	}

	return h.isVersionedRoot(segs)
}

// ServeHTTP dispatches a Location request on its path shape.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	segs := splitPath(r.URL.Path)
	if len(segs) == 0 {
		notFoundPath(w, r.URL.Path)

		return
	}

	if segs[0] == segTags {
		h.serveTags(w, r)

		return
	}

	res, ok := h.routes[segs[0]]
	if !ok || len(segs) < 2 || segs[1] != segVersion {
		notFoundPath(w, r.URL.Path)

		return
	}

	res.serve(w, r, segs[2:])
}

// splitPath splits a decoded URL path into its non-empty segments.
func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}
