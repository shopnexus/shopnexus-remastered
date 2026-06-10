package main

import (
	"context"
	"fmt"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/google/uuid"
	null "github.com/guregu/null/v6"
	"golang.org/x/crypto/bcrypt"

	accountdb "shopnexus-server/internal/module/account/db/sqlc"
)

type SeedAccount struct {
	ID       uuid.UUID
	Username string
	Email    string
	Password string // plaintext for logging
}

var seedAccounts = []SeedAccount{
	{Username: "alice_shop", Email: "alice@shopnexus.test", Password: "Alice@123"},
	{Username: "bob_store", Email: "bob@shopnexus.test", Password: "Bob@12345"},
	{Username: "carol_mart", Email: "carol@shopnexus.test", Password: "Carol@123"},
	{Username: "dave_goods", Email: "dave@shopnexus.test", Password: "Dave@1234"},
	{Username: "eve_bazaar", Email: "eve@shopnexus.test", Password: "Eve@12345"},
}

func hashPassword(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hashed), nil
}

func createAccounts(ctx context.Context, store *accountdb.Queries, fake *gofakeit.Faker) ([]SeedAccount, error) {
	for i := range seedAccounts {
		seedAccounts[i].ID = uuid.New()
		sa := seedAccounts[i]

		hashed, err := hashPassword(sa.Password)
		if err != nil {
			return nil, err
		}

		account, err := store.CreateAccount(ctx, accountdb.CreateAccountParams{
			ID:          sa.ID,
			Status:      accountdb.AccountStatusActive,
			Role:        accountdb.AccountRoleMember,
			Email:       null.StringFrom(sa.Email),
			Username:    null.StringFrom(sa.Username),
			Phone:       null.StringFrom(fake.Phone()),
			Password:    null.StringFrom(hashed),
			DateCreated: time.Now(),
		})
		if err != nil {
			return nil, fmt.Errorf("create account %s: %w", sa.Username, err)
		}

		// Create contact
		contact, err := store.CreateDefaultContact(ctx, accountdb.CreateDefaultContactParams{
			AccountID:   account.ID,
			FullName:    fake.Name(),
			Phone:       fake.Phone(),
			Address:     fmt.Sprintf("%s, %s, %s", fake.Street(), fake.City(), fake.Country()),
			AddressType: accountdb.AccountAddressTypeHome,
			Latitude:    fake.Latitude(),
			Longitude:   fake.Longitude(),
		})
		if err != nil {
			return nil, fmt.Errorf("create contact for %s: %w", sa.Username, err)
		}

		// Create profile
		gender := []accountdb.AccountGender{
			accountdb.AccountGenderMale,
			accountdb.AccountGenderFemale,
			accountdb.AccountGenderOther,
		}[fake.IntRange(0, 2)]

		_, err = store.CreateDefaultProfile(ctx, accountdb.CreateDefaultProfileParams{
			ID:      account.ID,
			Gender:  accountdb.NullAccountGender{AccountGender: gender, Valid: true},
			Name:    fake.Name(),
			Country: "VN",
			DateOfBirth: null.TimeFrom(
				fake.DateRange(
					time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
					time.Date(2003, 1, 1, 0, 0, 0, 0, time.UTC),
				),
			),
		})
		if err != nil {
			return nil, fmt.Errorf("create profile for %s: %w", sa.Username, err)
		}

		if err = store.SetAccountDefaultContact(ctx, accountdb.SetAccountDefaultContactParams{
			ID:               account.ID,
			DefaultContactID: uuid.NullUUID{UUID: contact.ID, Valid: true},
		}); err != nil {
			return nil, fmt.Errorf("set default contact for %s: %w", sa.Username, err)
		}
	}

	return seedAccounts, nil
}
