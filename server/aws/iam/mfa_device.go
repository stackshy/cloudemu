package iam

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	iamdriver "github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// mfaDeviceManager is the AWS-specific MFA-device surface. It's not part of the
// portable IAM driver, so the handler type-asserts for it.
type mfaDeviceManager interface {
	CreateVirtualMFADevice(ctx context.Context, name, path string) (*iamdriver.VirtualMFADeviceInfo, error)
	ListMFADevices(ctx context.Context, userName string) ([]iamdriver.MFADeviceInfo, error)
	EnableMFADevice(ctx context.Context, userName, serialNumber, authCode1, authCode2 string) error
	DeactivateMFADevice(ctx context.Context, userName, serialNumber string) error
	DeleteVirtualMFADevice(ctx context.Context, serialNumber string) error
	ListVirtualMFADevices(ctx context.Context, assignmentStatus string) ([]iamdriver.VirtualMFADeviceMetadata, error)
}

// The seed and QR-code payloads are blob fields the SDK base64-decodes on the
// way in, so they are emitted as base64 text rather than relying on the XML
// encoder's byte handling.
type virtualMFADeviceXML struct {
	SerialNumber     string `xml:"SerialNumber"`
	Base32StringSeed string `xml:"Base32StringSeed,omitempty"`
	QRCodePNG        string `xml:"QRCodePNG,omitempty"`
}

type createVirtualMFADeviceResponse struct {
	XMLName  xml.Name                     `xml:"CreateVirtualMFADeviceResponse"`
	Xmlns    string                       `xml:"xmlns,attr"`
	Result   createVirtualMFADeviceResult `xml:"CreateVirtualMFADeviceResult"`
	Metadata responseMetadata             `xml:"ResponseMetadata"`
}

type createVirtualMFADeviceResult struct {
	VirtualMFADevice virtualMFADeviceXML `xml:"VirtualMFADevice"`
}

type mfaDeviceXML struct {
	UserName     string `xml:"UserName"`
	SerialNumber string `xml:"SerialNumber"`
	EnableDate   string `xml:"EnableDate,omitempty"`
}

type mfaDevicesListXML struct {
	Member []mfaDeviceXML `xml:"member,omitempty"`
}

type listMFADevicesResponse struct {
	XMLName  xml.Name             `xml:"ListMFADevicesResponse"`
	Xmlns    string               `xml:"xmlns,attr"`
	Result   listMFADevicesResult `xml:"ListMFADevicesResult"`
	Metadata responseMetadata     `xml:"ResponseMetadata"`
}

type listMFADevicesResult struct {
	MFADevices  mfaDevicesListXML `xml:"MFADevices"`
	IsTruncated bool              `xml:"IsTruncated"`
}

func (h *Handler) createVirtualMFADevice(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.iam.(mfaDeviceManager)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "MFA devices not supported")
		return
	}

	d, err := mgr.CreateVirtualMFADevice(r.Context(), r.Form.Get("VirtualMFADeviceName"), r.Form.Get("Path"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createVirtualMFADeviceResponse{
		Xmlns: Namespace,
		Result: createVirtualMFADeviceResult{VirtualMFADevice: virtualMFADeviceXML{
			SerialNumber:     d.SerialNumber,
			Base32StringSeed: base64.StdEncoding.EncodeToString(d.Base32StringSeed),
			QRCodePNG:        base64.StdEncoding.EncodeToString(d.QRCodePNG),
		}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listMFADevices(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.iam.(mfaDeviceManager)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "MFA devices not supported")
		return
	}

	devices, err := mgr.ListMFADevices(r.Context(), r.Form.Get("UserName"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := mfaDevicesListXML{Member: make([]mfaDeviceXML, 0, len(devices))}
	for i := range devices {
		out.Member = append(out.Member, mfaDeviceXML{
			UserName:     devices[i].UserName,
			SerialNumber: devices[i].SerialNumber,
			EnableDate:   devices[i].EnableDate,
		})
	}

	awsquery.WriteXMLResponse(w, listMFADevicesResponse{
		Xmlns:    Namespace,
		Result:   listMFADevicesResult{MFADevices: out, IsTruncated: false},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

type enableMFADeviceResponse struct {
	XMLName  xml.Name         `xml:"EnableMFADeviceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) enableMFADevice(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.iam.(mfaDeviceManager)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "MFA devices not supported")
		return
	}

	err := mgr.EnableMFADevice(r.Context(),
		r.Form.Get("UserName"), r.Form.Get("SerialNumber"), r.Form.Get("AuthenticationCode1"), r.Form.Get("AuthenticationCode2"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, enableMFADeviceResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

type deactivateMFADeviceResponse struct {
	XMLName  xml.Name         `xml:"DeactivateMFADeviceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) deactivateMFADevice(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.iam.(mfaDeviceManager)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "MFA devices not supported")
		return
	}

	if err := mgr.DeactivateMFADevice(r.Context(), r.Form.Get("UserName"), r.Form.Get("SerialNumber")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deactivateMFADeviceResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

type deleteVirtualMFADeviceResponse struct {
	XMLName  xml.Name         `xml:"DeleteVirtualMFADeviceResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) deleteVirtualMFADevice(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.iam.(mfaDeviceManager)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "MFA devices not supported")
		return
	}

	if err := mgr.DeleteVirtualMFADevice(r.Context(), r.Form.Get("SerialNumber")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteVirtualMFADeviceResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// virtualMFADeviceMetadataXML is the list-shape AWS uses for
// ListVirtualMFADevices: unlike virtualMFADeviceXML it carries no seed/QR
// payload and instead reports the assigned user (omitted for an unassigned
// device).
type virtualMFADeviceMetadataXML struct {
	SerialNumber string   `xml:"SerialNumber"`
	EnableDate   string   `xml:"EnableDate,omitempty"`
	User         *userXML `xml:"User,omitempty"`
}

type virtualMFADevicesListXML struct {
	Member []virtualMFADeviceMetadataXML `xml:"member,omitempty"`
}

type listVirtualMFADevicesResponse struct {
	XMLName  xml.Name                    `xml:"ListVirtualMFADevicesResponse"`
	Xmlns    string                      `xml:"xmlns,attr"`
	Result   listVirtualMFADevicesResult `xml:"ListVirtualMFADevicesResult"`
	Metadata responseMetadata            `xml:"ResponseMetadata"`
}

type listVirtualMFADevicesResult struct {
	VirtualMFADevices virtualMFADevicesListXML `xml:"VirtualMFADevices"`
	IsTruncated       bool                     `xml:"IsTruncated"`
}

func (h *Handler) listVirtualMFADevices(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.iam.(mfaDeviceManager)
	if !ok {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction", "MFA devices not supported")
		return
	}

	devices, err := mgr.ListVirtualMFADevices(r.Context(), r.Form.Get("AssignmentStatus"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := virtualMFADevicesListXML{Member: make([]virtualMFADeviceMetadataXML, 0, len(devices))}

	for i := range devices {
		meta := virtualMFADeviceMetadataXML{SerialNumber: devices[i].SerialNumber, EnableDate: devices[i].EnableDate}

		if devices[i].AssignedUser != nil {
			u := toUserXML(devices[i].AssignedUser)
			meta.User = &u
		}

		out.Member = append(out.Member, meta)
	}

	awsquery.WriteXMLResponse(w, listVirtualMFADevicesResponse{
		Xmlns:    Namespace,
		Result:   listVirtualMFADevicesResult{VirtualMFADevices: out, IsTruncated: false},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}
