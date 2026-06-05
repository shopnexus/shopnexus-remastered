-- Role-based access control. 'Admin' is granted manually by ops; new signups
-- default to 'Member'.

CREATE TYPE "account"."role" AS ENUM ('Member', 'Admin');

ALTER TABLE "account"."account"
    ADD COLUMN "role" "account"."role" NOT NULL DEFAULT 'Member';
