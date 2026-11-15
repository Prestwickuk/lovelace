package app

import (
	"net/http"

	"github.com/tirbach/tir-bach/internal/config"
	"github.com/tirbach/tir-bach/internal/httpapi"
	"github.com/tirbach/tir-bach/internal/httpx"
	"github.com/tirbach/tir-bach/internal/service"
	"github.com/tirbach/tir-bach/internal/store/postgres"
	"github.com/tirbach/tir-bach/internal/webui"
)

// NewRouter builds the full Phase 1 HTTP handler with security headers, request context,
// session authentication, JSON API routes, and Admin Console web routes.
func NewRouter(cfg config.Config, store *postgres.Store, svc *service.Service) (http.Handler, error) {
	mux := http.NewServeMux()

	apiHandler := httpapi.New(svc, store, cfg)
	apiHandler.Register(mux)

	webHandler, err := webui.New(svc, cfg)
	if err != nil {
		return nil, err
	}
	webHandler.Register(mux)

	var h http.Handler = mux
	h = httpx.WithSessionAuth(svc, h)
	h = httpx.RequestContext(cfg, h)
	h = httpx.SecurityHeaders(h)
	return h, nil
}
