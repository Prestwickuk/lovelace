package app

import (
	"net/http"

	"github.com/Prestwickuk/lovelace/internal/config"
	"github.com/Prestwickuk/lovelace/internal/httpapi"
	"github.com/Prestwickuk/lovelace/internal/httpx"
	"github.com/Prestwickuk/lovelace/internal/service"
	"github.com/Prestwickuk/lovelace/internal/store/postgres"
	"github.com/Prestwickuk/lovelace/internal/webui"
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
	h = httpx.SecurityHeadersWithConfig(cfg, h)
	return h, nil
}
