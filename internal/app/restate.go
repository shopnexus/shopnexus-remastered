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

	appconfig "shopnexus-server/internal/app/config"
	accountbiz "shopnexus-server/internal/module/account/biz"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	chatbiz "shopnexus-server/internal/module/chat/biz"
	commonbiz "shopnexus-server/internal/module/common/biz"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	orderbiz "shopnexus-server/internal/module/order/biz"
	promotionbiz "shopnexus-server/internal/module/promotion/biz"

	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/server"
)

func SetupRestate(
	cfg *appconfig.Config,
	orderBiz *orderbiz.OrderHandler,
	accountBiz *accountbiz.AccountHandler,
	catalogBiz *catalogbiz.CatalogHandler,
	commonBiz *commonbiz.CommonHandler,
	inventoryBiz *inventorybiz.InventoryHandler,
	promotionBiz *promotionbiz.PromotionHandler,
	analyticBiz *analyticbiz.AnalyticHandler,
	chatBiz *chatbiz.ChatHandler,

	// workflows
	checkoutWf *orderbiz.CheckoutWorkflow,
	fulfillmentWf *orderbiz.FulfillmentWorkflow,
) {
	bindAddress := fmt.Sprintf(":%s", cfg.Restate.ServicePort)

	// General retry policy for all service methods: exponential backoff starting at 1s, max 30s, up to 10 attempts, then pause.
	retryPolicy := restate.WithInvocationRetryPolicy(
		restate.WithInitialInterval(time.Second),
		restate.WithMaxInterval(30*time.Second),
		restate.WithMaxAttempts(10),
		restate.PauseOnMaxAttempts())

	srv := server.NewRestate().
		Bind(restate.Reflect(accountbiz.NewAccountService(accountBiz), retryPolicy)).
		Bind(restate.Reflect(analyticbiz.NewAnalyticService(analyticBiz), retryPolicy)).
		Bind(restate.Reflect(catalogbiz.NewCatalogService(catalogBiz), retryPolicy)).
		Bind(restate.Reflect(chatbiz.NewChatService(chatBiz), retryPolicy)).
		Bind(restate.Reflect(commonbiz.NewCommonService(commonBiz), retryPolicy)).
		Bind(restate.Reflect(inventorybiz.NewInventoryService(inventoryBiz), retryPolicy)).
		Bind(restate.Reflect(orderBiz, retryPolicy)).
		Bind(restate.Reflect(promotionbiz.NewPromotionService(promotionBiz), retryPolicy)).
		Bind(restate.Reflect(checkoutWf, retryPolicy)).
		Bind(restate.Reflect(fulfillmentWf, retryPolicy))

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
