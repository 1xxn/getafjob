// Package apiv1 serves the JSON API under /api/v1 (spec-first: api/openapi.yaml → apiv1.gen.go).
package apiv1

//go:generate go tool oapi-codegen -config ../../../api/oapi-codegen.yaml ../../../api/openapi.yaml

import (
	"context"
	"net/http"

	"github.com/1xxn/getafjob/internal/platform/buildinfo"
)

// Server implements the generated StrictServerInterface.
type Server struct{}

var _ StrictServerInterface = Server{}

// GetMeta returns the server version and API level.
func (Server) GetMeta(context.Context, GetMetaRequestObject) (GetMetaResponseObject, error) {
	return GetMeta200JSONResponse{Version: buildinfo.Get().Version, Api: V1}, nil
}

// Mount registers the API on mux under /api/v1.
func Mount(mux *http.ServeMux) http.Handler {
	return HandlerWithOptions(NewStrictHandler(Server{}, nil), StdHTTPServerOptions{
		BaseURL:    "/api/v1",
		BaseRouter: mux,
	})
}
