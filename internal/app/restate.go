package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.uber.org/fx"

	"shopnexus-server/config"

	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/server"
)

type RestateParams struct {
	fx.In
	Cfg  *config.Config
	Defs []restate.ServiceDefinition `group:"restate"`
}

func SetupRestate(p RestateParams) {
	cfg := p.Cfg
	bindAddress := fmt.Sprintf(":%s", cfg.Restate.ServicePort)

	srv := server.NewRestate()
	for _, d := range p.Defs {
		srv = srv.Bind(d)
	}

	go func() {
		slog.Info("Starting Restate service endpoint", "address", bindAddress)
		if err := srv.Start(context.Background(), bindAddress); err != nil {
			slog.Error("Restate server error", slog.Any("error", err))
			os.Exit(1)
		}
	}()

	// Auto-register with Restate runtime
	go func() {
		registerWithRestate(
			cfg.Restate.AdminAddress,
			fmt.Sprintf("%s:%s", cfg.Restate.ServiceHost, cfg.Restate.ServicePort),
		)
	}()
}

// registerWithRestate registers the service endpoint with the Restate admin API.
// Retries up to 10 times with 2s delay to handle startup ordering.
func registerWithRestate(adminAddress, serviceURL string) {
	type deploymentRequest struct {
		URI   string `json:"uri"`
		Force bool   `json:"force"`
	}

	body, _ := json.Marshal(deploymentRequest{URI: serviceURL, Force: true})

	for i := range 10 {
		time.Sleep(2 * time.Second)

		resp, err := http.Post(adminAddress+"/deployments", "application/json", bytes.NewReader(body))
		if err != nil {
			slog.Warn("Restate registration attempt failed", "attempt", i+1, "error", err)
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			slog.Info("Registered services with Restate", "admin", adminAddress, "endpoint", serviceURL)
			return
		}

		slog.Warn(
			"Restate registration returned non-OK",
			"attempt",
			i+1,
			"status",
			resp.StatusCode,
			"body",
			string(respBody),
		)
	}

	slog.Error("Failed to register with Restate after retries", "admin", adminAddress)
}
