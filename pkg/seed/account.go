package seed

import (
	"context"
	"fmt"
	"time"

	"shopnexus-remastered/internal/db"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jaswdr/faker/v2"
)

// AccountSeedData holds seeded account data for other seeders to reference
type AccountSeedData struct {
	Accounts  []db.AccountAccount
	Customers []db.AccountCustomer
	Vendors   []db.AccountVendor
	Profiles  []db.AccountProfile
	Addresses []db.AccountAddress
}

// SeedAccountSchema seeds the account schema with fake data
func SeedAccountSchema(ctx context.Context, storage db.Querier, fake *faker.Faker, cfg *SeedConfig) (*AccountSeedData, error) {
	fmt.Println("🏠 Seeding account schema...")

	data := &AccountSeedData{
		Accounts:  make([]db.AccountAccount, 0, cfg.AccountCount),
		Customers: make([]db.AccountCustomer, 0),
		Vendors:   make([]db.AccountVendor, 0),
		Profiles:  make([]db.AccountProfile, 0),
		Addresses: make([]db.AccountAddress, 0),
	}

	// Create accounts (mix of customers and vendors)
	for i := 0; i < cfg.AccountCount; i++ {
		person := fake.Person()

		var accountType db.AccountType
		if i%5 == 0 { // 20% vendors
			accountType = "Vendor"
		} else {
			accountType = "Customer"
		}

		account, err := retryWithUniqueValues(3, func(attempt int) (db.AccountAccount, error) {
			return storage.CreateAccount(ctx, db.CreateAccountParams{
				Code:        generateUniqueCode(fake, "ACC"),
				Type:        accountType,
				Status:      "ACTIVE",
				Phone:       pgtype.Text{String: generateUniquePhone(fake), Valid: true},
				Email:       pgtype.Text{String: generateUniqueEmail(fake), Valid: true},
				Username:    pgtype.Text{String: generateUniqueUsername(fake), Valid: true},
				Password:    pgtype.Text{String: fake.Hash().MD5(), Valid: true},
				DateCreated: pgtype.Timestamptz{Time: time.Now().Add(-time.Duration(fake.RandomDigit()%365) * 24 * time.Hour), Valid: true},
				DateUpdated: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create account %d: %w", i+1, err)
		}
		data.Accounts = append(data.Accounts, account)

		// Create profile for each account
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

		profile, err := storage.CreateProfile(ctx, db.CreateProfileParams{
			AccountID:     account.ID,
			Gender:        db.NullAccountGender{AccountGender: gender, Valid: true},
			Name:          pgtype.Text{String: person.Name(), Valid: true},
			DateOfBirth:   pgtype.Date{Time: birthDate, Valid: true},
			EmailVerified: fake.Boolean().Bool(),
			PhoneVerified: fake.Boolean().Bool(),
			DateCreated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
			DateUpdated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create profile for account %d: %w", account.ID, err)
		}
		data.Profiles = append(data.Profiles, profile)

		// Create customer or vendor profile
		if accountType == "Customer" {
			customer, err := storage.CreateDefaultCustomer(ctx, db.CreateDefaultCustomerParams{
				AccountID: account.ID,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create customer for account %d: %w", account.ID, err)
			}
			data.Customers = append(data.Customers, customer)

			// Create 1-3 addresses for each customer
			addressCount := fake.RandomDigit()%3 + 1
			for j := 0; j < addressCount; j++ {
				address := fake.Address()

				var addressType db.AccountAddressType
				if j == 0 {
					addressType = "HOME"
				} else {
					addressType = "WORK"
				}

				addr, err := retryWithUniqueValues(3, func(attempt int) (db.AccountAddress, error) {
					return storage.CreateAddress(ctx, db.CreateAddressParams{
						Code:          generateUniqueCode(fake, "ADDR"),
						AccountID:     account.ID,
						Type:          addressType,
						FullName:      person.Name(),
						Phone:         generateUniquePhone(fake),
						PhoneVerified: fake.Boolean().Bool(),
						AddressLine:   address.Address(),
						City:          address.City(),
						StateProvince: address.State(),
						Country:       address.CountryCode(),
						DateCreated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
						DateUpdated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
					})
				})
				if err != nil {
					return nil, fmt.Errorf("failed to create address for customer %d: %w", customer.ID, err)
				}
				data.Addresses = append(data.Addresses, addr)
			}
		} else {
			vendor, err := storage.CreateVendor(ctx, account.ID)
			if err != nil {
				return nil, fmt.Errorf("failed to create vendor for account %d: %w", account.ID, err)
			}
			data.Vendors = append(data.Vendors, vendor)

			// Create 1-2 addresses for each vendor
			addressCount := fake.RandomDigit()%2 + 1
			for j := 0; j < addressCount; j++ {
				address := fake.Address()
				company := fake.Company()

				addr, err := retryWithUniqueValues(3, func(attempt int) (db.AccountAddress, error) {
					return storage.CreateAddress(ctx, db.CreateAddressParams{
						Code:          generateUniqueCode(fake, "ADDR"),
						AccountID:     account.ID,
						Type:          "WORK",
						FullName:      company.Name(),
						Phone:         generateUniquePhone(fake),
						PhoneVerified: fake.Boolean().Bool(),
						AddressLine:   address.Address(),
						City:          address.City(),
						StateProvince: address.State(),
						Country:       address.CountryCode(),
						DateCreated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
						DateUpdated:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
					})
				})
				if err != nil {
					return nil, fmt.Errorf("failed to create address for vendor %d: %w", vendor.ID, err)
				}
				data.Addresses = append(data.Addresses, addr)
			}
		}
	}

	fmt.Printf("✅ Account schema seeded: %d accounts, %d customers, %d vendors, %d profiles, %d addresses\n",
		len(data.Accounts), len(data.Customers), len(data.Vendors), len(data.Profiles), len(data.Addresses))

	return data, nil
}
