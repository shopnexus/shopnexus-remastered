package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	appconfig "shopnexus-server/internal/app/config"
	accountbiz "shopnexus-server/internal/module/account/biz"
	analyticbiz "shopnexus-server/internal/module/analytic/biz"
	catalogbiz "shopnexus-server/internal/module/catalog/biz"
	chatbiz "shopnexus-server/internal/module/chat/biz"
	commonbiz "shopnexus-server/internal/module/common/biz"
	inventorybiz "shopnexus-server/internal/module/inventory/biz"
	orderbiz "shopnexus-server/internal/module/order/biz"
	promotionbiz "shopnexus-server/internal/module/promotion/biz"
	"shopnexus-server/internal/shared/besteffort"
)

// SetupBestEffort starts the BestEffort HTTP/2 (h2c) endpoint and registers every
// module's biz on it. In the monolith, BestEffort calls go in-process; this server
// exists for the future split where remote clients call it over HTTP/2.
func SetupBestEffort(
	cfg *appconfig.Config,
	orderBiz *orderbiz.OrderHandler,
	accountBiz *accountbiz.AccountHandler,
	catalogBiz *catalogbiz.CatalogHandler,
	commonBiz *commonbiz.CommonHandler,
	inventoryBiz *inventorybiz.InventoryHandler,
	promotionBiz *promotionbiz.PromotionHandler,
	analyticBiz *analyticbiz.AnalyticHandler,
	chatBiz *chatbiz.ChatHandler,
) {
	srv := besteffort.NewServer()

	accountbiz.RegisterAccountBestEffort(srv, accountBiz)
	analyticbiz.RegisterAnalyticBestEffort(srv, analyticBiz)
	catalogbiz.RegisterCatalogBestEffort(srv, catalogBiz)
	chatbiz.RegisterChatBestEffort(srv, chatBiz)
	commonbiz.RegisterCommonBestEffort(srv, commonBiz)
	inventorybiz.RegisterInventoryBestEffort(srv, inventoryBiz)
	orderbiz.RegisterOrderBestEffort(srv, orderBiz)
	promotionbiz.RegisterPromotionBestEffort(srv, promotionBiz)

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
