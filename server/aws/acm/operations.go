package acm

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/internal/pagination"
	acmdriver "github.com/stackshy/cloudemu/v2/services/acm/driver"
)

func ctPref(o *certOptionsJSON) string {
	if o == nil {
		return ""
	}

	return o.CertificateTransparencyLoggingPreference
}

func (h *Handler) requestCertificate(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *requestCertificateRequest) (any, error) {
		arn, err := h.acm.RequestCertificate(ctx, acmdriver.RequestCertificateInput{
			DomainName:              req.DomainName,
			SubjectAlternativeNames: req.SubjectAlternativeNames,
			ValidationMethod:        req.ValidationMethod,
			DomainValidationOptions: req.validationOptions(),
			KeyAlgorithm:            req.KeyAlgorithm,
			IdempotencyToken:        req.IdempotencyToken,
			CTLoggingPreference:     ctPref(req.Options),
			Tags:                    tagsToMap(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return requestCertificateResponse{CertificateArn: arn}, nil
	})
}

func (h *Handler) importCertificate(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *importCertificateRequest) (any, error) {
		arn, err := h.acm.ImportCertificate(ctx, acmdriver.ImportCertificateInput{
			ARN:            req.CertificateArn,
			CertificatePEM: string(req.Certificate),
			PrivateKeyPEM:  string(req.PrivateKey),
			ChainPEM:       string(req.CertificateChain),
			Tags:           tagsToMap(req.Tags),
		})
		if err != nil {
			return nil, err
		}

		return requestCertificateResponse{CertificateArn: arn}, nil
	})
}

func (h *Handler) describeCertificate(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *certArnRequest) (any, error) {
		c, err := h.acm.DescribeCertificate(ctx, req.CertificateArn)
		if err != nil {
			return nil, err
		}

		return describeCertificateResponse{Certificate: certToWire(c)}, nil
	})
}

func (h *Handler) listCertificates(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listCertificatesRequest) (any, error) {
		certs, err := h.acm.ListCertificates(ctx,
			acmdriver.ListFilter{Statuses: req.CertificateStatuses, KeyTypes: req.keyTypes()})
		if err != nil {
			return nil, err
		}

		// Paginate over a stable ARN ordering so MaxItems/NextToken tokens stay
		// meaningful across calls (the driver's map iteration is unordered).
		limit := 0
		if req.MaxItems != nil {
			limit = int(*req.MaxItems)
		}

		page, err := pagination.PaginateSorted(certs,
			func(a, b acmdriver.Certificate) bool { return a.ARN < b.ARN }, req.NextToken, limit)
		if err != nil {
			return nil, err
		}

		return listCertificatesResponse{
			CertificateSummaryList: summaries(page.Items),
			NextToken:              page.NextPageToken,
		}, nil
	})
}

func summaries(certs []acmdriver.Certificate) []certSummaryJSON {
	out := make([]certSummaryJSON, 0, len(certs))
	for i := range certs {
		out = append(out, certToSummary(&certs[i]))
	}

	return out
}

func (h *Handler) deleteCertificate(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *certArnRequest) (any, error) {
		if err := h.acm.DeleteCertificate(ctx, req.CertificateArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) getCertificate(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *certArnRequest) (any, error) {
		cert, chain, err := h.acm.GetCertificate(ctx, req.CertificateArn)
		if err != nil {
			return nil, err
		}

		return getCertificateResponse{Certificate: cert, CertificateChain: chain}, nil
	})
}

func (h *Handler) exportCertificate(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *exportCertificateRequest) (any, error) {
		cert, chain, key, err := h.acm.ExportCertificate(ctx, req.CertificateArn, req.Passphrase)
		if err != nil {
			return nil, err
		}

		return exportCertificateResponse{Certificate: cert, CertificateChain: chain, PrivateKey: key}, nil
	})
}

func (h *Handler) renewCertificate(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *certArnRequest) (any, error) {
		if err := h.acm.RenewCertificate(ctx, req.CertificateArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) resendValidationEmail(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *certArnRequest) (any, error) {
		if err := h.acm.ResendValidationEmail(ctx, req.CertificateArn); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) updateCertificateOptions(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *updateOptionsRequest) (any, error) {
		if err := h.acm.UpdateCertificateOptions(ctx, req.CertificateArn,
			req.Options.CertificateTransparencyLoggingPreference); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) revokeCertificate(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *revokeCertificateRequest) (any, error) {
		arn, err := h.acm.RevokeCertificate(ctx, req.CertificateArn, req.RevocationReason)
		if err != nil {
			return nil, err
		}

		return revokeCertificateResponse{CertificateArn: arn}, nil
	})
}

func (h *Handler) searchCertificates(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *listCertificatesRequest) (any, error) {
		certs, err := h.acm.SearchCertificates(ctx,
			acmdriver.ListFilter{Statuses: req.CertificateStatuses, KeyTypes: req.keyTypes()})
		if err != nil {
			return nil, err
		}

		return listCertificatesResponse{CertificateSummaryList: summaries(certs)}, nil
	})
}

func (h *Handler) addTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *addTagsRequest) (any, error) {
		if err := h.acm.AddTagsToCertificate(ctx, req.CertificateArn, tagsToMap(req.Tags)); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) removeTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *removeTagsRequest) (any, error) {
		keys := make([]string, 0, len(req.Tags))
		for _, t := range req.Tags {
			keys = append(keys, t.Key)
		}

		if err := h.acm.RemoveTagsFromCertificate(ctx, req.CertificateArn, keys); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *certArnRequest) (any, error) {
		tags, err := h.acm.ListTagsForCertificate(ctx, req.CertificateArn)
		if err != nil {
			return nil, err
		}

		return listTagsResponse{Tags: mapToTags(tags)}, nil
	})
}

func (h *Handler) getAccountConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, _ *struct{}) (any, error) {
		cfg, err := h.acm.GetAccountConfiguration(ctx)
		if err != nil {
			return nil, err
		}

		return getAccountConfigResponse{ExpiryEvents: &expiryEventsJSON{DaysBeforeExpiry: cfg.DaysBeforeExpiry}}, nil
	})
}

func (h *Handler) putAccountConfiguration(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, ctx context.Context, req *putAccountConfigRequest) (any, error) {
		cfg := acmdriver.AccountConfiguration{}
		if req.ExpiryEvents != nil {
			cfg.DaysBeforeExpiry = req.ExpiryEvents.DaysBeforeExpiry
		}

		if err := h.acm.PutAccountConfiguration(ctx, cfg); err != nil {
			return nil, err
		}

		return struct{}{}, nil
	})
}
