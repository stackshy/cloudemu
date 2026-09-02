package servicequotas

import (
	"context"
	"net/http"

	"github.com/stackshy/cloudemu/v2/features/quota"
)

func (h *Handler) getServiceQuota(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, (*Handler).doGetServiceQuota)
}

func (h *Handler) doGetServiceQuota(_ context.Context, in *getServiceQuotaInput) (any, error) {
	q, err := h.reg.Get(in.ServiceCode, in.QuotaCode)
	if err != nil {
		return nil, err
	}

	return &getServiceQuotaOutput{Quota: h.toWire(&q)}, nil
}

func (h *Handler) getDefaultServiceQuota(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, (*Handler).doGetDefaultServiceQuota)
}

func (h *Handler) doGetDefaultServiceQuota(_ context.Context, in *getServiceQuotaInput) (any, error) {
	q, err := h.reg.Default(in.ServiceCode, in.QuotaCode)
	if err != nil {
		return nil, err
	}

	return &getDefaultOutput{Quota: h.toWire(&q)}, nil
}

func (h *Handler) listServiceQuotas(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, (*Handler).doListServiceQuotas)
}

func (h *Handler) doListServiceQuotas(_ context.Context, in *listServiceQuotasInput) (any, error) {
	quotas := h.reg.List(in.ServiceCode)

	out := &listServiceQuotasOutput{Quotas: make([]serviceQuota, 0, len(quotas))}
	for i := range quotas {
		out.Quotas = append(out.Quotas, h.toWire(&quotas[i]))
	}

	return out, nil
}

func (h *Handler) listDefaultServiceQuotas(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, (*Handler).doListDefaultServiceQuotas)
}

func (h *Handler) doListDefaultServiceQuotas(_ context.Context, in *listServiceQuotasInput) (any, error) {
	quotas := h.reg.ListDefaults(in.ServiceCode)

	out := &listServiceQuotasOutput{Quotas: make([]serviceQuota, 0, len(quotas))}
	for i := range quotas {
		out.Quotas = append(out.Quotas, h.toWire(&quotas[i]))
	}

	return out, nil
}

func (h *Handler) requestIncrease(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, (*Handler).doRequestIncrease)
}

func (h *Handler) doRequestIncrease(_ context.Context, in *requestIncreaseInput) (any, error) {
	cr, err := h.reg.RequestIncrease(in.ServiceCode, in.QuotaCode, in.DesiredValue)
	if err != nil {
		return nil, err
	}

	return &requestIncreaseOutput{RequestedQuota: h.toWireRequest(&cr)}, nil
}

func (h *Handler) listHistory(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, (*Handler).doListHistory)
}

func (h *Handler) doListHistory(_ context.Context, in *listHistoryInput) (any, error) {
	reqs := h.reg.History(in.ServiceCode, in.QuotaCode)

	out := &listHistoryOutput{RequestedQuotas: make([]requestedQuota, 0, len(reqs))}
	for i := range reqs {
		out.RequestedQuotas = append(out.RequestedQuotas, h.toWireRequest(&reqs[i]))
	}

	return out, nil
}

// quotaArn builds the Service Quotas ARN for a quota, e.g.
// arn:aws:servicequotas:us-east-1:000000000000:ec2/L-1216C47A.
func (h *Handler) quotaArn(serviceCode, quotaCode string) string {
	return "arn:aws:servicequotas:" + h.region + ":" + h.accountID + ":" + serviceCode + "/" + quotaCode
}

func (h *Handler) toWire(q *quota.Quota) serviceQuota {
	return serviceQuota{
		ServiceCode: q.ServiceCode,
		ServiceName: q.ServiceName,
		QuotaArn:    h.quotaArn(q.ServiceCode, q.QuotaCode),
		QuotaCode:   q.QuotaCode,
		QuotaName:   q.QuotaName,
		Value:       q.Value,
		Unit:        q.Unit,
		Adjustable:  q.Adjustable,
		GlobalQuota: q.GlobalQuota,
	}
}

func (h *Handler) toWireRequest(cr *quota.ChangeRequest) requestedQuota {
	return requestedQuota{
		ID:           cr.ID,
		ServiceCode:  cr.ServiceCode,
		ServiceName:  cr.ServiceName,
		QuotaArn:     h.quotaArn(cr.ServiceCode, cr.QuotaCode),
		QuotaCode:    cr.QuotaCode,
		QuotaName:    cr.QuotaName,
		DesiredValue: cr.DesiredValue,
		Status:       cr.Status,
		Unit:         cr.Unit,
		GlobalQuota:  cr.GlobalQuota,
		Created:      float64(cr.Created.Unix()),
		LastUpdated:  float64(cr.LastUpdated.Unix()),
	}
}
