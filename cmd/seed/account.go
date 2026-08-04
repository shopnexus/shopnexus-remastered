package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"shopnexus/internal/module/common/dbx"
)

// seedAccounts is the whole cast. Every listing belongs to one of them and every purchase
// is made by another, so each account is both a shop and a customer — which is what a C2C
// marketplace is. The passwords are printed on a successful run; they are dev credentials
// and are meant to be.
var seedAccounts = []struct {
	Username string
	Email    string
	Password string
	Name     string
	Bio      string
	Phone    string
	City     string
	Province string
	Ward     string
	WardName string
}{
	{"alice_shop", "alice@shopnexus.test", "Alice@123", "Alice's Corner", "Pre-loved fashion and small home things.", "+84900000001", "Ho Chi Minh City", "79", "26743", "Ben Nghe"},
	{"bob_store", "bob@shopnexus.test", "Bob@12345", "Bob Electronics", "Second-hand phones, laptops and parts. Tested before listing.", "+84900000002", "Ha Noi", "01", "00001", "Phuc Xa"},
	{"carol_mart", "carol@shopnexus.test", "Carol@123", "Carol Mart", "Household goods, kitchenware and everything in between.", "+84900000003", "Da Nang", "48", "20194", "Thanh Binh"},
	{"dave_goods", "dave@shopnexus.test", "Dave@1234", "Dave Goods", "Outdoor, sports and hobby gear.", "+84900000004", "Can Tho", "92", "31117", "Cai Khe"},
	{"eve_bazaar", "eve@shopnexus.test", "Eve@12345", "Eve's Bazaar", "A bit of everything. Ask me about bundles.", "+84900000005", "Hai Phong", "31", "11365", "May To"},
}

// seller is a seeded account as the other three schemas need it: an id, and the contact
// snapshot that gets frozen into every order it is a party to.
type seller struct {
	id      int64
	address map[string]any
	// area is where this seller collects from, as catalog.listing snapshots it: a seeded listing
	// has to carry it or the browse feed's area filter and "near me" see nothing at all.
	area listingArea
}

// listingArea is the subset of a contact catalog keeps on a listing.
type listingArea struct {
	provinceCode string
	provinceName string
	wardCode     string
	wardName     string
}

// writeAccounts creates the cast and gives each one a contact that is both its delivery
// and its pickup address, because each of them buys and sells.
func writeAccounts(ctx context.Context, pool *pgxpool.Pool) ([]seller, error) {
	out := make([]seller, 0, len(seedAccounts))
	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		for _, a := range seedAccounts {
			hash, err := bcrypt.GenerateFromPassword([]byte(a.Password), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("hash password for %s: %w", a.Username, err)
			}
			const insertAccount = `
				INSERT INTO account (status, role, phone, email, username, password_hash,
				                     email_verified, name, description,
				                     country, locale, timezone)
				VALUES ('active', 'user', @phone, @email, @username, @password_hash,
				        true, @name, @description,
				        'VN', 'vi-VN', 'Asia/Ho_Chi_Minh')
				RETURNING id`
			var accountID int64
			err = tx.QueryRow(ctx, insertAccount, pgx.NamedArgs{
				"phone":         a.Phone,
				"email":         a.Email,
				"username":      a.Username,
				"password_hash": string(hash),
				"name":          a.Name,
				"description":   a.Bio,
			}).Scan(&accountID)
			if err != nil {
				return fmt.Errorf("insert account %s: %w", a.Username, err)
			}

			const insertContact = `
				INSERT INTO contact (account_id, full_name, phone, phone_verified, address_type,
				                     is_default_delivery, is_default_pickup, country,
				                     province_code, province_name, ward_code, ward_name, address)
				VALUES (@account_id, @full_name, @phone, true, 'home',
				        true, true, 'VN',
				        @province_code, @province_name, @ward_code, @ward_name, @address)`
			address := fmt.Sprintf("%d Nguyen Hue, %s", 10+accountID, a.WardName)
			_, err = tx.Exec(ctx, insertContact, pgx.NamedArgs{
				"account_id":    accountID,
				"full_name":     a.Name,
				"phone":         a.Phone,
				"province_code": a.Province,
				"province_name": a.City,
				"ward_code":     a.Ward,
				"ward_name":     a.WardName,
				"address":       address,
			})
			if err != nil {
				return fmt.Errorf("insert contact for %s: %w", a.Username, err)
			}

			// Shaped like order/domain.AddressSnapshot, which is itself shaped like the
			// contact row it is copied from.
			out = append(out, seller{
				id: accountID,
				address: map[string]any{
					"full_name":      a.Name,
					"phone":          a.Phone,
					"country":        "VN",
					"province_code":  a.Province,
					"ward_code":      a.Ward,
					"address_detail": address,
				},
				area: listingArea{
					provinceCode: a.Province, provinceName: a.City,
					wardCode: a.Ward, wardName: a.WardName,
				},
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// errAlreadySeeded is not a failure to report as one: re-running the seeder over live data
// would double every listing, and there is no safe way to tell a seeded row from a real one
// after the fact.
var errAlreadySeeded = errors.New("database already seeded; drop and re-migrate to start over")

func checkNotSeeded(ctx context.Context, pool *pgxpool.Pool) error {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM account WHERE email = @email)`,
		pgx.NamedArgs{"email": seedAccounts[0].Email},
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check for existing seed: %w", err)
	}
	if exists {
		return errAlreadySeeded
	}
	return nil
}
