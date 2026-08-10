package rds

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

type createDBInstanceReadReplicaResponse struct {
	XMLName  xml.Name         `xml:"CreateDBInstanceReadReplicaResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbInstanceResult `xml:"CreateDBInstanceReadReplicaResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type promoteReadReplicaResponse struct {
	XMLName  xml.Name         `xml:"PromoteReadReplicaResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   dbInstanceResult `xml:"PromoteReadReplicaResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) readReplicasCap() (rdsdriver.ReadReplicas, bool) {
	rr, ok := h.db.(rdsdriver.ReadReplicas)

	return rr, ok
}

func (h *Handler) createDBInstanceReadReplica(w http.ResponseWriter, r *http.Request) {
	store, ok := h.readReplicasCap()
	if !ok {
		writeUnsupported(w, "read replicas")
		return
	}

	inst, err := store.CreateDBInstanceReadReplica(r.Context(), rdsdriver.ReadReplicaConfig{
		ID:                 r.Form.Get("DBInstanceIdentifier"),
		SourceInstanceID:   r.Form.Get("SourceDBInstanceIdentifier"),
		InstanceClass:      r.Form.Get("DBInstanceClass"),
		AvailabilityZone:   r.Form.Get("AvailabilityZone"),
		Port:               formInt(r.Form.Get("Port")),
		PubliclyAccessible: formBool(r.Form.Get("PubliclyAccessible")),
		Tags:               parseRDSTags(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createDBInstanceReadReplicaResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(inst)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) promoteReadReplica(w http.ResponseWriter, r *http.Request) {
	store, ok := h.readReplicasCap()
	if !ok {
		writeUnsupported(w, "read replicas")
		return
	}

	inst, err := store.PromoteReadReplica(r.Context(), r.Form.Get("DBInstanceIdentifier"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, promoteReadReplicaResponse{
		Xmlns:    Namespace,
		Result:   dbInstanceResult{DBInstance: toInstanceXML(inst)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
