package seed

import (
	"context"
	"fmt"
	"shopnexus-remastered/internal/utils/pgutil"
	"time"

	"shopnexus-remastered/internal/db"

	"shopnexus-remastered/internal/utils/ptr"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jaswdr/faker/v2"
)

// AccountSeedData holds seeded account data for other seeders to reference
type AccountSeedData struct {
	Accounts        []db.AccountBase
	Customers       []db.AccountCustomer
	Vendors         []db.AccountVendor
	Profiles        []db.AccountProfile
	Addresses       []db.AccountAddress
	IncomeHistories []db.AccountIncomeHistory
	Notifications   []db.AccountNotification
}

// SeedAccountSchema seeds the account schema with fake data
func SeedAccountSchema(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig) (*AccountSeedData, error) {
	fmt.Println("🏠 Seeding account schema...")

	// Tạo unique tracker để theo dõi tính duy nhất
	tracker := NewUniqueTracker()

	data := &AccountSeedData{
		Accounts:        make([]db.AccountBase, 0, cfg.AccountCount),
		Customers:       make([]db.AccountCustomer, 0),
		Vendors:         make([]db.AccountVendor, 0),
		Profiles:        make([]db.AccountProfile, 0),
		Addresses:       make([]db.AccountAddress, 0),
		IncomeHistories: make([]db.AccountIncomeHistory, 0),
		Notifications:   make([]db.AccountNotification, 0),
	}

	// Prepare bulk account data
	accountParams := make([]db.CreateAccountBaseParams, cfg.AccountCount)
	customerAccountIDs := make([]int64, 0)
	vendorAccountIDs := make([]int64, 0)

	for i := 0; i < cfg.AccountCount; i++ {
		var accountType db.AccountType
		if i%5 == 0 { // 20% vendors
			accountType = "Vendor"
		} else {
			accountType = "Customer"
		}

		accountParams[i] = db.CreateAccountBaseParams{
			Code:        generateUniqueCodeWithTracker(fake, "ACC", tracker),
			Type:        accountType,
			Status:      db.AccountStatusActive,
			Phone:       pgtype.Text{String: generateUniquePhoneWithTracker(fake, tracker), Valid: true},
			Email:       pgtype.Text{String: generateUniqueEmailWithTracker(fake, tracker), Valid: true},
			Username:    pgtype.Text{String: generateUniqueUsernameWithTracker(fake, tracker), Valid: true},
			Password:    pgtype.Text{String: fake.Hash().MD5(), Valid: true},
			DateCreated: pgtype.Timestamptz{Time: time.Now().Add(-time.Duration(fake.RandomDigit()%365) * 24 * time.Hour), Valid: true},
			DateUpdated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}
	}

	// Bulk insert accounts
	_, err := storage.CreateAccountBase(ctx, accountParams)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk create accounts: %w", err)
	}

	// Query back created accounts to get actual IDs
	accounts, err := storage.ListAccountBase(ctx, db.ListAccountBaseParams{
		Limit:  pgutil.Int32ToPgInt4(int32(cfg.AccountCount * 2)), // Get more than needed to be safe
		Offset: pgutil.Int32ToPgInt4(0),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query back created accounts: %w", err)
	}

	// Match created accounts with our parameters by code (unique identifier)
	accountCodeMap := make(map[string]db.AccountBase)
	for _, account := range accounts {
		accountCodeMap[account.Code] = account
	}

	// Populate data.Accounts with actual database records
	for _, params := range accountParams {
		if account, exists := accountCodeMap[params.Code]; exists {
			data.Accounts = append(data.Accounts, account)

			if params.Type == "Customer" {
				customerAccountIDs = append(customerAccountIDs, account.ID)
			} else {
				vendorAccountIDs = append(vendorAccountIDs, account.ID)
			}
		}
	}

	// Prepare bulk profile data
	profileParams := make([]db.CreateAccountProfileParams, cfg.AccountCount)
	for i, account := range data.Accounts {
		person := fake.Person()

		var gender db.AccountGender
		genderValue := fake.Gender().Name()
		if genderValue == "masculine" {
			gender = db.AccountGenderMale
		} else {
			gender = db.AccountGenderFemale
		}

		birthDate := fake.Time().TimeBetween(
			time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC),
			time.Date(2005, 12, 31, 0, 0, 0, 0, time.UTC),
		)

		profileParams[i] = db.CreateAccountProfileParams{
			ID:            account.ID,
			Gender:        db.NullAccountGender{AccountGender: gender, Valid: true},
			Name:          pgtype.Text{String: person.Name(), Valid: true},
			DateOfBirth:   pgtype.Date{Time: birthDate, Valid: true},
			EmailVerified: fake.Boolean().Bool(),
			PhoneVerified: fake.Boolean().Bool(),
			DateCreated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			DateUpdated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}
	}

	// Bulk insert profiles
	_, err = storage.CreateAccountProfile(ctx, profileParams)
	if err != nil {
		return nil, fmt.Errorf("failed to bulk create profiles: %w", err)
	}

	// Query back created profiles
	profiles, err := storage.ListAccountProfile(ctx, db.ListAccountProfileParams{
		Limit:  pgutil.Int32ToPgInt4(int32(cfg.AccountCount * 2)),
		Offset: pgutil.Int32ToPgInt4(0),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query back created profiles: %w", err)
	}

	// Match profiles with accounts by AccountID
	profileAccountMap := make(map[int64]db.AccountProfile)
	for _, profile := range profiles {
		profileAccountMap[profile.ID] = profile
	}

	// Populate data.Profiles with actual database records
	for _, account := range data.Accounts {
		if profile, exists := profileAccountMap[account.ID]; exists {
			data.Profiles = append(data.Profiles, profile)
		}
	}

	// Bulk create customers
	if len(customerAccountIDs) > 0 {
		customerParams := make([]db.CreateDefaultAccountCustomerParams, len(customerAccountIDs))
		for i, accountID := range customerAccountIDs {
			customerParams[i] = db.CreateDefaultAccountCustomerParams{
				ID: accountID,
			}
		}

		_, err = storage.CreateDefaultAccountCustomer(ctx, customerParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create customers: %w", err)
		}

		// Query back created customers
		customers, err := storage.ListAccountCustomer(ctx, db.ListAccountCustomerParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(customerAccountIDs) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created customers: %w", err)
		}

		// Match customers with account IDs
		customerAccountMap := make(map[int64]db.AccountCustomer)
		for _, customer := range customers {
			customerAccountMap[customer.ID] = customer
		}

		// Populate data.Customers with actual database records
		for _, accountID := range customerAccountIDs {
			if customer, exists := customerAccountMap[accountID]; exists {
				data.Customers = append(data.Customers, customer)
			}
		}
	}

	// Bulk create vendors
	if len(vendorAccountIDs) > 0 {
		vendorParams := make([]db.CreateAccountVendorParams, len(vendorAccountIDs))
		for i, accountID := range vendorAccountIDs {
			company := fake.Company()
			vendorParams[i] = db.CreateAccountVendorParams{
				ID:          accountID,
				Description: company.CatchPhrase(),
			}
		}

		_, err = storage.CreateAccountVendor(ctx, vendorParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create vendors: %w", err)
		}

		// Query back created vendors
		vendors, err := storage.ListAccountVendor(ctx, db.ListAccountVendorParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(vendorAccountIDs) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created vendors: %w", err)
		}

		// Match vendors with account IDs
		vendorAccountMap := make(map[int64]db.AccountVendor)
		for _, vendor := range vendors {
			vendorAccountMap[vendor.ID] = vendor
		}

		// Populate data.Vendors with actual database records
		for _, accountID := range vendorAccountIDs {
			if vendor, exists := vendorAccountMap[accountID]; exists {
				data.Vendors = append(data.Vendors, vendor)
			}
		}
	}

	// Prepare bulk address data
	var addressParams []db.CreateAccountAddressParams

	// Create addresses for customers (1-3 addresses each)
	for _, customer := range data.Customers {
		addressCount := fake.RandomDigit()%3 + 1
		for j := 0; j < addressCount; j++ {
			address := fake.Address()
			person := fake.Person()

			var addressType db.AccountAddressType
			if j == 0 {
				addressType = db.AccountAddressTypeHome
			} else {
				addressType = db.AccountAddressTypeWork
			}

			addressParams = append(addressParams, db.CreateAccountAddressParams{
				Code:          generateUniqueCodeWithTracker(fake, "ADDR", tracker),
				AccountID:     customer.ID,
				Type:          addressType,
				FullName:      person.Name(),
				Phone:         generateUniquePhoneWithTracker(fake, tracker),
				PhoneVerified: fake.Boolean().Bool(),
				AddressLine:   address.Address(),
				City:          address.City(),
				StateProvince: address.State(),
				Country:       address.CountryCode(),
				DateCreated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
				DateUpdated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
		}
	}

	// Create addresses for vendors (1-2 addresses each)
	for _, vendor := range data.Vendors {
		addressCount := fake.RandomDigit()%2 + 1
		for j := 0; j < addressCount; j++ {
			address := fake.Address()
			company := fake.Company()

			addressParams = append(addressParams, db.CreateAccountAddressParams{
				Code:          generateUniqueCodeWithTracker(fake, "ADDR", tracker),
				AccountID:     vendor.ID,
				Type:          db.AccountAddressTypeWork,
				FullName:      company.Name(),
				Phone:         generateUniquePhoneWithTracker(fake, tracker),
				PhoneVerified: fake.Boolean().Bool(),
				AddressLine:   address.Address(),
				City:          address.City(),
				StateProvince: address.State(),
				Country:       address.CountryCode(),
				DateCreated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
				DateUpdated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
		}
	}

	// Bulk insert addresses
	if len(addressParams) > 0 {
		_, err = storage.CreateAccountAddress(ctx, addressParams)
		if err != nil {
			return nil, fmt.Errorf("failed to bulk create addresses: %w", err)
		}

		// Query back created addresses
		addresses, err := storage.ListAccountAddress(ctx, db.ListAccountAddressParams{
			Limit:  pgutil.Int32ToPgInt4(int32(len(addressParams) * 2)),
			Offset: pgutil.Int32ToPgInt4(0),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query back created addresses: %w", err)
		}

		// Match addresses with our parameters by code (unique identifier)
		addressCodeMap := make(map[string]db.AccountAddress)
		for _, address := range addresses {
			addressCodeMap[address.Code] = address
		}

		// Populate data.Addresses with actual database records
		for _, params := range addressParams {
			if address, exists := addressCodeMap[params.Code]; exists {
				data.Addresses = append(data.Addresses, address)
			}
		}
	}

	// Create income histories for vendors
	if len(data.Vendors) > 0 {
		var incomeHistoryParams []db.CreateAccountIncomeHistoryParams

		for _, vendor := range data.Vendors {
			// Each vendor has 5-15 income history entries
			historyCount := fake.RandomDigit()%11 + 5
			currentBalance := int64(0)

			for j := 0; j < historyCount; j++ {
				incomeTypes := []string{"sale", "refund", "commission", "payout", "bonus"}
				incomeType := incomeTypes[fake.RandomDigit()%len(incomeTypes)]

				// Generate realistic income amounts
				var income int64
				switch incomeType {
				case "sale":
					income = int64(fake.RandomFloat(2, 100, 5000) * 100) // $1-$50
				case "refund":
					income = -int64(fake.RandomFloat(2, 10, 500) * 100) // -$0.10-$5
				case "commission":
					income = int64(fake.RandomFloat(2, 5, 200) * 100) // $0.05-$2
				case "payout":
					income = -int64(fake.RandomFloat(2, 50, 2000) * 100) // -$0.50-$20
				case "bonus":
					income = int64(fake.RandomFloat(2, 20, 1000) * 100) // $0.20-$10
				}

				currentBalance += income

				// Generate hash for this transaction
				hash := []byte(fake.Hash().SHA256())
				var prevHash []byte
				if j > 0 {
					prevHash = []byte(fake.Hash().SHA256()) // Simplified for seeding
				} else {
					prevHash = []byte("genesis")
				}

				incomeHistoryParams = append(incomeHistoryParams, db.CreateAccountIncomeHistoryParams{
					AccountID:      vendor.ID,
					Type:           incomeType,
					Income:         income,
					CurrentBalance: currentBalance,
					Note:           pgtype.Text{String: generateIncomeNote(fake, incomeType), Valid: true},
					DateCreated:    pgtype.Timestamptz{Time: time.Now().Add(-time.Duration(fake.RandomDigit()%365) * 24 * time.Hour), Valid: true},
					Hash:           hash,
					PrevHash:       prevHash,
				})
			}
		}

		// Bulk insert income histories
		if len(incomeHistoryParams) > 0 {
			_, err = storage.CreateAccountIncomeHistory(ctx, incomeHistoryParams)
			if err != nil {
				return nil, fmt.Errorf("failed to bulk create income histories: %w", err)
			}

			// Query back created income histories
			incomeHistories, err := storage.ListAccountIncomeHistory(ctx, db.ListAccountIncomeHistoryParams{
				Limit:  pgutil.Int32ToPgInt4(int32(len(incomeHistoryParams) * 2)),
				Offset: pgutil.Int32ToPgInt4(0),
			})
			if err != nil {
				return nil, fmt.Errorf("failed to query back created income histories: %w", err)
			}

			// Populate data.IncomeHistories with actual database records
			data.IncomeHistories = incomeHistories
		}
	}

	// Create notifications for all accounts
	if len(data.Accounts) > 0 {
		var notificationParams []db.CreateAccountNotificationParams

		for _, account := range data.Accounts {
			// Each account has 2-8 notifications
			notificationCount := fake.RandomDigit()%7 + 2

			for j := 0; j < notificationCount; j++ {
				notificationTypes := []string{"email", "sms", "push"}
				notificationChannels := []string{"order_update", "promotion", "system_alert", "payment", "refund"}

				notificationType := notificationTypes[fake.RandomDigit()%len(notificationTypes)]
				channel := notificationChannels[fake.RandomDigit()%len(notificationChannels)]

				// Generate notification content based on channel
				content := generateNotificationContent(fake, channel)

				// Some notifications are scheduled for future
				var dateScheduled *time.Time
				if fake.Boolean().Bool() {
					scheduledTime := time.Now().Add(time.Duration(fake.RandomDigit()%72) * time.Hour)
					dateScheduled = &scheduledTime
				}

				// Some notifications are already sent
				var dateSent *time.Time
				if fake.Boolean().Bool() {
					sentTime := time.Now().Add(-time.Duration(fake.RandomDigit()%168) * time.Hour)
					dateSent = &sentTime
				}

				notificationParams = append(notificationParams, db.CreateAccountNotificationParams{
					AccountID:     account.ID,
					Type:          notificationType,
					Channel:       channel,
					IsRead:        fake.Boolean().Bool(),
					Content:       content,
					DateCreated:   pgtype.Timestamptz{Time: time.Now().Add(-time.Duration(fake.RandomDigit()%720) * time.Hour), Valid: true},
					DateUpdated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
					DateSent:      pgtype.Timestamptz{Time: ptr.DerefDefault(dateSent, time.Time{}), Valid: dateSent != nil},
					DateScheduled: pgtype.Timestamptz{Time: ptr.DerefDefault(dateScheduled, time.Time{}), Valid: dateScheduled != nil},
				})
			}
		}

		// Bulk insert notifications
		if len(notificationParams) > 0 {
			_, err = storage.CreateAccountNotification(ctx, notificationParams)
			if err != nil {
				return nil, fmt.Errorf("failed to bulk create notifications: %w", err)
			}

			// Query back created notifications
			notifications, err := storage.ListAccountNotification(ctx, db.ListAccountNotificationParams{
				Limit:  pgutil.Int32ToPgInt4(int32(len(notificationParams) * 2)),
				Offset: pgutil.Int32ToPgInt4(0),
			})
			if err != nil {
				return nil, fmt.Errorf("failed to query back created notifications: %w", err)
			}

			// Populate data.Notifications with actual database records
			data.Notifications = notifications
		}
	}

	fmt.Printf("✅ Account schema seeded: %d accounts, %d customers, %d vendors, %d profiles, %d addresses, %d income histories, %d notifications\n",
		len(data.Accounts), len(data.Customers), len(data.Vendors), len(data.Profiles), len(data.Addresses), len(data.IncomeHistories), len(data.Notifications))

	return data, nil
}

// Helper functions for generating realistic data
func generateIncomeNote(fake *faker.Faker, incomeType string) string {
	switch incomeType {
	case "sale":
		return fmt.Sprintf("Sale of %s", fake.Lorem().Text(20))
	case "refund":
		return fmt.Sprintf("Refund for %s", fake.Lorem().Text(10))
	case "commission":
		return "Platform commission"
	case "payout":
		return "Monthly payout"
	case "bonus":
		return "Performance bonus"
	default:
		return "Transaction"
	}
}

func generateNotificationContent(fake *faker.Faker, channel string) string {
	switch channel {
	case "order_update":
		return fmt.Sprintf("Your order %s has been %s",
			fake.UUID().V4()[:8],
			[]string{"confirmed", "shipped", "delivered", "cancelled"}[fake.RandomDigit()%4])
	case "promotion":
		return fmt.Sprintf("New promotion: %s off on %s",
			[]string{"10%", "20%", "30%", "50%"}[fake.RandomDigit()%4],
			fake.Lorem().Text(10))
	case "system_alert":
		return fmt.Sprintf("System maintenance scheduled for %s",
			fake.Time().TimeBetween(time.Now(), time.Now().AddDate(0, 1, 0)).Format("2006-01-02"))
	case "payment":
		return fmt.Sprintf("Payment of $%.2f has been %s",
			fake.RandomFloat(2, 10, 500),
			[]string{"processed", "failed", "pending"}[fake.RandomDigit()%3])
	case "refund":
		return fmt.Sprintf("Refund of $%.2f has been %s",
			fake.RandomFloat(2, 5, 100),
			[]string{"processed", "pending"}[fake.RandomDigit()%2])
	default:
		return fake.Lorem().Sentence(3)
	}
}
