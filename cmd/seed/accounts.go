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

// seedAccount is one member of the demo cast.
//
// Protected marks the three accounts the graduation report signs in as. The seeder never
// rewrites them: if the row is already there it is used exactly as found — same password, same
// email, same username, same display name — and only data is hung off it. If it is *not* there
// the seeder creates it, because a screenshot of an empty buyer account is worse than a
// documented dev password. The wipe never deletes them.
type seedAccount struct {
	Key      string // stable handle used by dataset.json and by the plan
	Username string
	Email    string
	// Password is used only when the seeder has to create the row. An existing account keeps
	// whatever password it already had.
	Password     string
	Role         string // account_role: 'user' or 'admin'
	Name         string
	Bio          string
	Phone        string
	ProvinceCode string
	ProvinceName string
	WardCode     string
	WardName     string
	Street       string
	Protected    bool
}

// seedAccounts is the whole cast. Every listing belongs to one of them and every purchase is
// made by another, which is what a C2C marketplace is: there is no "shop" table because a shop
// is an account that happens to have listings.
//
// The three protected rows come first so that a reader of the log sees them first.
var seedAccounts = []seedAccount{
	{
		Key: "khoa_buyer", Username: "khoakomlem", Email: "khoakomlem@gmail.com",
		Password: "Khoa@12345", Role: "user", Protected: true,
		Name:  "Khoa Nguyễn",
		Bio:   "Mua bán đồ công nghệ và đồ dùng cá nhân. Nhắn tin trước khi đặt nhé.",
		Phone: "+84901234567", ProvinceCode: "01", ProvinceName: "Hà Nội",
		WardCode: "00001", WardName: "Phường Phúc Xá", Street: "48 Nghĩa Dũng",
	},
	{
		Key: "bob_store", Username: "bob_store", Email: "bob@shopnexus.test",
		Password: "Bob@12345", Role: "user", Protected: true,
		Name:  "Bob Mobile - Điện thoại & Laptop cũ",
		Bio:   "Điện thoại, laptop, máy tính bảng đã qua sử dụng. Máy nào cũng test kỹ trước khi đăng, có ảnh thật, không dùng ảnh mạng.",
		Phone: "+84902345678", ProvinceCode: "01", ProvinceName: "Hà Nội",
		WardCode: "00019", WardName: "Phường Dịch Vọng", Street: "112 Trần Thái Tông",
	},
	{
		Key: "admin", Username: "admin", Email: "admin@shopnexus.test",
		Password: "Admin@12345", Role: "admin", Protected: true,
		Name:  "Quản trị ShopNexus",
		Bio:   "Tài khoản quản trị hệ thống.",
		Phone: "+84903456789", ProvinceCode: "01", ProvinceName: "Hà Nội",
		WardCode: "00019", WardName: "Phường Dịch Vọng", Street: "112 Trần Thái Tông",
	},

	// Everything below is created by the seeder and removed by the wipe.
	{
		Key: "huyen_camera", Username: "huyen_camera", Email: "huyen.camera@shopnexus.test",
		Password: "Huyen@12345", Role: "user",
		Name:  "Huyền Camera Studio",
		Bio:   "Máy ảnh, ống kính và đồ chơi nhiếp ảnh. Đồ nào cũng để tủ chống ẩm, có thể qua studio ở quận 3 xem trực tiếp.",
		Phone: "+84904567890", ProvinceCode: "79", ProvinceName: "TP Hồ Chí Minh",
		WardCode: "26743", WardName: "Phường Bến Nghé", Street: "27 Nguyễn Thị Minh Khai",
	},
	{
		Key: "tuan_sport", Username: "tuan_sport", Email: "tuan.sport@shopnexus.test",
		Password: "Tuan@12345", Role: "user",
		Name:  "Tuấn Sport Secondhand",
		Bio:   "Xe đạp, đồ tập, đồ dã ngoại. Ưu tiên bạn nào qua xem và chạy thử trực tiếp.",
		Phone: "+84905678901", ProvinceCode: "79", ProvinceName: "TP Hồ Chí Minh",
		WardCode: "27154", WardName: "Phường 12", Street: "9 Điện Biên Phủ",
	},
	{
		Key: "linh_home", Username: "linh_home", Email: "linh.home@shopnexus.test",
		Password: "Linh@12345", Role: "user",
		Name:  "Nhà Của Linh",
		Bio:   "Đồ gia dụng và nội thất nhà mình dùng rồi, còn tốt thì để lại. Đồ nào cũng lau rửa sạch trước khi chụp.",
		Phone: "+84906789012", ProvinceCode: "48", ProvinceName: "Đà Nẵng",
		WardCode: "20194", WardName: "Phường Thanh Bình", Street: "35 Ông Ích Khiêm",
	},
	{
		Key: "sach_cu_hn", Username: "sach_cu_hn", Email: "sachcu.hanoi@shopnexus.test",
		Password: "Sach@12345", Role: "user",
		Name:  "Sách Cũ Hà Nội",
		Bio:   "Sách cũ, truyện tranh, giáo trình. Sách nào cũng kiểm tra đủ trang trước khi gửi, bọc nilon cẩn thận.",
		Phone: "+84907890123", ProvinceCode: "01", ProvinceName: "Hà Nội",
		WardCode: "00109", WardName: "Phường Láng Hạ", Street: "182 Láng Hạ",
	},
	{
		Key: "minh_pham", Username: "minh_pham", Email: "minh.pham@shopnexus.test",
		Password: "Minh@12345", Role: "user",
		Name:  "Phạm Nhật Minh",
		Bio:   "Thanh lý đồ trong nhà, mỗi thứ một ít.",
		Phone: "+84908901234", ProvinceCode: "92", ProvinceName: "Cần Thơ",
		WardCode: "31117", WardName: "Phường Cái Khế", Street: "21 Trần Văn Khéo",
	},
	{
		Key: "thao_le", Username: "thao_le", Email: "thao.le@shopnexus.test",
		Password: "Thao@12345", Role: "user",
		Name:  "Lê Phương Thảo",
		Bio:   "Thời trang, phụ kiện và đồ mẹ bé. Đồ nhà mình dùng rồi, mô tả đúng tình trạng.",
		Phone: "+84909012345", ProvinceCode: "79", ProvinceName: "TP Hồ Chí Minh",
		WardCode: "27289", WardName: "Phường 7", Street: "68 Phan Xích Long",
	},
	{
		Key: "duc_tran", Username: "duc_tran", Email: "duc.tran@shopnexus.test",
		Password: "Duc@123456", Role: "user",
		Name:  "Trần Minh Đức",
		Bio:   "Build và thanh lý máy tính. Máy nào bán cũng cho chạy thử thoải mái.",
		Phone: "+84910123456", ProvinceCode: "79", ProvinceName: "TP Hồ Chí Minh",
		WardCode: "27478", WardName: "Phường Linh Trung", Street: "3 Võ Văn Ngân",
	},
	{
		Key: "nga_vo", Username: "nga_vo", Email: "nga.vo@shopnexus.test",
		Password: "Nga@123456", Role: "user",
		Name:  "Võ Thanh Nga",
		Bio:   "Đồ gia dụng, đồ cho bé và vài món cá nhân. Nhắn tin mình trả lời nhanh.",
		Phone: "+84911234567", ProvinceCode: "31", ProvinceName: "Hải Phòng",
		WardCode: "11365", WardName: "Phường Máy Tơ", Street: "14 Lê Thánh Tông",
	},
	{
		Key: "an_nguyen", Username: "an_nguyen", Email: "an.nguyen@shopnexus.test",
		Password: "An@1234567", Role: "user",
		Name:  "Nguyễn Hoài An",
		Bio:   "Đồ công nghệ và đồ du lịch. Ship toàn quốc, cho xem hàng trước khi thanh toán.",
		Phone: "+84912345678", ProvinceCode: "48", ProvinceName: "Đà Nẵng",
		WardCode: "20263", WardName: "Phường Mỹ An", Street: "72 Ngũ Hành Sơn",
	},
}

