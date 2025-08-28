package app

import (
	"context"

	"shopnexus-remastered/config"
	"shopnexus-remastered/internal/logger"
	accountecho "shopnexus-remastered/internal/module/account/transport/echo"
	authecho "shopnexus-remastered/internal/module/auth/transport/echo"
	"shopnexus-remastered/internal/module/shared/transport/echo/validator"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.uber.org/fx"
)

// NewEcho creates a new Echo instance
func NewEcho() *echo.Echo {
	e := echo.New()

	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	return e
}

// RouteParams holds all the dependencies needed for route registration
type RouteParams struct {
	fx.In
	Echo *echo.Echo

	Account *accountecho.Handler
	Auth    *authecho.Handler
	// Add more handlers as needed
}

// SetupEcho registers all application routes
func SetupEcho(params RouteParams) {
	// Set the custom validator
	customVal, err := validator.New()
	if err != nil {
		logger.Log.Sugar().Fatalf("Failed to create validator: %v", err)
	}
	params.Echo.Validator = customVal

	// Health check
	params.Echo.GET("/health", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})
}

// StartHTTPServer starts the HTTP server with lifecycle management
func StartHTTPServer(lc fx.Lifecycle, e *echo.Echo, cfg *config.Config) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			go func() {
				port := ":8080" // Default port, you can make this configurable
				logger.Log.Sugar().Infof("Starting HTTP server on port %s", port)
				if err := e.Start(port); err != nil {
					logger.Log.Sugar().Errorf("HTTP server error: %v", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Log.Sugar().Info("Shutting down HTTP server...")
			return e.Shutdown(ctx)
		},
	})
}
