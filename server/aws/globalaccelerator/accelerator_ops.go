package globalaccelerator

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// registerAcceleratorRoutes wires the accelerator and accelerator-attribute
// operations.
func (h *Handler) registerAcceleratorRoutes() {
	h.routes["CreateAccelerator"] = h.createAccelerator
	h.routes["DescribeAccelerator"] = h.describeAccelerator
	h.routes["UpdateAccelerator"] = h.updateAccelerator
	h.routes["DeleteAccelerator"] = h.deleteAccelerator
	h.routes["ListAccelerators"] = h.listAccelerators
	h.routes["DescribeAcceleratorAttributes"] = h.describeAcceleratorAttributes
	h.routes["UpdateAcceleratorAttributes"] = h.updateAcceleratorAttributes
}

type createAcceleratorRequest struct {
	Name             string    `json:"Name"`
	IPAddressType    string    `json:"IpAddressType"`
	IPAddresses      []string  `json:"IpAddresses"`
	Enabled          *bool     `json:"Enabled"`
	IdempotencyToken string    `json:"IdempotencyToken"`
	Tags             []tagJSON `json:"Tags"`
}

type acceleratorResponse struct {
	Accelerator acceleratorJSON `json:"Accelerator"`
}

func (h *Handler) createAccelerator(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *createAcceleratorRequest) (any, error) {
		a, err := h.ga.CreateAccelerator(ctx, &driver.CreateAcceleratorInput{
			Name:             req.Name,
			IPAddressType:    req.IPAddressType,
			IPAddresses:      req.IPAddresses,
			Enabled:          req.Enabled,
			IdempotencyToken: req.IdempotencyToken,
			Tags:             tagsFromWire(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return acceleratorResponse{Accelerator: acceleratorToWire(a)}, nil
	})
}

type describeAcceleratorRequest struct {
	AcceleratorArn string `json:"AcceleratorArn"`
}

func (h *Handler) describeAccelerator(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeAcceleratorRequest) (any, error) {
		a, err := h.ga.DescribeAccelerator(ctx, req.AcceleratorArn)
		if err != nil {
			return nil, err
		}

		return acceleratorResponse{Accelerator: acceleratorToWire(a)}, nil
	})
}

type updateAcceleratorRequest struct {
	AcceleratorArn string   `json:"AcceleratorArn"`
	Name           *string  `json:"Name"`
	IPAddressType  *string  `json:"IpAddressType"`
	IPAddresses    []string `json:"IpAddresses"`
	Enabled        *bool    `json:"Enabled"`
}

func (h *Handler) updateAccelerator(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateAcceleratorRequest) (any, error) {
		a, err := h.ga.UpdateAccelerator(ctx, &driver.UpdateAcceleratorInput{
			AcceleratorArn: req.AcceleratorArn,
			Name:           req.Name,
			IPAddressType:  req.IPAddressType,
			IPAddresses:    req.IPAddresses,
			Enabled:        req.Enabled,
		})
		if err != nil {
			return nil, err
		}

		return acceleratorResponse{Accelerator: acceleratorToWire(a)}, nil
	})
}

type deleteAcceleratorRequest struct {
	AcceleratorArn string `json:"AcceleratorArn"`
}

func (h *Handler) deleteAccelerator(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *deleteAcceleratorRequest) (any, error) {
		if err := h.ga.DeleteAccelerator(ctx, req.AcceleratorArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

type listAcceleratorsRequest struct {
	MaxResults int32  `json:"MaxResults"`
	NextToken  string `json:"NextToken"`
}

type listAcceleratorsResponse struct {
	Accelerators []acceleratorJSON `json:"Accelerators"`
	NextToken    string            `json:"NextToken,omitempty"`
}

func (h *Handler) listAccelerators(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listAcceleratorsRequest) (any, error) {
		accs, next, err := h.ga.ListAccelerators(ctx, driver.Page{NextToken: req.NextToken, MaxResults: req.MaxResults})
		if err != nil {
			return nil, err
		}

		return listAcceleratorsResponse{
			Accelerators: toWireList(accs, acceleratorToWire),
			NextToken:    next,
		}, nil
	})
}

type describeAcceleratorAttributesRequest struct {
	AcceleratorArn string `json:"AcceleratorArn"`
}

type acceleratorAttributesResponse struct {
	AcceleratorAttributes acceleratorAttributesJSON `json:"AcceleratorAttributes"`
}

func (h *Handler) describeAcceleratorAttributes(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *describeAcceleratorAttributesRequest) (any, error) {
		attr, err := h.ga.DescribeAcceleratorAttributes(ctx, req.AcceleratorArn)
		if err != nil {
			return nil, err
		}

		return acceleratorAttributesResponse{AcceleratorAttributes: attributesToWire(attr)}, nil
	})
}

type updateAcceleratorAttributesRequest struct {
	AcceleratorArn   string  `json:"AcceleratorArn"`
	FlowLogsEnabled  *bool   `json:"FlowLogsEnabled"`
	FlowLogsS3Bucket *string `json:"FlowLogsS3Bucket"`
	FlowLogsS3Prefix *string `json:"FlowLogsS3Prefix"`
}

func (h *Handler) updateAcceleratorAttributes(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateAcceleratorAttributesRequest) (any, error) {
		attr, err := h.ga.UpdateAcceleratorAttributes(ctx, &driver.UpdateAcceleratorAttributesInput{
			AcceleratorArn:   req.AcceleratorArn,
			FlowLogsEnabled:  req.FlowLogsEnabled,
			FlowLogsS3Bucket: req.FlowLogsS3Bucket,
			FlowLogsS3Prefix: req.FlowLogsS3Prefix,
		})
		if err != nil {
			return nil, err
		}

		return acceleratorAttributesResponse{AcceleratorAttributes: attributesToWire(attr)}, nil
	})
}
