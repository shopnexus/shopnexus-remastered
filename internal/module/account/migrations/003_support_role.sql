-- The support desk's own role. 002 seeded the desk as a plain user, where nothing but its username
-- told it apart — and a username is something a user can register, so whoever held it answered
-- "who is support" and became a side of every ticket thread on the platform.
--
-- Its own file because a new enum value cannot be used in the transaction that adds it: 004 is what
-- indexes the value and puts it on the row.
ALTER TYPE "account_role" ADD VALUE IF NOT EXISTS 'support';
