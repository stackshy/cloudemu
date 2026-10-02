package compute

import (
	"net/http"
	"strconv"

	gcecompute "github.com/stackshy/cloudemu/v2/providers/gcp/compute"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// imageFamilySegment is the "family" segment of images/family/{family}, which
// the path parser reads as the image name with the family as the action.
const imageFamilySegment = "family"

const (
	imageKind     = "compute#image"
	imageListKind = "compute#imageList"
)

// imageFamilyLookup is the GCP-local capability resolving a user image family
// to its newest image.
type imageFamilyLookup interface {
	ImageFromFamilyGCP(family string) (computedriver.ImageInfo, bool)
}

// getImageFromFamily handles GET .../global/images/family/{family} for the
// project's own images.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getImageFromFamily(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	lookup, ok := h.compute.(imageFamilyLookup)
	if !ok {
		writeNotImplemented(w, "images getFromFamily")
		return
	}

	img, found := lookup.ImageFromFamilyGCP(rp.Action)
	if !found {
		writeImageFamilyNotFound(w, rp)
		return
	}

	scope := rp
	scope.ResourceName = tagOr(img.Tags, gcpImageNameTag, img.Name)

	gcprest.WriteJSON(w, http.StatusOK, toImageResponse(&img, scope, hostFromRequest(r)))
}

// servePublicImages answers the read-only public image projects (debian-cloud,
// ubuntu-os-cloud, ...): get, list and getFromFamily. Writes are refused with
// 403, as they are for any project the caller does not own.
//
//nolint:gocritic // rp is a request-scoped value
func servePublicImages(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if r.Method != http.MethodGet {
		gcprest.WriteError(w, http.StatusForbidden, "forbidden",
			"Required 'compute.images.create' permission for 'projects/"+rp.Project+"'")

		return
	}

	host := hostFromRequest(r)

	switch {
	case rp.ResourceName == "":
		imgs := gcecompute.PublicImages(rp.Project)
		out := make([]imageResponse, 0, len(imgs))

		for i := range imgs {
			out = append(out, toPublicImageResponse(&imgs[i], host))
		}

		gcprest.WriteJSON(w, http.StatusOK, imageListResponse{
			Kind:     imageListKind,
			ID:       "projects/" + rp.Project + "/global/images",
			Items:    out,
			SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeGlobal, "", "images", ""),
		})
	case rp.ResourceName == imageFamilySegment && rp.Action != "":
		img, ok := gcecompute.PublicImageFromFamily(rp.Project, rp.Action)
		if !ok {
			writeImageFamilyNotFound(w, rp)
			return
		}

		gcprest.WriteJSON(w, http.StatusOK, toPublicImageResponse(&img, host))
	default:
		img, ok := gcecompute.GetPublicImage(rp.Project, rp.ResourceName)
		if !ok {
			gcprest.WriteError(w, http.StatusNotFound, "notFound",
				"The resource 'projects/"+rp.Project+"/global/images/"+rp.ResourceName+"' was not found")

			return
		}

		gcprest.WriteJSON(w, http.StatusOK, toPublicImageResponse(&img, host))
	}
}

//nolint:gocritic // rp is a request-scoped value
func writeImageFamilyNotFound(w http.ResponseWriter, rp gcprest.ResourcePath) {
	gcprest.WriteError(w, http.StatusNotFound, "notFound",
		"The resource 'projects/"+rp.Project+"/global/images/family/"+rp.Action+"' was not found")
}

func toPublicImageResponse(img *gcecompute.PublicImage, host string) imageResponse {
	resp := imageResponse{
		Kind:              imageKind,
		ID:                numericID(img.Project + "/" + img.Name),
		Name:              img.Name,
		Status:            diskStatusReady,
		SelfLink:          gcprest.SelfLink(host, img.Project, gcprest.ScopeGlobal, "", "images", img.Name),
		Family:            img.Family,
		DiskSizeGb:        strconv.Itoa(img.DiskSizeGb),
		Description:       img.Description,
		CreationTimestamp: img.CreatedAt,
	}

	if img.Deprecated {
		resp.Deprecated = &imageDeprecation{
			State:       "DEPRECATED",
			Replacement: gcprest.SelfLink(host, img.Project, gcprest.ScopeGlobal, "", "images", img.Replacement),
		}
	}

	return resp
}
