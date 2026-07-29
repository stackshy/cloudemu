package rds

import (
	"encoding/xml"
	"net/http"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

type copyDBSnapshotResponse struct {
	XMLName  xml.Name         `xml:"CopyDBSnapshotResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbSnapshotResult `xml:"CopyDBSnapshotResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type copyDBClusterSnapshotResponse struct {
	XMLName  xml.Name                `xml:"CopyDBClusterSnapshotResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   dbClusterSnapshotResult `xml:"CopyDBClusterSnapshotResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type restoreDBInstanceToPointInTimeResponse struct {
	XMLName  xml.Name         `xml:"RestoreDBInstanceToPointInTimeResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbInstanceResult `xml:"RestoreDBInstanceToPointInTimeResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type restoreDBClusterToPointInTimeResponse struct {
	XMLName  xml.Name         `xml:"RestoreDBClusterToPointInTimeResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbClusterResult  `xml:"RestoreDBClusterToPointInTimeResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) advancedRestoreCap() (rdsdriver.AdvancedRestore, bool) {
	ar, ok := h.db.(rdsdriver.AdvancedRestore)

	return ar, ok
}

// parseRestoreTime reads the optional RFC3339 RestoreTime; a bad/absent value
// yields the zero time, which the emulator treats as "no specific point".
func parseRestoreTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}

	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}

	return t
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) copyDBSnapshot(w http.ResponseWriter, r *http.Request) {
	store, ok := h.advancedRestoreCap()
	if !ok {
		writeUnsupported(w, "snapshot copy")
		return
	}

	snap, err := store.CopyDBSnapshot(r.Context(),
		r.Form.Get("SourceDBSnapshotIdentifier"),
		r.Form.Get("TargetDBSnapshotIdentifier"),
		parseRDSTags(r.Form))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, copyDBSnapshotResponse{
		Xmlns:    Namespace,
		Result:   dbSnapshotResult{DBSnapshot: toSnapshotXML(snap)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // structurally mirrors its sibling per-resource block by design.
func (h *Handler) copyDBClusterSnapshot(w http.ResponseWriter, r *http.Request) {
	store, ok := h.advancedRestoreCap()
	if !ok {
		writeUnsupported(w, "snapshot copy")
		return
	}

	snap, err := store.CopyDBClusterSnapshot(r.Context(),
		r.Form.Get("SourceDBClusterSnapshotIdentifier"),
		r.Form.Get("TargetDBClusterSnapshotIdentifier"),
		parseRDSTags(r.Form))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, copyDBClusterSnapshotResponse{
		Xmlns:    Namespace,
		Result:   dbClusterSnapshotResult{DBClusterSnapshot: toClusterSnapshotXML(snap)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) restoreDBInstanceToPointInTime(w http.ResponseWriter, r *http.Request) {
	store, ok := h.advancedRestoreCap()
	if !ok {
		writeUnsupported(w, "point-in-time restore")
		return
	}

	inst, err := store.RestoreDBInstanceToPointInTime(r.Context(), rdsdriver.RestoreInstanceToPointInTimeInput{
		SourceInstanceID:        r.Form.Get("SourceDBInstanceIdentifier"),
		TargetInstanceID:        r.Form.Get("TargetDBInstanceIdentifier"),
		InstanceClass:           r.Form.Get("DBInstanceClass"),
		UseLatestRestorableTime: formBool(r.Form.Get("UseLatestRestorableTime")),
		RestoreTime:             parseRestoreTime(r.Form.Get("RestoreTime")),
		Tags:                    parseRDSTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, restoreDBInstanceToPointInTimeResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(inst)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) restoreDBClusterToPointInTime(w http.ResponseWriter, r *http.Request) {
	store, ok := h.advancedRestoreCap()
	if !ok {
		writeUnsupported(w, "point-in-time restore")
		return
	}

	cluster, err := store.RestoreDBClusterToPointInTime(r.Context(), rdsdriver.RestoreClusterToPointInTimeInput{
		SourceClusterID:         r.Form.Get("SourceDBClusterIdentifier"),
		TargetClusterID:         r.Form.Get("DBClusterIdentifier"),
		UseLatestRestorableTime: formBool(r.Form.Get("UseLatestRestorableTime")),
		RestoreTime:             parseRestoreTime(r.Form.Get("RestoreTime")),
		Tags:                    parseRDSTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, restoreDBClusterToPointInTimeResponse{
		Xmlns:    Namespace,
		Result:   dbClusterResult{DBCluster: toClusterXML(cluster)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
