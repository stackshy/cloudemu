package lambda

import (
	"context"
	"net/http"
	"strings"

	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// functionURLManager is the AWS-specific Lambda Function URL surface. Function
// URLs aren't part of the portable Serverless driver, so the handler
// type-asserts for it rather than requiring every cloud to implement it.
type functionURLManager interface {
	CreateFunctionURLConfig(ctx context.Context, cfg sdrv.FunctionURLConfig) (*sdrv.FunctionURLConfig, error)
	GetFunctionURLConfig(ctx context.Context, functionName string) (*sdrv.FunctionURLConfig, error)
	UpdateFunctionURLConfig(ctx context.Context, cfg sdrv.FunctionURLConfig) (*sdrv.FunctionURLConfig, error)
	DeleteFunctionURLConfig(ctx context.Context, functionName string) error
	ListFunctionURLConfigs(ctx context.Context, functionName string) ([]sdrv.FunctionURLConfig, error)
}

// corsJSON is the AWS Cors shape shared by the Function URL request and response.
type corsJSON struct {
	AllowCredentials bool     `json:"AllowCredentials,omitempty"`
	AllowHeaders     []string `json:"AllowHeaders,omitempty"`
	AllowMethods     []string `json:"AllowMethods,omitempty"`
	AllowOrigins     []string `json:"AllowOrigins,omitempty"`
	ExposeHeaders    []string `json:"ExposeHeaders,omitempty"`
	MaxAge           int      `json:"MaxAge,omitempty"`
}

// functionURLRequest is the body of Create/UpdateFunctionUrlConfig.
type functionURLRequest struct {
	AuthType   string    `json:"AuthType"`
	InvokeMode string    `json:"InvokeMode"`
	Cors       *corsJSON `json:"Cors"`
}

// functionURLResponse is the AWS FunctionUrlConfig shape.
type functionURLResponse struct {
	FunctionArn      string    `json:"FunctionArn"`
	FunctionURL      string    `json:"FunctionUrl"`
	AuthType         string    `json:"AuthType"`
	InvokeMode       string    `json:"InvokeMode,omitempty"`
	Cors             *corsJSON `json:"Cors,omitempty"`
	CreationTime     string    `json:"CreationTime,omitempty"`
	LastModifiedTime string    `json:"LastModifiedTime,omitempty"`
}

// listFunctionURLConfigsResponse is the ListFunctionUrlConfigs envelope.
type listFunctionURLConfigsResponse struct {
	FunctionURLConfigs []functionURLResponse `json:"FunctionUrlConfigs"`
}

func toCorsJSON(c *sdrv.FunctionURLCors) *corsJSON {
	if c == nil {
		return nil
	}

	return &corsJSON{
		AllowCredentials: c.AllowCredentials,
		AllowHeaders:     c.AllowHeaders,
		AllowMethods:     c.AllowMethods,
		AllowOrigins:     c.AllowOrigins,
		ExposeHeaders:    c.ExposeHeaders,
		MaxAge:           c.MaxAge,
	}
}

func toCorsDriver(c *corsJSON) *sdrv.FunctionURLCors {
	if c == nil {
		return nil
	}

	return &sdrv.FunctionURLCors{
		AllowCredentials: c.AllowCredentials,
		AllowHeaders:     c.AllowHeaders,
		AllowMethods:     c.AllowMethods,
		AllowOrigins:     c.AllowOrigins,
		ExposeHeaders:    c.ExposeHeaders,
		MaxAge:           c.MaxAge,
	}
}

func toFunctionURLResponse(u *sdrv.FunctionURLConfig) functionURLResponse {
	return functionURLResponse{
		FunctionArn:      u.FunctionArn,
		FunctionURL:      u.FunctionURL,
		AuthType:         u.AuthType,
		InvokeMode:       u.InvokeMode,
		Cors:             toCorsJSON(u.Cors),
		CreationTime:     u.CreationTime,
		LastModifiedTime: u.LastModified,
	}
}

// serveFunctionURL dispatches the Lambda Function URL API under
// /2021-10-31/functions/{name}/url (create/get/update/delete) and
// /2021-10-31/functions/{name}/urls (list).
func (h *Handler) serveFunctionURL(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.fn.(functionURLManager)
	if !ok {
		writeError(w, http.StatusNotImplemented, "InvalidRequestException", "function urls not supported")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, functionURLPrefix)
	rest = strings.TrimPrefix(rest, "/")
	parts := strings.Split(rest, "/")

	const wantParts = 2 // {name}/url or {name}/urls
	if len(parts) != wantParts || parts[0] == "" {
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
		return
	}

	name := parts[0]

	switch parts[1] {
	case "url":
		serveFunctionURLItem(w, r, mgr, name)
	case "urls":
		listFunctionURLConfigs(w, r, mgr, name)
	default:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", "unsupported Lambda path")
	}
}

// serveFunctionURLItem handles POST/GET/PUT/DELETE on .../{name}/url.
func serveFunctionURLItem(w http.ResponseWriter, r *http.Request, mgr functionURLManager, name string) {
	switch r.Method {
	case http.MethodPost:
		writeFunctionURL(w, r, mgr.CreateFunctionURLConfig, name, http.StatusCreated)
	case http.MethodPut:
		writeFunctionURL(w, r, mgr.UpdateFunctionURLConfig, name, http.StatusOK)
	case http.MethodGet:
		u, err := mgr.GetFunctionURLConfig(r.Context(), name)
		if err != nil {
			writeErr(w, err)
			return
		}

		writeJSON(w, http.StatusOK, toFunctionURLResponse(u))
	case http.MethodDelete:
		if err := mgr.DeleteFunctionURLConfig(r.Context(), name); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
	}
}

// writeFunctionURL decodes a create/update body and writes the result. The op
// (create vs update) is supplied as fn so both share the decode/encode path.
func writeFunctionURL(
	w http.ResponseWriter, r *http.Request,
	fn func(context.Context, sdrv.FunctionURLConfig) (*sdrv.FunctionURLConfig, error),
	name string, status int,
) {
	var req functionURLRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	u, err := fn(r.Context(), sdrv.FunctionURLConfig{
		FunctionName: name,
		AuthType:     req.AuthType,
		InvokeMode:   req.InvokeMode,
		Cors:         toCorsDriver(req.Cors),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, status, toFunctionURLResponse(u))
}

func listFunctionURLConfigs(w http.ResponseWriter, r *http.Request, mgr functionURLManager, name string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "InvalidRequestException", "method not allowed")
		return
	}

	urls, err := mgr.ListFunctionURLConfigs(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := listFunctionURLConfigsResponse{FunctionURLConfigs: make([]functionURLResponse, 0, len(urls))}
	for i := range urls {
		out.FunctionURLConfigs = append(out.FunctionURLConfigs, toFunctionURLResponse(&urls[i]))
	}

	writeJSON(w, http.StatusOK, out)
}
