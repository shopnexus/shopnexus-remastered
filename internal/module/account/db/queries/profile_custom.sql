-- name: UpdateProfileCountry :execrows
UPDATE "account"."profile"
SET "country" = @country
WHERE "id" = @id AND "internal_balance" = 0;

-- name: CreateSignupProfile :one
INSERT INTO "account"."profile" ("id", "country", "name")
VALUES (@id, @country, @name)
RETURNING *;

-- name: SetAccountDefaultContact :exec
UPDATE "account"."profile"
SET "default_contact_id" = @default_contact_id
WHERE "id" = @id;

-- name: GetAccountDefaults :one
SELECT "default_contact_id" FROM "account"."profile" WHERE "id" = @id;