// seedMarkerEmail is the row whose presence means "this database has already been seeded". It
// is deliberately one of the *deletable* accounts and not one of the protected three: the
// protected rows may exist because a person registered them, which is not the same fact.
const seedMarkerEmail = "huyen.camera@shopnexus.test"

// party is a seeded account as the other five schemas need it: an id, the contact snapshot
// frozen into every order it is a party to, and the area a listing of its copies.
type party struct {
	seedAccount
	id int64
	// created is false when the row was already there. The wipe reads it back from the
	// protected flag rather than from here, but the log line is worth having.
	created bool
	address map[string]any
	area    listingArea
}

// listingArea is the subset of a contact that catalog keeps on a listing.
type listingArea struct {
	provinceCode string
	provinceName string
	wardCode     string
	wardName     string
}

// writeAccounts resolves the cast against whatever is already in the database. An account that
// exists is used as found and never updated — the three the report signs in as are somebody's
// real credentials, and a seeder that "fixes" a display name is a seeder that logs you out of
// your own screenshots. An account that does not exist is created.
//
// Each one gets a contact that is both its delivery and its pickup address, because each of
// them buys and sells. If it already has a default pickup contact, that one is used: the
// partial unique index allows exactly one, so inserting a second would fail.
func writeAccounts(ctx context.Context, pool *pgxpool.Pool) ([]party, error) {
	out := make([]party, 0, len(seedAccounts))
	err := dbx.InTx(ctx, pool, func(tx pgx.Tx) error {
		for _, a := range seedAccounts {
			p := party{seedAccount: a}

			const findAccount = `
				SELECT id FROM account
				WHERE username = @username OR (@email::text IS NOT NULL AND email = @email)
				LIMIT 1`
			err := tx.QueryRow(ctx, findAccount, pgx.NamedArgs{
				"username": a.Username, "email": a.Email,
			}).Scan(&p.id)
			switch {
			case err == nil:
				// Found. Nothing is written to the row itself.
			case errors.Is(err, pgx.ErrNoRows):
				id, err := insertAccount(ctx, tx, a)
				if err != nil {
					return err
				}
				p.id, p.created = id, true
			default:
				return fmt.Errorf("look up account %s: %w", a.Username, err)
			}

			if err := ensureContact(ctx, tx, p.id, a); err != nil {
				return err
			}
			p.address = map[string]any{
				"full_name":      a.Name,
				"phone":          a.Phone,
				"country":        "VN",
				"province_code":  a.ProvinceCode,
				"ward_code":      a.WardCode,
				"address_detail": a.Street,
			}
			p.area = listingArea{
				provinceCode: a.ProvinceCode, provinceName: a.ProvinceName,
				wardCode: a.WardCode, wardName: a.WardName,
			}
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func insertAccount(ctx context.Context, tx pgx.Tx, a seedAccount) (int64, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(a.Password), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf("hash password for %s: %w", a.Username, err)
	}
	const q = `
		INSERT INTO account (status, role, phone, email, username, password_hash,
		                     email_verified, name, description,
		                     country, locale, timezone)
		VALUES ('active', @role, @phone, @email, @username, @password_hash,
		        true, @name, @description,
		        'VN', 'vi-VN', 'Asia/Ho_Chi_Minh')
		RETURNING id`
	var id int64
	err = tx.QueryRow(ctx, q, pgx.NamedArgs{
		"role":          a.Role,
		"phone":         a.Phone,
		"email":         a.Email,
		"username":      a.Username,
		"password_hash": string(hash),
		"name":          a.Name,
		"description":   a.Bio,
	}).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert account %s: %w", a.Username, err)
	}
	return id, nil
}

// ensureContact gives the account a default pickup/delivery address if it has none. A listing
// snapshots the seller's pickup contact when it is published, so an account without one cannot
// have a listing the browse feed's area filter can see.
func ensureContact(ctx context.Context, tx pgx.Tx, accountID int64, a seedAccount) error {
	var exists bool
	const has = `SELECT EXISTS (SELECT 1 FROM contact WHERE account_id = @id AND is_default_pickup)`
	if err := tx.QueryRow(ctx, has, pgx.NamedArgs{"id": accountID}).Scan(&exists); err != nil {
		return fmt.Errorf("check contact for %s: %w", a.Username, err)
	}
	if exists {
		return nil
	}
	const q = `
		INSERT INTO contact (account_id, full_name, phone, phone_verified, address_type,
		                     is_default_delivery, is_default_pickup, country,
		                     province_code, province_name, ward_code, ward_name, address)
		VALUES (@account_id, @full_name, @phone, true, 'home',
		        NOT EXISTS (SELECT 1 FROM contact WHERE account_id = @account_id AND is_default_delivery),
		        true, 'VN',
		        @province_code, @province_name, @ward_code, @ward_name, @address)`
	_, err := tx.Exec(ctx, q, pgx.NamedArgs{
		"account_id":    accountID,
		"full_name":     a.Name,
		"phone":         a.Phone,
		"province_code": a.ProvinceCode,
		"province_name": a.ProvinceName,
		"ward_code":     a.WardCode,
		"ward_name":     a.WardName,
		"address":       a.Street,
	})
	if err != nil {
		return fmt.Errorf("insert contact for %s: %w", a.Username, err)
	}
	return nil
}

// supportDeskID is the account every ticket thread's other side is. Bootstrap, seeded by
// account's own migration 004 — looked up by role and never by username, because a username is
// something a user can register.
func supportDeskID(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var id int64
	err := pool.QueryRow(ctx, `SELECT id FROM account WHERE role = 'support'`).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("find support desk account (is the database migrated?): %w", err)
	}
	return id, nil
}

// errAlreadySeeded is not a failure to report as one: re-running the seeder over data that is
// already there would double every listing. Wipe first — that is what `-wipe` is for.
var errAlreadySeeded = errors.New(
	"database already seeded; run `seed -wipe -yes-i-mean-it` first, then seed again")

func checkNotSeeded(ctx context.Context, pool *pgxpool.Pool) error {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM account WHERE email = @email)`,
		pgx.NamedArgs{"email": seedMarkerEmail},
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check for existing seed: %w", err)
	}
	if exists {
		return errAlreadySeeded
	}
	return nil
}

// deletableIdentifiers is what the wipe matches an account row on: everything in the cast that
// is not one of the three the report signs in as. Identifiers rather than ids, so the wipe can
// run without the seeder's plan in hand.
func deletableIdentifiers() (usernames, emails []string) {
	for _, a := range seedAccounts {
		if a.Protected {
			continue
		}
		usernames = append(usernames, a.Username)
		emails = append(emails, a.Email)
	}
	return usernames, emails
}

func protectedIdentifiers() (usernames, emails []string) {
	for _, a := range seedAccounts {
		if !a.Protected {
			continue
		}
		usernames = append(usernames, a.Username)
		emails = append(emails, a.Email)
	}
	return usernames, emails
}
