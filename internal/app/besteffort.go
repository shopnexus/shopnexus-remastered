package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"go.uber.org/fx"

	"shopnexus-server/config"
	"shopnexus-server/internal/shared/besteffort"
)

type BestEffortParams struct {
	fx.In
	Cfg        *config.Config
	Registrars []besteffort.Registrar `group:"besteffort"`
}

// SetupBestEffort starts the BestEffort HTTP/2 (h2c) endpoint and registers every
// module's biz on it. In the monolith, BestEffort calls go in-process; this server
// exists for the future split where remote clients call it over HTTP/2.
func SetupBestEffort(p BestEffortParams) {
	cfg := p.Cfg
	srv := besteffort.NewServer()
	for _, r := range p.Registrars {
		r(srv)
	}

	bindAddress := fmt.Sprintf(":%s", cfg.BestEffort.Port)

	// Serve HTTP/1.1 + unencrypted HTTP/2 (h2c) without the deprecated h2c wrapper.
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	server := &http.Server{Addr: bindAddress, Handler: srv.Handler(), Protocols: &protocols}

	go func() {
		slog.Info("Starting BestEffort service endpoint", "address", bindAddress)
		if err := server.ListenAndServe(); err != nil {
			slog.Error("BestEffort server error", slog.Any("error", err))
			os.Exit(1)
		}
	}()
}
