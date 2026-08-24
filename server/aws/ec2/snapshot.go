package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

type snapshotXML struct {
	SnapshotID  string    `xml:"snapshotId"`
	VolumeID    string    `xml:"volumeId"`
	State       string    `xml:"status"`
	StartTime   string    `xml:"startTime,omitempty"`
	Progress    string    `xml:"progress,omitempty"`
	OwnerID     string    `xml:"ownerId,omitempty"`
	Description string    `xml:"description,omitempty"`
	VolumeSize  int       `xml:"volumeSize"`
	Encrypted   bool      `xml:"encrypted"`
	Tags        []tagItem `xml:"tagSet>item,omitempty"`
}

type createSnapshotResponseXML struct {
	XMLName   xml.Name `xml:"CreateSnapshotResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	snapshotXML
}

type describeSnapshotsResponseXML struct {
	XMLName     xml.Name      `xml:"DescribeSnapshotsResponse"`
	Xmlns       string        `xml:"xmlns,attr"`
	RequestID   string        `xml:"requestId"`
	SnapshotSet []snapshotXML `xml:"snapshotSet>item"`
}

type deleteSnapshotResponseXML struct {
	XMLName   xml.Name `xml:"DeleteSnapshotResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type modifySnapshotAttributeResponseXML struct {
	XMLName   xml.Name `xml:"ModifySnapshotAttributeResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// copySnapshotResponseXML mirrors AWS CopySnapshot: the new snapshot id plus
// any tags applied by TagSpecification.N.
type copySnapshotResponseXML struct {
	XMLName    xml.Name  `xml:"CopySnapshotResponse"`
	Xmlns      string    `xml:"xmlns,attr"`
	RequestID  string    `xml:"requestId"`
	SnapshotID string    `xml:"snapshotId"`
	Tags       []tagItem `xml:"tagSet>item,omitempty"`
}

// copySnapshot handles Action=CopySnapshot. It is served by the AWS-only
// SnapshotCopier capability; a compute driver that does not implement it
// reports Unimplemented.
func (h *Handler) copySnapshot(w http.ResponseWriter, r *http.Request) {
	copier, ok := h.compute.(computedriver.SnapshotCopier)
	if !ok {
		writeSnapshotErr(w, cerrors.New(cerrors.Unimplemented, "CopySnapshot is not supported"))
		return
	}

	info, err := copier.CopySnapshot(r.Context(), computedriver.CopySnapshotInput{
		SourceRegion:     r.Form.Get("SourceRegion"),
		SourceSnapshotID: r.Form.Get("SourceSnapshotId"),
		Description:      r.Form.Get("Description"),
		Encrypted:        r.Form.Get("Encrypted") == formTrue,
		KmsKeyID:         r.Form.Get("KmsKeyId"),
		Tags:             mergeTagSpecs(awsquery.TagSpecs(r.Form), "snapshot"),
	})
	if err != nil {
		writeSnapshotErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, copySnapshotResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		SnapshotID: info.ID,
		Tags:       toTagItems(info.Tags),
	})
}

//nolint:dupl // per-resource create pattern; mirrors peering/flow-log shape
func (h *Handler) createSnapshot(w http.ResponseWriter, r *http.Request) {
	cfg := computedriver.SnapshotConfig{
		VolumeID:    r.Form.Get("VolumeId"),
		Description: r.Form.Get("Description"),
		Tags:        mergeTagSpecs(awsquery.TagSpecs(r.Form), "snapshot"),
	}

	info, err := h.compute.CreateSnapshot(r.Context(), cfg)
	if err != nil {
		writeSnapshotErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createSnapshotResponseXML{
		Xmlns:       awsquery.Namespace,
		RequestID:   awsquery.RequestID,
		snapshotXML: toSnapshotXML(info),
	})
}

func (h *Handler) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	if err := h.compute.DeleteSnapshot(r.Context(), r.Form.Get("SnapshotId")); err != nil {
		// A snapshot referenced by a registered AMI cannot be deleted until the
		// AMI is deregistered; real EC2 answers InvalidSnapshot.InUse.
		if cerrors.IsFailedPrecondition(err) {
			awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidSnapshot.InUse", err.Error())
			return
		}

		writeSnapshotErr(w, err)

		return
	}

	awsquery.WriteXMLResponse(w, deleteSnapshotResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

//nolint:dupl // per-resource describe+filter pattern; siblings in volume/image
func (h *Handler) describeSnapshots(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "SnapshotId")
	filters := awsquery.Filters(r.Form)

	if err := validateSnapshotFilters(filters); err != nil {
		writeSnapshotErr(w, err)
		return
	}

	snaps, err := h.compute.DescribeSnapshots(r.Context(), ids)
	if err != nil {
		writeSnapshotErr(w, err)
		return
	}

	out := make([]snapshotXML, 0, len(snaps))

	for i := range snaps {
		if snapshotMatchesFilters(&snaps[i], filters) {
			out = append(out, toSnapshotXML(&snaps[i]))
		}
	}

	awsquery.WriteXMLResponse(w, describeSnapshotsResponseXML{
		Xmlns:       awsquery.Namespace,
		RequestID:   awsquery.RequestID,
		SnapshotSet: out,
	})
}

func validateSnapshotFilters(filters []awsquery.Filter) error {
	for _, f := range filters {
		if isStorageTagFilter(f.Name) {
			continue
		}

		switch f.Name {
		case filterSnapshotID, filterVolumeID, filterStatus, filterOwnerID, filterEncrypted, filterDescription:
		default:
			return newInvalidParameterErr("The filter '" + f.Name + "' is invalid")
		}
	}

	return nil
}

func snapshotMatchesFilters(s *computedriver.SnapshotInfo, filters []awsquery.Filter) bool {
	for _, f := range filters {
		if !snapshotMatchesFilter(s, f) {
			return false
		}
	}

	return true
}

func snapshotMatchesFilter(s *computedriver.SnapshotInfo, f awsquery.Filter) bool {
	if matched, isTag := matchStorageTagFilter(s.Tags, f); isTag {
		return matched
	}

	switch f.Name {
	case filterSnapshotID:
		return containsString(f.Values, s.ID)
	case filterVolumeID:
		return containsString(f.Values, s.VolumeID)
	case filterStatus:
		return containsString(f.Values, nonEmpty(s.State, "completed"))
	case filterOwnerID:
		return containsString(f.Values, s.OwnerID)
	case filterEncrypted:
		return containsString(f.Values, boolFilterValue(s.Encrypted))
	case filterDescription:
		return containsString(f.Values, s.Description)
	default:
		return false
	}
}

// modifySnapshotAttribute applies createVolumePermission add/remove grants
// (snapshot sharing). When the compute driver models the attribute it persists
// the grants so DescribeSnapshotAttribute reads them back; otherwise the request
// is acknowledged without persistence.
func (h *Handler) modifySnapshotAttribute(w http.ResponseWriter, r *http.Request) {
	snapshotID := r.Form.Get("SnapshotId")

	if _, err := h.compute.DescribeSnapshots(r.Context(), []string{snapshotID}); err != nil {
		writeSnapshotErr(w, err)
		return
	}

	if modifier, ok := h.compute.(computedriver.SnapshotAttributeModifier); ok {
		if err := applySnapshotPermissionChanges(r, modifier, snapshotID); err != nil {
			writeSnapshotErr(w, err)
			return
		}
	}

	awsquery.WriteXMLResponse(w, modifySnapshotAttributeResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// applySnapshotPermissionChanges reads the createVolumePermission Add/Remove
// modifications from the request and persists each non-empty side. It supports
// both the structured CreateVolumePermission.Add.N.* form the SDK sends and the
// flat OperationType + UserGroup.N / UserId.N form.
func applySnapshotPermissionChanges(
	r *http.Request, modifier computedriver.SnapshotAttributeModifier, snapshotID string,
) error {
	addGroups, addUsers := snapshotPermissionGrants(r, "Add")
	removeGroups, removeUsers := snapshotPermissionGrants(r, "Remove")

	if op := r.Form.Get("OperationType"); op != "" {
		groups := awsquery.ListStrings(r.Form, "UserGroup")
		users := awsquery.ListStrings(r.Form, "UserId")

		if op == permissionOpRemove {
			removeGroups, removeUsers = append(removeGroups, groups...), append(removeUsers, users...)
		} else {
			addGroups, addUsers = append(addGroups, groups...), append(addUsers, users...)
		}
	}

	if len(addGroups) > 0 || len(addUsers) > 0 {
		if err := modifier.ModifySnapshotAttribute(r.Context(), computedriver.ModifySnapshotAttributeInput{
			SnapshotID: snapshotID, OperationType: "add", Groups: addGroups, UserIDs: addUsers,
		}); err != nil {
			return err
		}
	}

	if len(removeGroups) > 0 || len(removeUsers) > 0 {
		return modifier.ModifySnapshotAttribute(r.Context(), computedriver.ModifySnapshotAttributeInput{
			SnapshotID: snapshotID, OperationType: "remove", Groups: removeGroups, UserIDs: removeUsers,
		})
	}

	return nil
}

// snapshotPermissionGrants reads CreateVolumePermission.<side>.N.Group / .UserId.
func snapshotPermissionGrants(r *http.Request, side string) (groups, users []string) {
	for _, i := range awsquery.CollectIndices(r.Form, "CreateVolumePermission."+side) {
		base := "CreateVolumePermission." + side + "." + strconv.Itoa(i)
		if g := r.Form.Get(base + ".Group"); g != "" {
			groups = append(groups, g)
		}

		if u := r.Form.Get(base + ".UserId"); u != "" {
			users = append(users, u)
		}
	}

	return groups, users
}

type createVolumePermissionXML struct {
	Group  string `xml:"group,omitempty"`
	UserID string `xml:"userId,omitempty"`
}

type describeSnapshotAttributeResponseXML struct {
	XMLName                 xml.Name                    `xml:"DescribeSnapshotAttributeResponse"`
	Xmlns                   string                      `xml:"xmlns,attr"`
	RequestID               string                      `xml:"requestId"`
	SnapshotID              string                      `xml:"snapshotId"`
	CreateVolumePermissions []createVolumePermissionXML `xml:"createVolumePermission>item,omitempty"`
}

// describeSnapshotAttribute returns the createVolumePermission grants persisted
// by ModifySnapshotAttribute, completing the snapshot-sharing round-trip.
func (h *Handler) describeSnapshotAttribute(w http.ResponseWriter, r *http.Request) {
	snapshotID := r.Form.Get("SnapshotId")

	if _, err := h.compute.DescribeSnapshots(r.Context(), []string{snapshotID}); err != nil {
		writeSnapshotErr(w, err)
		return
	}

	resp := describeSnapshotAttributeResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		SnapshotID: snapshotID,
	}

	if modifier, ok := h.compute.(computedriver.SnapshotAttributeModifier); ok &&
		r.Form.Get("Attribute") == "createVolumePermission" {
		perms, err := modifier.DescribeSnapshotVolumePermissions(r.Context(), snapshotID)
		if err != nil {
			writeSnapshotErr(w, err)
			return
		}

		for _, p := range perms {
			resp.CreateVolumePermissions = append(resp.CreateVolumePermissions,
				createVolumePermissionXML{Group: p.Group, UserID: p.UserID})
		}
	}

	awsquery.WriteXMLResponse(w, resp)
}

func toSnapshotXML(s *computedriver.SnapshotInfo) snapshotXML {
	state := s.State
	if state == "" {
		state = "completed"
	}

	return snapshotXML{
		SnapshotID:  s.ID,
		VolumeID:    s.VolumeID,
		State:       state,
		StartTime:   s.CreatedAt,
		Progress:    s.Progress,
		OwnerID:     s.OwnerID,
		Description: s.Description,
		VolumeSize:  s.Size,
		Encrypted:   s.Encrypted,
		Tags:        toTagItems(s.Tags),
	}
}

func writeSnapshotErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidSnapshot.NotFound", "IncorrectState")
}
