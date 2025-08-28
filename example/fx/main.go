package main

import (
	"context"
	"log"
	"os"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Config struct {
	DBURL string
	Port  string
}

// Provide Config from environment
func NewConfig() *Config {
	return &Config{
		DBURL: os.Getenv("DATABASE_URL"),
		Port:  getEnv("PORT", "8080"),
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// Provide GORM database connection
func NewDatabase(lc fx.Lifecycle, cfg *Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DBURL), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Lifecycle hook for cleanup
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			sqlDB, _ := db.DB()
			log.Println("Closing database connection...")
			return sqlDB.Close()
		},
	})

	return db, nil
}

// Provide Fiber app
func NewFiber() *fiber.App {
	return fiber.New()
}

// Start HTTP server with lifecycle hooks
func RegisterHTTPServer(lc fx.Lifecycle, app *fiber.App, cfg *Config) {
	// Define routes
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	server := app
	addr := ":" + cfg.Port

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			log.Println("Starting HTTP server on", addr)
			go func() {
				if err := server.Listen(addr); err != nil {
					log.Println("HTTP server error:", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			log.Println("Shutting down HTTP server...")
			return server.Shutdown()
		},
	})
}

func main() {
	app := fx.New(
		fx.Provide(
			NewConfig,
			NewDatabase,
			NewFiber,
		),
		fx.Invoke(
			RegisterHTTPServer,
		),
	)

	app.Run()
}
