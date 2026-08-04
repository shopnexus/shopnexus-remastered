-- The support desk's own account. It is the second side of every ticket thread, which is what lets
-- a ticket reuse the ordinary 1-1 conversation: the moderator who answers stays anonymous behind it,
-- the next one inherits the same thread, and staff share one read mark the way a support inbox does.
--
-- No password hash and no email, so nothing can sign in as it — the identifier is the username, and
-- the CHECK that an account has one is satisfied by that alone. Seeded here rather than by an
-- operator because the ticket route cannot work without it.
INSERT INTO "account" ("username", "name", "country", "locale", "timezone", "role")
VALUES ('support', 'Hỗ trợ ShopNexus', 'VN', 'vi-VN', 'Asia/Ho_Chi_Minh', 'user')
ON CONFLICT ("username") DO NOTHING;
