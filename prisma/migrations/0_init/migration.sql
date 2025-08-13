-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "account";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "payment";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "product";

-- CreateEnum
CREATE TYPE "account"."account_type" AS ENUM ('ACCOUNT_TYPE_USER', 'ACCOUNT_TYPE_ADMIN');

-- CreateEnum
CREATE TYPE "account"."gender" AS ENUM ('MALE', 'FEMALE', 'OTHER');

-- CreateEnum
CREATE TYPE "product"."sale_type" AS ENUM ('SALE_TYPE_TAG', 'SALE_TYPE_PRODUCT_MODEL', 'SALE_TYPE_BRAND');

-- CreateEnum
CREATE TYPE "product"."comment_type" AS ENUM ('PRODUCT_MODEL', 'BRAND', 'COMMENT');

-- CreateEnum
CREATE TYPE "payment"."payment_method" AS ENUM ('CASH', 'VNPAY', 'MOMO');

-- CreateEnum
CREATE TYPE "payment"."refund_method" AS ENUM ('PICK_UP', 'DROP_OFF');

-- CreateEnum
CREATE TYPE "payment"."status" AS ENUM ('PENDING', 'SUCCESS', 'CANCELED', 'FAILED');

-- CreateEnum
CREATE TYPE "product"."resource_type" AS ENUM ('BRAND', 'COMMENT', 'PRODUCT_MODEL', 'PRODUCT', 'REFUND');

