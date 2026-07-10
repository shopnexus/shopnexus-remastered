package app

import (
	"context"
	"log/slog"

	appconfig "shopnexus-server/internal/app/config"
	"shopnexus-server/internal/infras/metrics"
	"shopnexus-server/internal/shared/binder"

	"shopnexus-server/internal/shared/validator"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	echoSwagger "github.com/swaggo/echo-swagger"
	"go.uber.org/fx"
)

// NewEcho creates a new Echo instance.
func NewEcho() *echo.Echo {
	e := echo.New()

	// Middleware
	// e.Use(middleware.Logger())
	// e.Use(middleware.Recover())
	e.Use(middleware.CORS())
	e.Use(metrics.EchoMiddleware())

	return e
}

// RouteParams holds all the dependencies needed for route registration.
type RouteParams struct {
	fx.In

	Echo *echo.Echo
}

// SetupEcho registers all application routes.
func SetupEcho(params RouteParams) {
	// Set the custom validator
	customVal, err := validator.New()
	if err != nil {
		slog.Error("Failed to create validator", slog.Any("error", err))
		panic(err)
	}
	params.Echo.Validator = customVal
	params.Echo.Binder = binder.NewCustomBinder()

	// Metrics endpoint (no auth — must be registered before auth middleware on routes)
	params.Echo.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	// Health check
	params.Echo.GET("/health", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	// Swagger UI + raw spec. Backed by the generated internal/openapi package
	// (blank-imported in cmd/server/main.go). Browse at /swagger/index.html.
	params.Echo.GET("/swagger/*", echoSwagger.WrapHandler)
}

// SetupHTTPServer starts the HTTP server with lifecycle management.
func SetupHTTPServer(lc fx.Lifecycle, e *echo.Echo, cfg *appconfig.Config) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				port := cfg.Port
				if port == "" {
					port = "8080"
				}
				slog.Info("Starting HTTP server", "port", port)
				if err := e.Start(":" + port); err != nil {
					slog.Error("HTTP server error", slog.Any("error", err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			slog.Info("Shutting down HTTP server...")
			return e.Shutdown(ctx)
		},
	})
}
