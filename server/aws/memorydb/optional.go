package memorydb

import (
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	mdbdriver "github.com/stackshy/cloudemu/v2/services/memorydb/driver"
)

const (
	opCreateMRC        = "CreateMultiRegionCluster"
	opDescribeMRCs     = "DescribeMultiRegionClusters"
	opUpdateMRC        = "UpdateMultiRegionCluster"
	opDeleteMRC        = "DeleteMultiRegionCluster"
	opListMRCUpdates   = "ListAllowedMultiRegionClusterUpdates"
	opDescribeMRPGs    = "DescribeMultiRegionParameterGroups"
	opDescribeMRParams = "DescribeMultiRegionParameters"
	opDescribeRNs      = "DescribeReservedNodes"
	opDescribeRNOffers = "DescribeReservedNodesOfferings"
	opPurchaseRNOffer  = "PurchaseReservedNodesOffering"
	opDescribeSvcUpd   = "DescribeServiceUpdates"
	opBatchUpdate      = "BatchUpdateCluster"
)

// serveOptional handles the multi-region-cluster and reserved-node operations,
// which are optional driver capabilities discovered by type assertion.
func (h *Handler) serveOptional(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case opCreateMRC, opDescribeMRCs, opUpdateMRC,
		opDeleteMRC, opListMRCUpdates,
		opDescribeMRPGs, opDescribeMRParams:
		mr, ok := h.db.(mdbdriver.MultiRegion)
		if !ok {
			wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterValueException", "multi-region not supported")
			return true
		}

		h.serveMultiRegion(w, r, op, mr)

		return true
	case opDescribeRNs, opDescribeRNOffers, opPurchaseRNOffer:
		rn, ok := h.db.(mdbdriver.ReservedNodes)
		if !ok {
			wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterValueException", "reserved nodes not supported")
			return true
		}

		h.serveReservedNodes(w, r, op, rn)

		return true
	case opDescribeSvcUpd, opBatchUpdate:
		su, ok := h.db.(mdbdriver.ServiceUpdates)
		if !ok {
			wire.WriteJSONError(w, http.StatusBadRequest, "InvalidParameterValueException", "service updates not supported")
			return true
		}

		h.serveServiceUpdates(w, r, op, su)

		return true
	default:
		return false
	}
}

//nolint:gocyclo,gocognit,funlen // flat op switch over the multi-region operations.
func (*Handler) serveMultiRegion(w http.ResponseWriter, r *http.Request, op string, mr mdbdriver.MultiRegion) {
	switch op {
	case opCreateMRC:
		var in memorydb.CreateMultiRegionClusterInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		c, err := mr.CreateMultiRegionCluster(r.Context(), mdbdriver.CreateMultiRegionClusterConfig{
			NameSuffix: aws.ToString(in.MultiRegionClusterNameSuffix), Description: aws.ToString(in.Description),
			NodeType: aws.ToString(in.NodeType), Engine: aws.ToString(in.Engine), EngineVersion: aws.ToString(in.EngineVersion),
			NumShards: int(aws.ToInt32(in.NumShards)), TLSEnabled: aws.ToBool(in.TLSEnabled),
			MultiRegionParameterGroupName: aws.ToString(in.MultiRegionParameterGroupName), Tags: tagMap(in.Tags),
		})
		if err != nil {
			writeErr(w, "MultiRegionCluster", err)
			return
		}

		wire.WriteJSON(w, memorydb.CreateMultiRegionClusterOutput{MultiRegionCluster: toWireMultiRegion(c)})
	case opDescribeMRCs:
		var in memorydb.DescribeMultiRegionClustersInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		var names []string
		if in.MultiRegionClusterName != nil {
			names = []string{aws.ToString(in.MultiRegionClusterName)}
		}

		clusters, err := mr.DescribeMultiRegionClusters(r.Context(), names)
		if err != nil {
			writeErr(w, "MultiRegionCluster", err)
			return
		}

		page, next, err := paginate(clusters, in.MaxResults, in.NextToken)
		if err != nil {
			writeErr(w, "MultiRegionCluster", err)
			return
		}

		out := memorydb.DescribeMultiRegionClustersOutput{NextToken: next}
		for i := range page {
			out.MultiRegionClusters = append(out.MultiRegionClusters, *toWireMultiRegion(&page[i]))
		}

		wire.WriteJSON(w, out)
	case opUpdateMRC:
		var in memorydb.UpdateMultiRegionClusterInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		var shardCount *int

		if in.ShardConfiguration != nil {
			v := int(in.ShardConfiguration.ShardCount)
			shardCount = &v
		}

		c, err := mr.UpdateMultiRegionCluster(r.Context(),
			aws.ToString(in.MultiRegionClusterName), aws.ToString(in.NodeType), aws.ToString(in.EngineVersion), shardCount)
		if err != nil {
			writeErr(w, "MultiRegionCluster", err)
			return
		}

		wire.WriteJSON(w, memorydb.UpdateMultiRegionClusterOutput{MultiRegionCluster: toWireMultiRegion(c)})
	case opDeleteMRC:
		var in memorydb.DeleteMultiRegionClusterInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		c, err := mr.DeleteMultiRegionCluster(r.Context(), aws.ToString(in.MultiRegionClusterName))
		if err != nil {
			writeErr(w, "MultiRegionCluster", err)
			return
		}

		wire.WriteJSON(w, memorydb.DeleteMultiRegionClusterOutput{MultiRegionCluster: toWireMultiRegion(c)})
	case opListMRCUpdates:
		var in memorydb.ListAllowedMultiRegionClusterUpdatesInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		nodeTypes, err := mr.ListAllowedMultiRegionClusterUpdates(r.Context(), aws.ToString(in.MultiRegionClusterName))
		if err != nil {
			writeErr(w, "MultiRegionCluster", err)
			return
		}

		wire.WriteJSON(w, memorydb.ListAllowedMultiRegionClusterUpdatesOutput{ScaleUpNodeTypes: nodeTypes})
	case opDescribeMRPGs:
		groups, err := mr.DescribeMultiRegionParameterGroups(r.Context(), nil)
		if err != nil {
			writeErr(w, "MultiRegionParameterGroup", err)
			return
		}

		out := memorydb.DescribeMultiRegionParameterGroupsOutput{}
		for _, g := range groups {
			out.MultiRegionParameterGroups = append(out.MultiRegionParameterGroups, mrParamGroupWire(g))
		}

		wire.WriteJSON(w, out)
	case opDescribeMRParams:
		var in memorydb.DescribeMultiRegionParametersInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		params, err := mr.DescribeMultiRegionParameters(r.Context(), aws.ToString(in.MultiRegionParameterGroupName))
		if err != nil {
			writeErr(w, "MultiRegionParameterGroup", err)
			return
		}

		out := memorydb.DescribeMultiRegionParametersOutput{}
		for _, p := range params {
			out.MultiRegionParameters = append(out.MultiRegionParameters, mrParamWire(&p))
		}

		wire.WriteJSON(w, out)
	}
}

