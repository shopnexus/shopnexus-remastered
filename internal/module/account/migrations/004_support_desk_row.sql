-- Put the desk's role on the desk's row, and make that role the way it is found.

-- One row may hold it, so "who is support" is a key lookup with exactly one answer rather than a
-- query whose result depends on which of two rows sorts first.
CREATE UNIQUE INDEX IF NOT EXISTS "account_support_role_key"
    ON "account" ("role")
    WHERE "role" = 'support';

-- Promote the row 002 seeded. It is the only account with no way to sign in — no password hash and
-- no linked provider — which is what tells it apart from a user who registered the same username;
-- the domain refuses to write any other account in that state.
UPDATE "account" SET "role" = 'support'
WHERE "username" = 'support'
  AND "password_hash" IS NULL
  AND NOT EXISTS (SELECT 1 FROM "oauth_identity" WHERE "account_id" = "account"."id");

-- And seed it where 002 found the username already taken and did nothing. No ON CONFLICT: if a user
-- holds that name this fails and an operator renames them, because the alternatives are a
-- deployment with no desk at all or one whose desk is somebody's account.
INSERT INTO "account" ("username", "name", "country", "locale", "timezone", "role")
SELECT 'support', 'Hỗ trợ ShopNexus', 'VN', 'vi-VN', 'Asia/Ho_Chi_Minh', 'support'
WHERE NOT EXISTS (SELECT 1 FROM "account" WHERE "role" = 'support');