-- CreateTable
CREATE TABLE "account"."base"
(
  "id"       BIGSERIAL                NOT NULL,
  "username" VARCHAR(100)             NOT NULL,
  "password" VARCHAR(255)             NOT NULL,
  "type"     "account"."account_type" NOT NULL,

  CONSTRAINT "base_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."role"
(
  "id"          VARCHAR(50) NOT NULL,
  "description" TEXT,

  CONSTRAINT "role_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."role_on_admin"
(
  "admin_id" BIGINT NOT NULL,
  "role_id"  TEXT   NOT NULL,

  CONSTRAINT "role_on_admin_pkey" PRIMARY KEY ("admin_id", "role_id")
);

-- CreateTable
CREATE TABLE "account"."permission"
(
  "id"          VARCHAR(100) NOT NULL,
  "description" TEXT,

  CONSTRAINT "permission_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."permission_on_role"
(
  "role_id"       TEXT NOT NULL,
  "permission_id" TEXT NOT NULL,

  CONSTRAINT "permission_on_role_pkey" PRIMARY KEY ("role_id", "permission_id")
);

-- CreateTable
CREATE TABLE "account"."user"
(
  "id"                 BIGINT             NOT NULL,
  "email"              VARCHAR(255)       NOT NULL,
  "phone"              VARCHAR(50)        NOT NULL,
  "gender"             "account"."gender" NOT NULL,
  "full_name"          VARCHAR(100)       NOT NULL DEFAULT '',
  "default_address_id" BIGINT,
  "avatar_url"         VARCHAR(500),

  CONSTRAINT "user_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."admin"
(
  "id"             BIGINT  NOT NULL,
  "avatar_url"     VARCHAR(255),
  "is_super_admin" BOOLEAN NOT NULL DEFAULT false,

  CONSTRAINT "admin_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."address"
(
  "id"           BIGSERIAL NOT NULL,
  "user_id"      BIGINT    NOT NULL,
  "full_name"    TEXT      NOT NULL,
  "phone"        TEXT      NOT NULL,
  "address"      TEXT      NOT NULL,
  "city"         TEXT      NOT NULL,
  "province"     TEXT      NOT NULL,
  "country"      TEXT      NOT NULL,
  "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

  CONSTRAINT "address_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."cart"
(
  "id" BIGINT NOT NULL,

  CONSTRAINT "cart_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."item_on_cart"
(
  "cart_id"      BIGINT NOT NULL,
  "product_id"   BIGINT NOT NULL,
  "quantity"     BIGINT NOT NULL,
  "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

  CONSTRAINT "item_on_cart_pkey" PRIMARY KEY ("cart_id", "product_id")
);

-- CreateTable
CREATE TABLE "product"."brand"
(
  "id"          BIGSERIAL NOT NULL,
  "name"        TEXT      NOT NULL,
  "description" TEXT      NOT NULL,

  CONSTRAINT "brand_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "product"."model"
(
  "id"                BIGSERIAL NOT NULL,
  "type"              BIGINT    NOT NULL,
  "brand_id"          BIGINT    NOT NULL,
  "name"              TEXT      NOT NULL,
  "description"       TEXT      NOT NULL,
  "list_price"        BIGINT    NOT NULL,
  "date_manufactured" TIMESTAMPTZ(3) NOT NULL,

  CONSTRAINT "model_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "product"."comment"
(
  "id"           BIGSERIAL                NOT NULL,
  "type"         "product"."comment_type" NOT NULL,
  "account_id"   BIGINT                   NOT NULL,
  "dest_id"      BIGINT                   NOT NULL,
  "body"         TEXT                     NOT NULL,
  "upvote"       BIGINT                   NOT NULL DEFAULT 0,
  "downvote"     BIGINT                   NOT NULL DEFAULT 0,
  "score"        INTEGER                  NOT NULL DEFAULT 0,
  "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

  CONSTRAINT "comment_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "product"."base"
(
  "id"               BIGSERIAL NOT NULL,
  "product_model_id" BIGINT    NOT NULL,
  "additional_price" BIGINT    NOT NULL DEFAULT 0,
  "is_active"        BOOLEAN   NOT NULL DEFAULT true,
  "can_combine"      BOOLEAN   NOT NULL DEFAULT false,
  "metadata"         JSONB     NOT NULL DEFAULT '{}',
  "date_created"     TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

  CONSTRAINT "base_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "product"."tracking"
(
  "product_id"    BIGINT NOT NULL,
  "current_stock" BIGINT NOT NULL,
  "sold"          BIGINT NOT NULL DEFAULT 0,

  CONSTRAINT "tracking_pkey" PRIMARY KEY ("product_id")
);

-- CreateTable
CREATE TABLE "product"."serial"
(
  "serial_id"    TEXT    NOT NULL,
  "product_id"   BIGINT  NOT NULL,
  "is_sold"      BOOLEAN NOT NULL DEFAULT false,
  "is_active"    BOOLEAN NOT NULL DEFAULT true,
  "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- CreateTable
CREATE TABLE "product"."type"
(
  "id"   BIGSERIAL NOT NULL,
  "name" TEXT      NOT NULL,

  CONSTRAINT "type_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "product"."sale"
(
  "id"                 BIGSERIAL             NOT NULL,
  "type"               "product"."sale_type" NOT NULL,
  "item_id"            BIGINT                NOT NULL,
  "date_created"       TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "date_started"       TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "date_ended"         TIMESTAMPTZ(3),
  "is_active"          BOOLEAN               NOT NULL DEFAULT true,
  "discount_percent"   INTEGER,
  "discount_price"     BIGINT,
  "max_discount_price" BIGINT                NOT NULL DEFAULT 0,

  CONSTRAINT "sale_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "product"."sale_tracking"
(
  "sale_id"       BIGINT NOT NULL,
  "current_stock" BIGINT NOT NULL DEFAULT 0,
  "used"          BIGINT NOT NULL,

  CONSTRAINT "sale_tracking_pkey" PRIMARY KEY ("sale_id")
);

-- CreateTable
CREATE TABLE "product"."tag_on_product_model"
(
  "product_model_id" BIGINT NOT NULL,
  "tag"              TEXT   NOT NULL,

  CONSTRAINT "tag_on_product_model_pkey" PRIMARY KEY ("product_model_id", "tag")
);

-- CreateTable
CREATE TABLE "product"."tag"
(
  "id"          BIGSERIAL   NOT NULL,
  "tag"         VARCHAR(50) NOT NULL,
  "description" TEXT        NOT NULL DEFAULT '',

  CONSTRAINT "tag_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "payment"."product_serial_on_product_on_payment"
(
  "product_on_payment_id" BIGINT NOT NULL,
  "product_serial_id"     TEXT   NOT NULL,

  CONSTRAINT "product_serial_on_product_on_payment_pkey" PRIMARY KEY ("product_on_payment_id", "product_serial_id")
);

-- CreateTable
CREATE TABLE "payment"."product_on_payment"
(
  "id"          BIGSERIAL NOT NULL,
  "payment_id"  BIGINT    NOT NULL,
  "product_id"  BIGINT    NOT NULL,
  "quantity"    BIGINT    NOT NULL,
  "price"       BIGINT    NOT NULL,
  "total_price" BIGINT    NOT NULL,

  CONSTRAINT "product_on_payment_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "payment"."base"
(
  "id"           BIGSERIAL                  NOT NULL,
  "user_id"      BIGINT                     NOT NULL,
  "method"       "payment"."payment_method" NOT NULL,
  "status"       "payment"."status"         NOT NULL,
  "address"      TEXT                       NOT NULL,
  "total"        BIGINT                     NOT NULL,
  "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

  CONSTRAINT "base_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "payment"."vnpay"
(
  "id"                    BIGINT NOT NULL,
  "vnp_Amount"            TEXT   NOT NULL,
  "vnp_BankCode"          TEXT   NOT NULL,
  "vnp_CardType"          TEXT   NOT NULL,
  "vnp_OrderInfo"         TEXT   NOT NULL,
  "vnp_PayDate"           TEXT   NOT NULL,
  "vnp_ResponseCode"      TEXT   NOT NULL,
  "vnp_SecureHash"        TEXT   NOT NULL,
  "vnp_TmnCode"           TEXT   NOT NULL,
  "vnp_TransactionNo"     TEXT   NOT NULL,
  "vnp_TransactionStatus" TEXT   NOT NULL,
  "vnp_TxnRef"            TEXT   NOT NULL,

  CONSTRAINT "vnpay_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "payment"."refund"
(
  "id"                    BIGSERIAL                 NOT NULL,
  "product_on_payment_id" BIGINT                    NOT NULL,
  "method"                "payment"."refund_method" NOT NULL,
  "status"                "payment"."status"        NOT NULL,
  "reason"                TEXT                      NOT NULL,
  "address"               TEXT                      NOT NULL,
  "amount"                BIGINT                    NOT NULL,
  "approved_by_id"        BIGINT,
  "date_created"          TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

  CONSTRAINT "refund_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "product"."resource"
(
  "id"       BIGSERIAL                 NOT NULL,
  "type"     "product"."resource_type" NOT NULL,
  "owner_id" BIGINT                    NOT NULL,
  "url"      TEXT                      NOT NULL,
  "order"    INTEGER                   NOT NULL,

  CONSTRAINT "resource_pkey" PRIMARY KEY ("id")
);

-- CreateIndex
CREATE UNIQUE INDEX "base_username_key" ON "account"."base" ("username");

-- CreateIndex
CREATE UNIQUE INDEX "user_email_key" ON "account"."user" ("email");

-- CreateIndex
CREATE UNIQUE INDEX "user_phone_key" ON "account"."user" ("phone");

-- CreateIndex
CREATE UNIQUE INDEX "comment_account_id_dest_id_key" ON "product"."comment" ("account_id", "dest_id");

-- CreateIndex
CREATE UNIQUE INDEX "serial_serial_id_key" ON "product"."serial" ("serial_id");

-- CreateIndex
CREATE UNIQUE INDEX "type_name_key" ON "product"."type" ("name");

-- CreateIndex
CREATE UNIQUE INDEX "tag_tag_key" ON "product"."tag" ("tag");

-- AddForeignKey
ALTER TABLE "account"."role_on_admin"
  ADD CONSTRAINT "role_on_admin_admin_id_fkey" FOREIGN KEY ("admin_id") REFERENCES "account"."admin" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."role_on_admin"
  ADD CONSTRAINT "role_on_admin_role_id_fkey" FOREIGN KEY ("role_id") REFERENCES "account"."role" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."permission_on_role"
  ADD CONSTRAINT "permission_on_role_role_id_fkey" FOREIGN KEY ("role_id") REFERENCES "account"."role" ("id") ON DELETE RESTRICT ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."permission_on_role"
  ADD CONSTRAINT "permission_on_role_permission_id_fkey" FOREIGN KEY ("permission_id") REFERENCES "account"."permission" ("id") ON DELETE RESTRICT ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."user"
  ADD CONSTRAINT "user_id_fkey" FOREIGN KEY ("id") REFERENCES "account"."base" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."admin"
  ADD CONSTRAINT "admin_id_fkey" FOREIGN KEY ("id") REFERENCES "account"."base" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."address"
  ADD CONSTRAINT "address_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "account"."user" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."cart"
  ADD CONSTRAINT "cart_id_fkey" FOREIGN KEY ("id") REFERENCES "account"."user" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."item_on_cart"
  ADD CONSTRAINT "item_on_cart_cart_id_fkey" FOREIGN KEY ("cart_id") REFERENCES "account"."cart" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."item_on_cart"
  ADD CONSTRAINT "item_on_cart_product_id_fkey" FOREIGN KEY ("product_id") REFERENCES "product"."base" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "product"."model"
  ADD CONSTRAINT "model_type_fkey" FOREIGN KEY ("type") REFERENCES "product"."type" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "product"."model"
  ADD CONSTRAINT "model_brand_id_fkey" FOREIGN KEY ("brand_id") REFERENCES "product"."brand" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "product"."comment"
  ADD CONSTRAINT "comment_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."base" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "product"."base"
  ADD CONSTRAINT "base_product_model_id_fkey" FOREIGN KEY ("product_model_id") REFERENCES "product"."model" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "product"."tracking"
  ADD CONSTRAINT "tracking_product_id_fkey" FOREIGN KEY ("product_id") REFERENCES "product"."base" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "product"."serial"
  ADD CONSTRAINT "serial_product_id_fkey" FOREIGN KEY ("product_id") REFERENCES "product"."base" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "product"."sale_tracking"
  ADD CONSTRAINT "sale_tracking_sale_id_fkey" FOREIGN KEY ("sale_id") REFERENCES "product"."sale" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "product"."tag_on_product_model"
  ADD CONSTRAINT "tag_on_product_model_product_model_id_fkey" FOREIGN KEY ("product_model_id") REFERENCES "product"."model" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "product"."tag_on_product_model"
  ADD CONSTRAINT "tag_on_product_model_tag_fkey" FOREIGN KEY ("tag") REFERENCES "product"."tag" ("tag") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."product_serial_on_product_on_payment"
  ADD CONSTRAINT "product_serial_on_product_on_payment_product_on_payment_id_fkey" FOREIGN KEY ("product_on_payment_id") REFERENCES "payment"."product_on_payment" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."product_serial_on_product_on_payment"
  ADD CONSTRAINT "product_serial_on_product_on_payment_product_serial_id_fkey" FOREIGN KEY ("product_serial_id") REFERENCES "product"."serial" ("serial_id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."product_on_payment"
  ADD CONSTRAINT "product_on_payment_payment_id_fkey" FOREIGN KEY ("payment_id") REFERENCES "payment"."base" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."product_on_payment"
  ADD CONSTRAINT "product_on_payment_product_id_fkey" FOREIGN KEY ("product_id") REFERENCES "product"."base" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."base"
  ADD CONSTRAINT "base_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "account"."user" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."vnpay"
  ADD CONSTRAINT "vnpay_id_fkey" FOREIGN KEY ("id") REFERENCES "payment"."base" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."refund"
  ADD CONSTRAINT "refund_product_on_payment_id_fkey" FOREIGN KEY ("product_on_payment_id") REFERENCES "payment"."product_on_payment" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."refund"
  ADD CONSTRAINT "refund_approved_by_id_fkey" FOREIGN KEY ("approved_by_id") REFERENCES "account"."admin" ("id") ON DELETE CASCADE ON UPDATE CASCADE;