//nolint:dupl,gocyclo // per-op decode/paginate/encode blocks are structurally similar by design.
func (*Handler) serveReservedNodes(w http.ResponseWriter, r *http.Request, op string, rn mdbdriver.ReservedNodes) {
	switch op {
	case opDescribeRNs:
		var in memorydb.DescribeReservedNodesInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		nodes, err := rn.DescribeReservedNodes(r.Context())
		if err != nil {
			writeErr(w, "ReservedNode", err)
			return
		}

		page, next, err := paginate(nodes, in.MaxResults, in.NextToken)
		if err != nil {
			writeErr(w, "ReservedNode", err)
			return
		}

		out := memorydb.DescribeReservedNodesOutput{NextToken: next}
		for i := range page {
			out.ReservedNodes = append(out.ReservedNodes, reservedNodeWire(&page[i]))
		}

		wire.WriteJSON(w, out)
	case opDescribeRNOffers:
		var in memorydb.DescribeReservedNodesOfferingsInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		offerings, err := rn.DescribeReservedNodesOfferings(r.Context())
		if err != nil {
			writeErr(w, "ReservedNodesOffering", err)
			return
		}

		page, next, err := paginate(offerings, in.MaxResults, in.NextToken)
		if err != nil {
			writeErr(w, "ReservedNodesOffering", err)
			return
		}

		out := memorydb.DescribeReservedNodesOfferingsOutput{NextToken: next}
		for i := range page {
			out.ReservedNodesOfferings = append(out.ReservedNodesOfferings, offeringWire(&page[i]))
		}

		wire.WriteJSON(w, out)
	case opPurchaseRNOffer:
		var in memorydb.PurchaseReservedNodesOfferingInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		node, err := rn.PurchaseReservedNodesOffering(r.Context(),
			aws.ToString(in.ReservedNodesOfferingId), aws.ToString(in.ReservationId), int(aws.ToInt32(in.NodeCount)))
		if err != nil {
			// A missing offering is a ReservedNodesOffering fault; a duplicate
			// reservation is a ReservedNode fault (the SDK models
			// ReservedNodeAlreadyExistsFault, not the *Offering* variant).
			if cerrors.IsAlreadyExists(err) {
				writeErr(w, "ReservedNode", err)
				return
			}

			writeErr(w, "ReservedNodesOffering", err)

			return
		}

		rnw := reservedNodeWire(node)
		wire.WriteJSON(w, memorydb.PurchaseReservedNodesOfferingOutput{ReservedNode: &rnw})
	}
}

//nolint:gocyclo // a flat op switch over the service-update operations is the clearest shape.
func (*Handler) serveServiceUpdates(w http.ResponseWriter, r *http.Request, op string, su mdbdriver.ServiceUpdates) {
	switch op {
	case opDescribeSvcUpd:
		var in memorydb.DescribeServiceUpdatesInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		status := make([]string, 0, len(in.Status))
		for _, s := range in.Status {
			status = append(status, string(s))
		}

		updates, err := su.DescribeServiceUpdates(r.Context(), aws.ToString(in.ServiceUpdateName), in.ClusterNames, status)
		if err != nil {
			writeErr(w, "ServiceUpdate", err)
			return
		}

		page, next, err := paginate(updates, in.MaxResults, in.NextToken)
		if err != nil {
			writeErr(w, "ServiceUpdate", err)
			return
		}

		out := memorydb.DescribeServiceUpdatesOutput{NextToken: next}
		for i := range page {
			out.ServiceUpdates = append(out.ServiceUpdates, serviceUpdateWire(&page[i]))
		}

		wire.WriteJSON(w, out)
	case opBatchUpdate:
		var in memorydb.BatchUpdateClusterInput
		if !wire.DecodeJSON(w, r, &in) {
			return
		}

		name := ""
		if in.ServiceUpdate != nil {
			name = aws.ToString(in.ServiceUpdate.ServiceUpdateNameToApply)
		}

		processed, unprocessed, err := su.BatchUpdateCluster(r.Context(), in.ClusterNames, name)
		if err != nil {
			writeErr(w, "Cluster", err)
			return
		}

		out := memorydb.BatchUpdateClusterOutput{}
		for i := range processed {
			out.ProcessedClusters = append(out.ProcessedClusters, *toWireCluster(&processed[i]))
		}

		for i := range unprocessed {
			out.UnprocessedClusters = append(out.UnprocessedClusters, unprocessedWire(&unprocessed[i]))
		}

		wire.WriteJSON(w, out)
	}
}
