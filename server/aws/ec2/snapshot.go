package ec2

import (
	"encoding/xml"
	"net/http"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

type snapshotXML struct {
	SnapshotID  string    `xml:"snapshotId"`
	VolumeID    string    `xml:"volumeId"`
	State       string    `xml:"status"`
	StartTime   string    `xml:"startTime,omitempty"`
	Description string    `xml:"description,omitempty"`
	VolumeSize  int       `xml:"volumeSize"`
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
		writeSnapshotErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteSnapshotResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

//nolint:dupl // per-resource describe pattern; siblings in vpc/subnet/sg/igw/route_table/volume/keypair
func (h *Handler) describeSnapshots(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "SnapshotId")

	snaps, err := h.compute.DescribeSnapshots(r.Context(), ids)
	if err != nil {
		writeSnapshotErr(w, err)
		return
	}

	out := make([]snapshotXML, 0, len(snaps))
	for i := range snaps {
		out = append(out, toSnapshotXML(&snaps[i]))
	}

	awsquery.WriteXMLResponse(w, describeSnapshotsResponseXML{
		Xmlns:       awsquery.Namespace,
		RequestID:   awsquery.RequestID,
		SnapshotSet: out,
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
		Description: s.Description,
		VolumeSize:  s.Size,
		Tags:        toTagItems(s.Tags),
	}
}

func writeSnapshotErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidSnapshot.NotFound", "IncorrectState")
}
