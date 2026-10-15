package apigateway

import (
	"net/http"
	"net/url"

	"github.com/stackshy/cloudemu/v2/services/apigateway/driver"
)

// serveDomainNames handles /domainnames, /domainnames/{name},
// /domainnames/{name}/basepathmappings and .../basepathmappings/{basePath}.
//
//nolint:dupl // the same item/collection router shape as the sibling resource families by design
func (h *Handler) serveDomainNames(w http.ResponseWriter, r *http.Request, rest []string) {
	svc, ok := h.ag.(driver.DomainNames)
	if !ok {
		notImplemented(w)
		return
	}

	const (
		domainOnly = 1
		mappings   = 2
		mappingOne = 3
	)

	switch len(rest) {
	case 0:
		h.serveDomainCollection(w, r, svc)
	case domainOnly:
		name := rest[0]

		serveItem(w, r,
			func(ops []driver.PatchOperation) (*driver.DomainName, error) {
				return svc.UpdateDomainName(r.Context(), name, ops)
			},
			func() (*driver.DomainName, error) { return svc.GetDomainName(r.Context(), name) },
			func() error { return svc.DeleteDomainName(r.Context(), name) },
			toDomainNameResponse,
		)
	case mappings:
		h.serveMappingCollection(w, r, svc, rest[0], rest[1])
	case mappingOne:
		h.serveMappingItem(w, r, svc, rest)
	default:
		notImplemented(w)
	}
}

func (*Handler) serveDomainCollection(w http.ResponseWriter, r *http.Request, svc driver.DomainNames) {
	switch r.Method {
	case http.MethodGet:
		serveListOf(w, r, func(page driver.PageInput) ([]driver.DomainName, string, error) {
			res, err := svc.GetDomainNames(r.Context(), page)
			if err != nil {
				return nil, "", err
			}

			return res.Items, res.Position, nil
		}, toDomainNameResponse)
	case http.MethodPost:
		serveCreateOf(w, r, func(req *domainNameRequest) (*driver.DomainName, error) {
			in := &driver.CreateDomainNameInput{
				DomainName: req.DomainName, CertificateName: req.CertificateName, CertificateARN: req.CertificateARN,
				RegionalCertificateName: req.RegionalCertificateName, RegionalCertificateARN: req.RegionalCertificateARN,
				SecurityPolicy: req.SecurityPolicy, Tags: req.Tags,
			}
			if req.EndpointConfiguration != nil {
				in.EndpointConfigurationType = req.EndpointConfiguration.Types
			}

			return svc.CreateDomainName(r.Context(), in)
		}, toDomainNameResponse)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) serveMappingCollection(w http.ResponseWriter, r *http.Request, svc driver.DomainNames, domain, sub string) {
	if sub != segBasePathMaps {
		notImplemented(w)
		return
	}

	switch r.Method {
	case http.MethodGet:
		serveListOf(w, r, func(page driver.PageInput) ([]driver.BasePathMapping, string, error) {
			res, err := svc.GetBasePathMappings(r.Context(), domain, page)
			if err != nil {
				return nil, "", err
			}

			return res.Items, res.Position, nil
		}, toBasePathMappingResponse)
	case http.MethodPost:
		serveCreateOf(w, r, func(req *basePathMappingRequest) (*driver.BasePathMapping, error) {
			return svc.CreateBasePathMapping(r.Context(), domain, driver.BasePathMapping{
				BasePath: req.BasePath, RestAPIID: req.RestAPIID, Stage: req.Stage,
			})
		}, toBasePathMappingResponse)
	default:
		writeMethodNotAllowed(w)
	}
}

func (*Handler) serveMappingItem(w http.ResponseWriter, r *http.Request, svc driver.DomainNames, rest []string) {
	if rest[1] != segBasePathMaps {
		notImplemented(w)
		return
	}

	domain := rest[0]

	basePath, err := url.PathUnescape(rest[2])
	if err != nil {
		basePath = rest[2]
	}

	serveItem(w, r,
		func(ops []driver.PatchOperation) (*driver.BasePathMapping, error) {
			return svc.UpdateBasePathMapping(r.Context(), domain, basePath, ops)
		},
		func() (*driver.BasePathMapping, error) { return svc.GetBasePathMapping(r.Context(), domain, basePath) },
		func() error { return svc.DeleteBasePathMapping(r.Context(), domain, basePath) },
		toBasePathMappingResponse,
	)
}

// serveVpcLinks handles /vpclinks and /vpclinks/{id}.
func (h *Handler) serveVpcLinks(w http.ResponseWriter, r *http.Request, rest []string) {
	svc, ok := h.ag.(driver.VpcLinks)
	if !ok || len(rest) > 1 {
		notImplemented(w)
		return
	}

	if len(rest) == 1 {
		id := rest[0]

		serveItem(w, r,
			func(ops []driver.PatchOperation) (*driver.VpcLink, error) {
				return svc.UpdateVpcLink(r.Context(), id, ops)
			},
			func() (*driver.VpcLink, error) { return svc.GetVpcLink(r.Context(), id) },
			func() error { return svc.DeleteVpcLink(r.Context(), id) },
			toVpcLinkResponse,
		)

		return
	}

	switch r.Method {
	case http.MethodGet:
		serveListOf(w, r, func(page driver.PageInput) ([]driver.VpcLink, string, error) {
			res, err := svc.GetVpcLinks(r.Context(), page)
			if err != nil {
				return nil, "", err
			}

			return res.Items, res.Position, nil
		}, toVpcLinkResponse)
	case http.MethodPost:
		serveCreateOf(w, r, func(req *vpcLinkRequest) (*driver.VpcLink, error) {
			return svc.CreateVpcLink(r.Context(), &driver.CreateVpcLinkInput{
				Name: req.Name, Description: req.Description, TargetARNs: req.TargetARNs, Tags: req.Tags,
			})
		}, toVpcLinkResponse)
	default:
		writeMethodNotAllowed(w)
	}
}
