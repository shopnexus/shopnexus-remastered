package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"shopnexus-remastered/internal/db"
	"shopnexus-remastered/internal/utils/ptr"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jaswdr/faker/v2"
)

// PaymentSeedData holds seeded payment data for other seeders to reference
type PaymentSeedData struct {
	Orders           []db.PaymentOrder
	OrderItems       []db.PaymentOrderItem
	OrderItemSerials []db.PaymentOrderItemSerial
	VnpayPayments    []db.PaymentVnpay
	Refunds          []db.PaymentRefund
	RefundDisputes   []db.PaymentRefundDispute
	Invoices         []db.PaymentInvoice
	InvoiceItems     []db.PaymentInvoiceItem
}

// SeedPaymentSchema seeds the payment schema with fake data
func SeedPaymentSchema(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig, accountData *AccountSeedData, catalogData *CatalogSeedData, inventoryData *InventorySeedData) (*PaymentSeedData, error) {
	fmt.Println("💳 Seeding payment schema...")

	data := &PaymentSeedData{
		Orders:           make([]db.PaymentOrder, 0),
		OrderItems:       make([]db.PaymentOrderItem, 0),
		OrderItemSerials: make([]db.PaymentOrderItemSerial, 0),
		VnpayPayments:    make([]db.PaymentVnpay, 0),
		Refunds:          make([]db.PaymentRefund, 0),
		RefundDisputes:   make([]db.PaymentRefundDispute, 0),
		Invoices:         make([]db.PaymentInvoice, 0),
		InvoiceItems:     make([]db.PaymentInvoiceItem, 0),
	}

	if len(accountData.Customers) == 0 || len(catalogData.ProductSkus) == 0 {
		fmt.Println("⚠️ No customers or product SKUs found, skipping payment seeding")
		return data, nil
	}

	paymentMethods := db.AllPaymentPaymentMethodValues()
	statuses := db.AllSharedStatusValues()

	// Create orders
	for i := 0; i < cfg.OrderCount; i++ {
		customer := accountData.Customers[fake.RandomDigit()%len(accountData.Customers)]
		customerAddress := ""

		// Find an address for this customer
		for _, addr := range accountData.Addresses {
			if addr.AccountID == customer.AccountID {
				customerAddress = fmt.Sprintf("%s, %s, %s, %s",
					addr.AddressLine, addr.City, addr.StateProvince, addr.Country)
				break
			}
		}

		if customerAddress == "" {
			customerAddress = fake.Address().Address()
		}

		order, err := retryWithUniqueValues(3, func(attempt int) (db.PaymentOrder, error) {
			return storage.CreateOrder(ctx, db.CreateOrderParams{
				Code:          generateUniqueCode(fake, "ORDER"),
				CustomerID:    customer.ID,
				PaymentMethod: paymentMethods[fake.RandomDigit()%len(paymentMethods)],
				Status:        statuses[fake.RandomDigit()%len(statuses)],
				Address:       customerAddress,
				DateCreated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
				DateUpdated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create order %d: %w", i+1, err)
		}
		data.Orders = append(data.Orders, order)

		// Create order items (1-5 items per order)
		itemCount := fake.RandomDigit()%5 + 1
		orderTotal := int64(0)
		orderItems := make([]db.PaymentOrderItem, 0)

		for j := 0; j < itemCount; j++ {
			sku := catalogData.ProductSkus[fake.RandomDigit()%len(catalogData.ProductSkus)]
			quantity := int64(fake.RandomDigit()%3 + 1) // 1-3 items

			orderItem, err := retryWithUniqueValues(3, func(attempt int) (db.PaymentOrderItem, error) {
				return storage.CreateOrderItem(ctx, db.CreateOrderItemParams{
					Code:     generateUniqueCode(fake, "ITEM"),
					OrderID:  order.ID,
					SkuID:    sku.ID,
					Quantity: quantity,
				})
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create order item: %w", err)
			}
			data.OrderItems = append(data.OrderItems, orderItem)
			orderItems = append(orderItems, orderItem)
			orderTotal += sku.Price * quantity

			// Create order item serials for products that have serials
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
					orderSerial, err := storage.CreateOrderItemSerial(ctx, db.CreateOrderItemSerialParams{
						OrderItemID:     orderItem.ID,
						ProductSerialID: serial.ID,
					})
					if err != nil {
						return nil, fmt.Errorf("failed to create order item serial: %w", err)
					}
					data.OrderItemSerials = append(data.OrderItemSerials, orderSerial)
				}
			}
		}

		// Create VNPay payment for Card/EWallet orders (50% chance)
		if (order.PaymentMethod == "Card" || order.PaymentMethod == "EWallet") && fake.Boolean().Bool() {
			vnpay, err := storage.CreateVnpay(ctx, db.CreateVnpayParams{
				OrderID:              order.ID,
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
			if err != nil {
				return nil, fmt.Errorf("failed to create VNPay payment: %w", err)
			}
			data.VnpayPayments = append(data.VnpayPayments, vnpay)
		}

		// Create refunds for some order items (10% chance)
		for _, orderItem := range orderItems {
			if fake.RandomDigit()%10 == 0 && order.Status == "Success" {
				refundMethods := db.AllPaymentRefundMethodValues()
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

				refund, err := retryWithUniqueValues(3, func(attempt int) (db.PaymentRefund, error) {
					return storage.CreateRefund(ctx, db.CreateRefundParams{
						Code:         generateUniqueCode(fake, "REFUND"),
						OrderItemID:  orderItem.ID,
						ReviewedByID: pgtype.Int8{Int64: ptr.DerefDefault(reviewerID, 0), Valid: reviewerID != nil},
						Method:       refundMethods[fake.RandomDigit()%len(refundMethods)],
						Status:       refundStatuses[fake.RandomDigit()%len(refundStatuses)],
						Reason:       generateRefundReason(fake),
						Address:      pgtype.Text{String: refundAddress, Valid: true},
						DateCreated:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
					})
				})
				if err != nil {
					return nil, fmt.Errorf("failed to create refund: %w", err)
				}
				data.Refunds = append(data.Refunds, refund)

				// Create refund dispute (20% chance)
				if fake.RandomDigit()%5 == 0 && len(accountData.Vendors) > 0 {
					vendor := accountData.Vendors[fake.RandomDigit()%len(accountData.Vendors)]
					dispute, err := retryWithUniqueValues(3, func(attempt int) (db.PaymentRefundDispute, error) {
						return storage.CreateRefundDispute(ctx, db.CreateRefundDisputeParams{
							Code:        generateUniqueCode(fake, "DISPUTE"),
							RefundID:    refund.ID,
							VendorID:    vendor.ID,
							Reason:      generateDisputeReason(fake),
							Status:      "Pending",
							DateCreated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
							DateUpdated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
						})
					})
					if err != nil {
						return nil, fmt.Errorf("failed to create refund dispute: %w", err)
					}
					data.RefundDisputes = append(data.RefundDisputes, dispute)
				}
			}
		}

		// Create invoice for completed orders
		if order.Status == "Success" {
			invoiceTypes := db.AllPaymentInvoiceTypeValues()

			invoice, err := retryWithUniqueValues(3, func(attempt int) (db.PaymentInvoice, error) {
				return storage.CreateInvoice(ctx, db.CreateInvoiceParams{
					Code:           generateUniqueCode(fake, "INV"),
					Type:           invoiceTypes[fake.RandomDigit()%len(invoiceTypes)],
					RefType:        "Order",
					RefID:          order.ID,
					BuyerAccountID: customer.AccountID,
					Status:         "Success",
					PaymentMethod:  order.PaymentMethod,
					Address:        order.Address,
					Phone:          generateUniquePhone(fake),
					Subtotal:       orderTotal,
					Total:          orderTotal - int64(fake.RandomDigit()%100), // Small discount
					FileRsID:       fake.UUID().V4(),
					Hash:           []byte(fake.Hash().SHA256()),
					DateCreated:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
				})
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create invoice: %w", err)
			}
			data.Invoices = append(data.Invoices, invoice)

			// Create invoice items
			for _, orderItem := range orderItems {
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

					invoiceItem, err := storage.CreateInvoiceItem(ctx, db.CreateInvoiceItemParams{
						InvoiceID: invoice.ID,
						Snapshot:  snapshotMarshal,
						Quantity:  orderItem.Quantity,
						UnitPrice: unitPrice,
						Subtotal:  subtotal,
						Total:     total,
					})
					if err != nil {
						return nil, fmt.Errorf("failed to create invoice item: %w", err)
					}
					data.InvoiceItems = append(data.InvoiceItems, invoiceItem)
				}
			}
		}
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
