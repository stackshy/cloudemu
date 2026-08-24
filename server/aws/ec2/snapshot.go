package ec2

import (
	"encoding/xml"
	"net/http"

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

// modifySnapshotAttribute accepts createVolumePermission / productCode changes.
// The emulator does not model snapshot sharing, so it acknowledges the request
// without persisting the attribute (Terraform's aws_snapshot_create_volume_
// permission only needs the 200).
func (h *Handler) modifySnapshotAttribute(w http.ResponseWriter, r *http.Request) {
	if _, err := h.compute.DescribeSnapshots(r.Context(), []string{r.Form.Get("SnapshotId")}); err != nil {
		writeSnapshotErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifySnapshotAttributeResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
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
