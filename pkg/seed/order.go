package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"shopnexus-remastered/internal/utils/pgutil"
	"time"

	"shopnexus-remastered/internal/db"
	"shopnexus-remastered/internal/utils/ptr"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jaswdr/faker/v2"
)

// PaymentSeedData holds seeded payment data for other seeders to reference
type PaymentSeedData struct {
	Orders           []db.OrderBase
	OrderItems       []db.OrderItem
	OrderItemSerials []db.OrderItemSerial
	VnpayPayments    []db.OrderVnpay
	Refunds          []db.OrderRefund
	RefundDisputes   []db.OrderRefundDispute
	Invoices         []db.OrderInvoice
	InvoiceItems     []db.OrderInvoiceItem
}

// SeedPaymentSchema seeds the payment schema with fake data
func SeedPaymentSchema(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig, accountData *AccountSeedData, catalogData *CatalogSeedData, inventoryData *InventorySeedData) (*PaymentSeedData, error) {
	fmt.Println("💳 Seeding payment schema...")

	// Tạo unique tracker để theo dõi tính duy nhất
	tracker := NewUniqueTracker()

	data := &PaymentSeedData{
		Orders:           make([]db.OrderBase, 0),
		OrderItems:       make([]db.OrderItem, 0),
		OrderItemSerials: make([]db.OrderItemSerial, 0),
		VnpayPayments:    make([]db.OrderVnpay, 0),
		Refunds:          make([]db.OrderRefund, 0),
		RefundDisputes:   make([]db.OrderRefundDispute, 0),
		Invoices:         make([]db.OrderInvoice, 0),
		InvoiceItems:     make([]db.OrderInvoiceItem, 0),
	}

	if len(accountData.Customers) == 0 || len(catalogData.ProductSkus) == 0 {
		fmt.Println("⚠️ No customers or product SKUs found, skipping payment seeding")
		return data, nil
	}

	paymentMethods := db.AllOrderPaymentMethodValues()
	statuses := db.AllSharedStatusValues()

	// Prepare bulk order data
	orderParams := make([]db.CreateOrderBaseParams, cfg.OrderCount)
	for i := 0; i < cfg.OrderCount; i++ {
		customer := accountData.Customers[fake.RandomDigit()%len(accountData.Customers)]
		customerAddress := ""

		// Find an address for this customer
		for _, addr := range accountData.Addresses {
			if addr.AccountID == customer.ID {
				customerAddress = fmt.Sprintf("%s, %s, %s, %s",
					addr.AddressLine, addr.City, addr.StateProvince, addr.Country)
				break
			}
		}

		if customerAddress == "" {
			customerAddress = fake.Address().Address()
		}

		orderParams[i] = db.CreateOrderBaseParams{
			Code:          generateUniqueCodeWithTracker(fake, "ORDER", tracker),
			CustomerID:    customer.ID,
			PaymentMethod: paymentMethods[fake.RandomDigit()%len(paymentMethods)],
			Status:        statuses[fake.RandomDigit()%len(statuses)],
			Address:       customerAddress,
			DateCreated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			DateUpdated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}
	}

	// Bulk insert orders
	_, err := storage.CreateOrderBase(ctx, orderParams)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk create orders: %w", err)
	}

	// Query back created orders
	orders, err := storage.ListOrderBase(ctx, db.ListOrderBaseParams{
		Limit:  pgutil.Int32ToPgInt4(int32(len(orderParams) * 2)),
		Offset: pgutil.Int32ToPgInt4(0),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query back created orders: %w", err)
	}

	// Match orders with our parameters by code (unique identifier)
	orderCodeMap := make(map[string]db.OrderBase)
	for _, order := range orders {
		orderCodeMap[order.Code] = order
	}

	// Populate data.Orders with actual database records
	for _, params := range orderParams {
		if order, exists := orderCodeMap[params.Code]; exists {
			data.Orders = append(data.Orders, order)
		}
	}

	// Prepare bulk order items and related data
	var orderItemParams []db.CreateOrderItemParams
	var orderItemSerialParams []db.CreateOrderItemSerialParams
	var vnpayParams []db.CreateOrderVnpayParams
	var refundParams []db.CreateOrderRefundParams
	var refundDisputeParams []db.CreateOrderRefundDisputeParams
	var invoiceParams []db.CreateOrderInvoiceParams
	var invoiceItemParams []db.CreateOrderInvoiceItemParams

	orderTotals := make(map[int64]int64)                            // order ID -> total
	orderItemMapping := make(map[string][]db.CreateOrderItemParams) // order code -> order items

	// Create order items for each order
	for _, order := range data.Orders {
		itemCount := fake.RandomDigit()%5 + 1
		orderTotal := int64(0)
		orderItems := make([]db.OrderItem, 0)

		var currentOrderItems []db.CreateOrderItemParams
		for j := 0; j < itemCount; j++ {
			sku := catalogData.ProductSkus[fake.RandomDigit()%len(catalogData.ProductSkus)]
			quantity := int64(fake.RandomDigit()%3 + 1) // 1-3 items

			itemCode := generateUniqueCodeWithTracker(fake, "ITEM", tracker)
			orderItemParam := db.CreateOrderItemParams{
				Code:     itemCode,
				OrderID:  order.ID,
				SkuID:    sku.ID,
				Quantity: quantity,
			}
			orderItemParams = append(orderItemParams, orderItemParam)
			currentOrderItems = append(currentOrderItems, orderItemParam)
			orderTotal += sku.Price * quantity

			// Store serial assignments for later (we'll need actual order item IDs)
			var availableSerials []db.InventorySkuSerial
			for _, serial := range inventoryData.ProductSerials {
				if serial.SkuID == sku.ID && serial.Status == "Active" {
					availableSerials = append(availableSerials, serial)
				}
			}

			if len(availableSerials) > 0 {
				serialsToAssign := int(quantity)
				if serialsToAssign > len(availableSerials) {
					serialsToAssign = len(availableSerials)
				}

				for k := 0; k < serialsToAssign; k++ {
					serial := availableSerials[k]
					// Store with item code as temporary reference
					orderItemSerialParams = append(orderItemSerialParams, db.CreateOrderItemSerialParams{
						OrderItemID:     0, // Will be filled after order item creation
						ProductSerialID: serial.ID,
					})
				}
			}
		}
		orderItemMapping[order.Code] = currentOrderItems
		orderTotals[order.ID] = orderTotal

		// Prepare VNPay payment for Card/EWallet orders (50% chance)
		if (order.PaymentMethod == "Card" || order.PaymentMethod == "EWallet") && fake.Boolean().Bool() {
			vnpayParams = append(vnpayParams, db.CreateOrderVnpayParams{
				ID:                   order.ID,
				VnpAmount:            fmt.Sprintf("%d", orderTotal),
				VnpBankCode:          fake.Payment().CreditCardType(),
				VnpCardType:          "ATM",
				VnpOrderInfo:         fmt.Sprintf("Payment for order %s", order.Code),
				VnpPayDate:           "20241201120000",
				VnpResponseCode:      "00",
				VnpSecureHash:        fake.Hash().SHA256(),
				VnpTmnCode:           "2QXUI4J4",
				VnpTransactionNo:     fmt.Sprintf("%d", fake.RandomDigit()%1000000+1000000),
				VnpTransactionStatus: "00",
				VnpTxnRef:            order.Code,
			})
		}

		// Prepare refunds for some order items (10% chance)
		for _, orderItem := range orderItems {
			if fake.RandomDigit()%10 == 0 && order.Status == "Success" {
				refundMethods := db.AllOrderRefundMethodValues()
				refundStatuses := db.AllSharedStatusValues()

				var reviewerID *int64
				if len(accountData.Vendors) > 0 && fake.Boolean().Bool() {
					vendor := accountData.Vendors[fake.RandomDigit()%len(accountData.Vendors)]
					reviewerID = &vendor.ID
				}

				refundAddress := ""
				if fake.Boolean().Bool() { // 50% chance of having pickup address
					refundAddress = fake.Address().Address()
				}

				refundParams = append(refundParams, db.CreateOrderRefundParams{
					Code:         generateUniqueCodeWithTracker(fake, "REFUND", tracker),
					OrderItemID:  orderItem.ID,
					ReviewedByID: pgtype.Int8{Int64: ptr.DerefDefault(reviewerID, 0), Valid: reviewerID != nil},
					Method:       refundMethods[fake.RandomDigit()%len(refundMethods)],
					Status:       refundStatuses[fake.RandomDigit()%len(refundStatuses)],
					Reason:       generateRefundReason(fake),
					Address:      pgtype.Text{String: refundAddress, Valid: true},
					DateCreated:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
				})
			}
		}

		// Prepare invoice for completed orders
		if order.Status == "Success" {
			invoiceTypes := db.AllOrderInvoiceTypeValues()
			customer := getCustomerByID(accountData.Customers, order.CustomerID)

			// Generate hash for this invoice
			hash := []byte(fake.Hash().SHA256())
			var prevHash []byte
			if len(invoiceParams) > 0 {
				prevHash = invoiceParams[len(invoiceParams)-1].Hash // Use previous invoice's hash
			} else {
				prevHash = []byte("genesis") // First invoice
			}

			invoiceParams = append(invoiceParams, db.CreateOrderInvoiceParams{
				Code:           generateUniqueCodeWithTracker(fake, "INV", tracker),
				Type:           invoiceTypes[fake.RandomDigit()%len(invoiceTypes)],
				RefType:        "Order",
				RefID:          order.ID,
				BuyerAccountID: customer.ID,
				Status:         "Success",
				PaymentMethod:  order.PaymentMethod,
				Address:        order.Address,
				Phone:          generateUniquePhoneWithTracker(fake, tracker),
				Subtotal:       orderTotal,
				Total:          orderTotal - int64(fake.RandomDigit()%100), // Small discount
				FileRsID:       fake.UUID().V4(),
				Hash:           hash,
				PrevHash:       prevHash,
				DateCreated:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
		}
	}

	// Bulk insert order items
	if len(orderItemParams) > 0 {
		_, err = storage.CreateOrderItem(ctx, orderItemParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create order items: %w", err)
		}

		// Query back created order items
		orderItems, err := storage.ListOrderItem(ctx, db.ListOrderItemParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(orderItemParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created order items: %w", err)
		}

		// Match order items with our parameters by code (unique identifier)
		orderItemCodeMap := make(map[string]db.OrderItem)
		for _, orderItem := range orderItems {
			orderItemCodeMap[orderItem.Code] = orderItem
		}

		// Populate data.OrderItems with actual database records
		for _, params := range orderItemParams {
			if orderItem, exists := orderItemCodeMap[params.Code]; exists {
				data.OrderItems = append(data.OrderItems, orderItem)
			}
		}

		// Update order item serial parameters with actual order item IDs
		// This is a simplified approach - we'll just create serials for the first few items
		serialIndex := 0
		for _, orderItem := range data.OrderItems {
			// Find available serials for this SKU
			var availableSerials []db.InventorySkuSerial
			for _, serial := range inventoryData.ProductSerials {
				if serial.SkuID == orderItem.SkuID && serial.Status == "Active" {
					availableSerials = append(availableSerials, serial)
				}
			}

			if len(availableSerials) > 0 && serialIndex < len(orderItemSerialParams) {
				serialsToAssign := int(orderItem.Quantity)
				if serialsToAssign > len(availableSerials) {
					serialsToAssign = len(availableSerials)
				}

				for k := 0; k < serialsToAssign && serialIndex < len(orderItemSerialParams); k++ {
					orderItemSerialParams[serialIndex].OrderItemID = orderItem.ID
					serialIndex++
				}
			}
		}
	}

	// Bulk insert order item serials
	if len(orderItemSerialParams) > 0 {
		// Filter out serials without valid order item IDs
		validSerialParams := make([]db.CreateOrderItemSerialParams, 0)
		for _, serial := range orderItemSerialParams {
			if serial.OrderItemID > 0 {
				validSerialParams = append(validSerialParams, serial)
			}
		}

		if len(validSerialParams) > 0 {
			_, err = storage.CreateOrderItemSerial(ctx, validSerialParams)
			if err != nil {
				return nil, fmt.Errorf("failed to bulk create order item serials: %w", err)
			}

			// Query back created order item serials
			orderItemSerials, err := storage.ListOrderItemSerial(ctx, db.ListOrderItemSerialParams{
				Limit:  pgutil.Int32ToPgInt4(int32(len(validSerialParams) * 2)),
				Offset: pgutil.Int32ToPgInt4(0),
			})
			if err != nil {
				return nil, fmt.Errorf("failed to query back created order item serials: %w", err)
			}

			// Populate data.OrderItemSerials with actual database records
			data.OrderItemSerials = orderItemSerials
		}
	}

	// Bulk insert VNPay payments
	if len(vnpayParams) > 0 {
		_, err = storage.CreateOrderVnpay(ctx, vnpayParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create VNPay payments: %w", err)
		}

		// Query back created VNPay payments
		vnpayPayments, err := storage.ListOrderVnpay(ctx, db.ListOrderVnpayParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(vnpayParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created VNPay payments: %w", err)
		}

		// Populate data.VnpayPayments with actual database records
		data.VnpayPayments = vnpayPayments
	}

	// Bulk insert refunds
	if len(refundParams) > 0 {
		_, err = storage.CreateOrderRefund(ctx, refundParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create refunds: %w", err)
		}

		// Query back created refunds
		refunds, err := storage.ListOrderRefund(ctx, db.ListOrderRefundParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(refundParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created refunds: %w", err)
		}

		// Match refunds with our parameters by code (unique identifier)
		refundCodeMap := make(map[string]db.OrderRefund)
		for _, refund := range refunds {
			refundCodeMap[refund.Code] = refund
		}

		// Populate data.Refunds with actual database records and prepare disputes
		for _, params := range refundParams {
			if refund, exists := refundCodeMap[params.Code]; exists {
				data.Refunds = append(data.Refunds, refund)

				// Create refund dispute (20% chance)
				if fake.RandomDigit()%5 == 0 && len(accountData.Vendors) > 0 {
					vendor := accountData.Vendors[fake.RandomDigit()%len(accountData.Vendors)]
					refundDisputeParams = append(refundDisputeParams, db.CreateOrderRefundDisputeParams{
						Code:        generateUniqueCodeWithTracker(fake, "DISPUTE", tracker),
						RefundID:    refund.ID,
						IssuedByID:  vendor.ID,
						Reason:      generateDisputeReason(fake),
						Status:      "Pending",
						DateCreated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
						DateUpdated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
					})
				}
			}
		}
	}

	// Bulk insert refund disputes
	if len(refundDisputeParams) > 0 {
		_, err = storage.CreateOrderRefundDispute(ctx, refundDisputeParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create refund disputes: %w", err)
		}

		// Query back created refund disputes
		refundDisputes, err := storage.ListOrderRefundDispute(ctx, db.ListOrderRefundDisputeParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(refundDisputeParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created refund disputes: %w", err)
		}

		// Populate data.RefundDisputes with actual database records
		data.RefundDisputes = refundDisputes
	}

	// Bulk insert invoices
	if len(invoiceParams) > 0 {
		_, err = storage.CreateOrderInvoice(ctx, invoiceParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create invoices: %w", err)
		}

		// Query back created invoices
		invoices, err := storage.ListOrderInvoice(ctx, db.ListOrderInvoiceParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(invoiceParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created invoices: %w", err)
		}

		// Match invoices with our parameters by code (unique identifier)
		invoiceCodeMap := make(map[string]db.OrderInvoice)
		for _, invoice := range invoices {
			invoiceCodeMap[invoice.Code] = invoice
		}

		// Populate data.Invoices with actual database records and prepare invoice items
		for _, params := range invoiceParams {
			if invoice, exists := invoiceCodeMap[params.Code]; exists {
				data.Invoices = append(data.Invoices, invoice)

				// Prepare invoice items for this invoice
				for _, orderItem := range data.OrderItems {
					if orderItem.OrderID == params.RefID {
						sku := getSKUByID(catalogData.ProductSkus, orderItem.SkuID)
						if sku != nil {
							spu := getSPUByID(catalogData.ProductSpus, sku.SpuID)
							snapshotData := map[string]interface{}{
								"product_name": "",
								"product_code": sku.Code,
								"price":        sku.Price,
							}
							if spu != nil {
								snapshotData["product_name"] = spu.Name
							}
							snapshotMarshal, _ := json.Marshal(snapshotData)

							unitPrice := sku.Price
							subtotal := unitPrice * orderItem.Quantity
							total := subtotal

							invoiceItemParams = append(invoiceItemParams, db.CreateOrderInvoiceItemParams{
								InvoiceID: invoice.ID,
								Snapshot:  snapshotMarshal,
								Quantity:  orderItem.Quantity,
								UnitPrice: unitPrice,
								Subtotal:  subtotal,
								Total:     total,
							})
						}
					}
				}
			}
		}
	}

	// Bulk insert invoice items
	if len(invoiceItemParams) > 0 {
		_, err = storage.CreateOrderInvoiceItem(ctx, invoiceItemParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create invoice items: %w", err)
		}

		// Query back created invoice items
		invoiceItems, err := storage.ListOrderInvoiceItem(ctx, db.ListOrderInvoiceItemParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(invoiceItemParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created invoice items: %w", err)
		}

		// Populate data.InvoiceItems with actual database records
		data.InvoiceItems = invoiceItems
	}

	fmt.Printf("✅ Payment schema seeded: %d orders, %d order items, %d order serials, %d vnpay payments, %d refunds, %d disputes, %d invoices, %d invoice items\n",
		len(data.Orders), len(data.OrderItems), len(data.OrderItemSerials), len(data.VnpayPayments),
		len(data.Refunds), len(data.RefundDisputes), len(data.Invoices), len(data.InvoiceItems))

	return data, nil
}

// Helper functions
func getSKUByID(skus []db.CatalogProductSku, id int64) *db.CatalogProductSku {
	for _, sku := range skus {
		if sku.ID == id {
			return &sku
		}
	}
	return nil
}

func getSPUByID(spus []db.CatalogProductSpu, id int64) *db.CatalogProductSpu {
	for _, spu := range spus {
		if spu.ID == id {
			return &spu
		}
	}
	return nil
}

func getCustomerByID(customers []db.AccountCustomer, id int64) *db.AccountCustomer {
	for _, customer := range customers {
		if customer.ID == id {
			return &customer
		}
	}
	return nil
}

func generateRefundReason(fake *faker.Faker) string {
	reasons := []string{
		"Product arrived damaged",
		"Wrong item received",
		"Product doesn't match description",
		"Changed my mind",
		"Found better price elsewhere",
		"Product quality is poor",
		"Shipping took too long",
		"Product doesn't fit",
		"Missing accessories",
		"Product not working properly",
	}
	return reasons[fake.RandomDigit()%len(reasons)]
}

func generateDisputeReason(fake *faker.Faker) string {
	reasons := []string{
		"Customer misunderstood product description",
		"Product was shipped correctly according to specifications",
		"Damage occurred during shipping, not vendor's fault",
		"Customer didn't follow return policy",
		"Product was tested before shipping",
		"Customer changed mind after purchase deadline",
		"Return request beyond acceptable timeframe",
		"Product was delivered to correct address",
	}
	return reasons[fake.RandomDigit()%len(reasons)]
}
