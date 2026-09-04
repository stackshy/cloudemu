package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

type placementGroupXML struct {
	GroupName      string    `xml:"groupName"`
	GroupID        string    `xml:"groupId"`
	Strategy       string    `xml:"strategy"`
	State          string    `xml:"state"`
	PartitionCount int       `xml:"partitionCount,omitempty"`
	SpreadLevel    string    `xml:"spreadLevel,omitempty"`
	Tags           []tagItem `xml:"tagSet>item,omitempty"`
}

func (h *Handler) placementGroups() (computedriver.PlacementGroups, bool) {
	pg, ok := h.compute.(computedriver.PlacementGroups)

	return pg, ok
}

func (h *Handler) routePlacementGroups(w http.ResponseWriter, r *http.Request, action string) bool {
	pg, ok := h.placementGroups()
	if !ok {
		return false
	}

	switch action {
	case "CreatePlacementGroup":
		h.createPlacementGroup(w, r, pg)
	case "DeletePlacementGroup":
		h.deletePlacementGroup(w, r, pg)
	case "DescribePlacementGroups":
		h.describePlacementGroups(w, r, pg)
	default:
		return false
	}

	return true
}

func (*Handler) createPlacementGroup(w http.ResponseWriter, r *http.Request, pg computedriver.PlacementGroups) {
	partitionCount, _ := strconv.Atoi(r.Form.Get("PartitionCount"))

	out, err := pg.CreatePlacementGroup(r.Context(), computedriver.PlacementGroupConfig{
		Name:           r.Form.Get("GroupName"),
		Strategy:       r.Form.Get("Strategy"),
		PartitionCount: partitionCount,
		SpreadLevel:    r.Form.Get("SpreadLevel"),
		Tags:           mergeTagSpecs(awsquery.TagSpecs(r.Form), "placement-group"),
	})
	if err != nil {
		writePlacementGroupErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name          `xml:"CreatePlacementGroupResponse"`
		Xmlns   string            `xml:"xmlns,attr"`
		Req     string            `xml:"requestId"`
		Group   placementGroupXML `xml:"placementGroup"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Group: toPlacementGroupXML(out)})
}

func (*Handler) deletePlacementGroup(w http.ResponseWriter, r *http.Request, pg computedriver.PlacementGroups) {
	if err := pg.DeletePlacementGroup(r.Context(), r.Form.Get("GroupName")); err != nil {
		writePlacementGroupErr(w, err)
		return
	}

	writeReturnTrue(w, "DeletePlacementGroupResponse")
}

func (*Handler) describePlacementGroups(w http.ResponseWriter, r *http.Request, pg computedriver.PlacementGroups) {
	items, err := pg.DescribePlacementGroups(r.Context(),
		awsquery.ListStrings(r.Form, "GroupName"), awsquery.ListStrings(r.Form, "GroupId"))
	if err != nil {
		writePlacementGroupErr(w, err)
		return
	}

	out := make([]placementGroupXML, 0, len(items))
	for i := range items {
		out = append(out, toPlacementGroupXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name            `xml:"DescribePlacementGroupsResponse"`
		Xmlns   string              `xml:"xmlns,attr"`
		Req     string              `xml:"requestId"`
		Set     []placementGroupXML `xml:"placementGroupSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func toPlacementGroupXML(pg *computedriver.PlacementGroup) placementGroupXML {
	return placementGroupXML{
		GroupName:      pg.Name,
		GroupID:        pg.ID,
		Strategy:       pg.Strategy,
		State:          nonEmpty(pg.State, stateAvailable),
		PartitionCount: pg.PartitionCount,
		SpreadLevel:    pg.SpreadLevel,
		Tags:           toTagItems(pg.Tags),
	}
}

func writePlacementGroupErr(w http.ResponseWriter, err error) {
	if cerrors.IsAlreadyExists(err) {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidPlacementGroup.Duplicate", cerrors.Message(err))
		return
	}

	writeErrWithNotFound(w, err, "InvalidPlacementGroup.Unknown", "DependencyViolation")
}
