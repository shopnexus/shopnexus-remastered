-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "account";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "catalog";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "inventory";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "order";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "promotion";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "shared";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "system";

-- CreateEnum
CREATE TYPE "account"."type" AS ENUM ('Customer', 'Vendor');

-- CreateEnum
CREATE TYPE "account"."status" AS ENUM ('Active', 'Suspended');

-- CreateEnum
CREATE TYPE "account"."gender" AS ENUM ('Male', 'Female', 'Other');

-- CreateEnum
CREATE TYPE "account"."address_type" AS ENUM ('Home', 'Work');

-- CreateEnum
CREATE TYPE "catalog"."comment_ref_type" AS ENUM ('ProductSPU', 'Comment');

-- CreateEnum
CREATE TYPE "inventory"."stock_type" AS ENUM ('ProductSKU', 'Promotion');

-- CreateEnum
CREATE TYPE "inventory"."product_status" AS ENUM ('Active', 'Inactive', 'Sold', 'Damaged');

-- CreateEnum
CREATE TYPE "order"."payment_method" AS ENUM ('COD', 'Card', 'EWallet', 'Crypto');

-- CreateEnum
CREATE TYPE "order"."refund_method" AS ENUM ('PickUp', 'DropOff');

-- CreateEnum
CREATE TYPE "order"."invoice_type" AS ENUM ('Sale', 'Service', 'Adjustment');

-- CreateEnum
CREATE TYPE "order"."invoice_ref_type" AS ENUM ('Order', 'Fee');

-- CreateEnum
CREATE TYPE "promotion"."type" AS ENUM ('Discount', 'Bundle', 'BuyXGetY', 'Cashback');

-- CreateEnum
CREATE TYPE "promotion"."ref_type" AS ENUM ('All', 'ProductSpu', 'ProductSku', 'Category', 'Brand');

-- CreateEnum
CREATE TYPE "shared"."resource_type" AS ENUM ('Account', 'ProductSpu', 'ProductSku', 'Brand', 'Refund', 'ReturnDispute');

-- CreateEnum
CREATE TYPE "shared"."status" AS ENUM ('Pending', 'Processing', 'Success', 'Canceled', 'Failed');

-- CreateEnum
CREATE TYPE "system"."event_type" AS ENUM ('Created', 'Updated', 'Deleted');

-- CreateTable
CREATE TABLE "account"."base" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "type" "account"."type" NOT NULL,
    "status" "account"."status" NOT NULL DEFAULT 'Active',
    "phone" VARCHAR(50),
    "email" VARCHAR(255),
    "username" VARCHAR(100),
    "password" VARCHAR(255),
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "base_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."profile" (
    "id" BIGINT NOT NULL,
    "gender" "account"."gender",
    "name" VARCHAR(100),
    "date_of_birth" DATE,
    "avatar_rs_id" BIGINT,
    "email_verified" BOOLEAN NOT NULL DEFAULT false,
    "phone_verified" BOOLEAN NOT NULL DEFAULT false,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "profile_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."customer" (
    "id" BIGINT NOT NULL,
    "default_address_id" BIGINT,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "customer_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."vendor" (
    "id" BIGINT NOT NULL,
    "description" VARCHAR(255) NOT NULL DEFAULT '',

    CONSTRAINT "vendor_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."income_history" (
    "id" BIGSERIAL NOT NULL,
    "account_id" BIGINT NOT NULL,
    "type" VARCHAR(50) NOT NULL,
    "income" BIGINT NOT NULL,
    "current_balance" BIGINT NOT NULL,
    "note" VARCHAR(100),
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "hash" BYTEA NOT NULL,
    "prev_hash" BYTEA NOT NULL,

    CONSTRAINT "income_history_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."notification" (
    "id" BIGSERIAL NOT NULL,
    "account_id" BIGINT NOT NULL,
    "type" VARCHAR(50) NOT NULL,
    "channel" VARCHAR(50) NOT NULL,
    "is_read" BOOLEAN NOT NULL DEFAULT false,
    "content" TEXT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_sent" TIMESTAMPTZ(3),
    "date_scheduled" TIMESTAMPTZ(3),

    CONSTRAINT "notification_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."cart_item" (
    "id" BIGSERIAL NOT NULL,
    "cart_id" BIGINT NOT NULL,
    "sku_id" BIGINT NOT NULL,
    "quantity" BIGINT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "cart_item_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."address" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "account_id" BIGINT NOT NULL,
    "type" "account"."address_type" NOT NULL DEFAULT 'Home',
    "full_name" VARCHAR(100) NOT NULL,
    "phone" VARCHAR(30) NOT NULL,
    "phone_verified" BOOLEAN NOT NULL DEFAULT false,
    "address_line" VARCHAR(255) NOT NULL,
    "city" VARCHAR(100) NOT NULL,
    "state_province" VARCHAR(100) NOT NULL,
    "country" VARCHAR(2) NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "address_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."brand" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "name" TEXT NOT NULL,
    "description" TEXT NOT NULL,

    CONSTRAINT "brand_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."category" (
    "id" BIGSERIAL NOT NULL,
    "name" VARCHAR(100) NOT NULL,
    "description" TEXT NOT NULL DEFAULT '',
    "parent_id" BIGINT,

    CONSTRAINT "category_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."product_spu" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "account_id" BIGINT NOT NULL,
    "category_id" BIGINT NOT NULL,
    "brand_id" BIGINT NOT NULL,
    "name" TEXT NOT NULL,
    "description" TEXT NOT NULL,
    "is_active" BOOLEAN NOT NULL DEFAULT true,
    "date_manufactured" TIMESTAMPTZ(3) NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_deleted" TIMESTAMPTZ(3),

    CONSTRAINT "product_spu_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."product_sku" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "spu_id" BIGINT NOT NULL,
    "price" BIGINT NOT NULL,
    "can_combine" BOOLEAN NOT NULL DEFAULT false,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_deleted" TIMESTAMPTZ(3),

    CONSTRAINT "product_sku_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."product_sku_attribute" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "sku_id" BIGINT NOT NULL,
    "name" VARCHAR(100) NOT NULL,
    "value" VARCHAR(255) NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "product_sku_attribute_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."tag" (
    "id" BIGSERIAL NOT NULL,
    "tag" VARCHAR(50) NOT NULL,
    "description" TEXT NOT NULL DEFAULT '',

    CONSTRAINT "tag_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."product_spu_tag" (
    "id" BIGSERIAL NOT NULL,
    "spu_id" BIGINT NOT NULL,
    "tag_id" BIGINT NOT NULL,

    CONSTRAINT "product_spu_tag_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."comment" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "account_id" BIGINT NOT NULL,
    "ref_type" "catalog"."comment_ref_type" NOT NULL,
    "ref_id" BIGINT NOT NULL,
    "body" TEXT NOT NULL,
    "upvote" BIGINT NOT NULL DEFAULT 0,
    "downvote" BIGINT NOT NULL DEFAULT 0,
    "score" INTEGER NOT NULL DEFAULT 0,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "comment_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "inventory"."sku_serial" (
    "id" BIGSERIAL NOT NULL,
    "serial_number" VARCHAR(50) NOT NULL,
    "sku_id" BIGINT NOT NULL,
    "status" "inventory"."product_status" NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "sku_serial_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "inventory"."stock" (
    "id" BIGSERIAL NOT NULL,
    "ref_type" "inventory"."stock_type" NOT NULL,
    "ref_id" BIGINT NOT NULL,
    "current_stock" BIGINT NOT NULL DEFAULT 0,
    "sold" BIGINT NOT NULL DEFAULT 0,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "stock_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "inventory"."stock_history" (
    "id" BIGSERIAL NOT NULL,
    "stock_id" BIGINT NOT NULL,
    "change" BIGINT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "stock_history_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "order"."base" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "customer_id" BIGINT NOT NULL,
    "payment_method" "order"."payment_method" NOT NULL,
    "status" "shared"."status" NOT NULL,
    "address" TEXT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL,

    CONSTRAINT "base_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "order"."item" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "order_id" BIGINT NOT NULL,
    "sku_id" BIGINT NOT NULL,
    "quantity" BIGINT NOT NULL,

    CONSTRAINT "item_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "order"."item_serial" (
    "id" BIGSERIAL NOT NULL,
    "order_item_id" BIGINT NOT NULL,
    "product_serial_id" BIGINT NOT NULL,

    CONSTRAINT "item_serial_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "order"."vnpay" (
    "id" BIGINT NOT NULL,
    "vnp_Amount" TEXT NOT NULL,
    "vnp_BankCode" TEXT NOT NULL,
    "vnp_CardType" TEXT NOT NULL,
    "vnp_OrderInfo" TEXT NOT NULL,
    "vnp_PayDate" TEXT NOT NULL,
    "vnp_ResponseCode" TEXT NOT NULL,
    "vnp_SecureHash" TEXT NOT NULL,
    "vnp_TmnCode" TEXT NOT NULL,
    "vnp_TransactionNo" TEXT NOT NULL,
    "vnp_TransactionStatus" TEXT NOT NULL,
    "vnp_TxnRef" TEXT NOT NULL,

    CONSTRAINT "vnpay_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "order"."refund" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "order_item_id" BIGINT NOT NULL,
    "reviewed_by_id" BIGINT,
    "method" "order"."refund_method" NOT NULL,
    "status" "shared"."status" NOT NULL,
    "reason" TEXT NOT NULL,
    "address" TEXT,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "refund_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "order"."refund_dispute" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "refund_id" BIGINT NOT NULL,
    "issued_by_id" BIGINT NOT NULL,
    "reason" TEXT NOT NULL,
    "status" "shared"."status" NOT NULL DEFAULT 'Pending',
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL,

    CONSTRAINT "refund_dispute_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "order"."invoice" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "type" "order"."invoice_type" NOT NULL,
    "ref_type" "order"."invoice_ref_type" NOT NULL,
    "ref_id" BIGINT NOT NULL,
    "seller_account_id" BIGINT,
    "buyer_account_id" BIGINT NOT NULL,
    "status" "shared"."status" NOT NULL,
    "payment_method" "order"."payment_method" NOT NULL,
    "address" TEXT NOT NULL,
    "phone" TEXT NOT NULL,
    "subtotal" BIGINT NOT NULL,
    "total" BIGINT NOT NULL,
    "file_rs_id" TEXT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "hash" BYTEA NOT NULL,
    "prev_hash" BYTEA NOT NULL,

    CONSTRAINT "invoice_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "order"."invoice_item" (
    "id" BIGSERIAL NOT NULL,
    "invoice_id" BIGINT NOT NULL,
    "snapshot" JSONB NOT NULL,
    "quantity" BIGINT NOT NULL,
    "unit_price" BIGINT NOT NULL,
    "subtotal" BIGINT NOT NULL,
    "total" BIGINT NOT NULL,

    CONSTRAINT "invoice_item_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "promotion"."base" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "owner_id" BIGINT,
    "ref_type" "promotion"."ref_type" NOT NULL,
    "ref_id" BIGINT,
    "type" "promotion"."type" NOT NULL,
    "title" TEXT NOT NULL,
    "description" TEXT,
    "is_active" BOOLEAN NOT NULL DEFAULT true,
    "date_started" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_ended" TIMESTAMPTZ(3),
    "schedule_tz" TEXT,
    "schedule_start" TIMESTAMPTZ(3),
    "schedule_duration" INTEGER,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL,

    CONSTRAINT "base_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "promotion"."discount" (
    "id" BIGINT NOT NULL,
    "order_wide" BOOLEAN NOT NULL,
    "min_spend" BIGINT NOT NULL DEFAULT 0,
    "max_discount" BIGINT NOT NULL DEFAULT 0,
    "discount_percent" INTEGER,
    "discount_price" BIGINT,

    CONSTRAINT "discount_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "shared"."resource" (
    "id" BIGSERIAL NOT NULL,
    "mime_type" TEXT NOT NULL,
    "owner_id" BIGINT NOT NULL,
    "owner_type" "shared"."resource_type" NOT NULL,
    "url" TEXT NOT NULL,
    "order" INTEGER NOT NULL,

    CONSTRAINT "resource_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "system"."event" (
    "id" BIGSERIAL NOT NULL,
    "account_id" BIGINT,
    "aggregate_id" BIGINT NOT NULL,
    "aggregate_type" VARCHAR(100) NOT NULL,
    "event_type" "system"."event_type" NOT NULL,
    "payload" JSONB NOT NULL,
    "version" BIGINT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "event_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "system"."search_sync" (
    "id" BIGSERIAL NOT NULL,
    "name" VARCHAR(100) NOT NULL,
    "last_synced" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "search_sync_pkey" PRIMARY KEY ("id")
);

-- CreateIndex
CREATE UNIQUE INDEX "base_code_key" ON "account"."base"("code");

-- CreateIndex
CREATE UNIQUE INDEX "base_phone_key" ON "account"."base"("phone");

-- CreateIndex
CREATE UNIQUE INDEX "base_email_key" ON "account"."base"("email");

-- CreateIndex
CREATE UNIQUE INDEX "base_username_key" ON "account"."base"("username");

-- CreateIndex
CREATE UNIQUE INDEX "profile_avatar_rs_id_key" ON "account"."profile"("avatar_rs_id");

-- CreateIndex
CREATE INDEX "customer_default_address_id_idx" ON "account"."customer"("default_address_id");

-- CreateIndex
CREATE INDEX "vendor_id_idx" ON "account"."vendor"("id");

-- CreateIndex
CREATE UNIQUE INDEX "income_history_hash_key" ON "account"."income_history"("hash");

-- CreateIndex
CREATE INDEX "income_history_account_id_idx" ON "account"."income_history"("account_id");

-- CreateIndex
CREATE INDEX "income_history_type_idx" ON "account"."income_history"("type");

-- CreateIndex
CREATE INDEX "income_history_date_created_idx" ON "account"."income_history"("date_created");

-- CreateIndex
CREATE INDEX "notification_account_id_idx" ON "account"."notification"("account_id");

-- CreateIndex
CREATE INDEX "notification_type_idx" ON "account"."notification"("type");

-- CreateIndex
CREATE INDEX "notification_channel_idx" ON "account"."notification"("channel");

-- CreateIndex
CREATE INDEX "notification_date_created_idx" ON "account"."notification"("date_created");

-- CreateIndex
CREATE INDEX "cart_item_cart_id_idx" ON "account"."cart_item"("cart_id");

-- CreateIndex
CREATE INDEX "cart_item_sku_id_idx" ON "account"."cart_item"("sku_id");

-- CreateIndex
CREATE UNIQUE INDEX "cart_item_cart_id_sku_id_key" ON "account"."cart_item"("cart_id", "sku_id");

-- CreateIndex
CREATE UNIQUE INDEX "address_code_key" ON "account"."address"("code");

-- CreateIndex
CREATE INDEX "address_account_id_idx" ON "account"."address"("account_id");

-- CreateIndex
CREATE INDEX "address_type_idx" ON "account"."address"("type");

-- CreateIndex
CREATE UNIQUE INDEX "brand_code_key" ON "catalog"."brand"("code");

-- CreateIndex
CREATE UNIQUE INDEX "category_name_key" ON "catalog"."category"("name");

-- CreateIndex
CREATE INDEX "category_parent_id_idx" ON "catalog"."category"("parent_id");

-- CreateIndex
CREATE UNIQUE INDEX "product_spu_code_key" ON "catalog"."product_spu"("code");

-- CreateIndex
CREATE INDEX "product_spu_account_id_idx" ON "catalog"."product_spu"("account_id");

-- CreateIndex
CREATE INDEX "product_spu_category_id_idx" ON "catalog"."product_spu"("category_id");

-- CreateIndex
CREATE INDEX "product_spu_brand_id_idx" ON "catalog"."product_spu"("brand_id");

-- CreateIndex
CREATE UNIQUE INDEX "product_sku_code_key" ON "catalog"."product_sku"("code");

-- CreateIndex
CREATE INDEX "product_sku_spu_id_idx" ON "catalog"."product_sku"("spu_id");

-- CreateIndex
CREATE UNIQUE INDEX "product_sku_attribute_code_key" ON "catalog"."product_sku_attribute"("code");

-- CreateIndex
CREATE INDEX "product_sku_attribute_sku_id_idx" ON "catalog"."product_sku_attribute"("sku_id");

-- CreateIndex
CREATE INDEX "product_sku_attribute_name_idx" ON "catalog"."product_sku_attribute"("name");

-- CreateIndex
CREATE UNIQUE INDEX "tag_tag_key" ON "catalog"."tag"("tag");

-- CreateIndex
CREATE INDEX "product_spu_tag_spu_id_idx" ON "catalog"."product_spu_tag"("spu_id");

-- CreateIndex
CREATE INDEX "product_spu_tag_tag_id_idx" ON "catalog"."product_spu_tag"("tag_id");

-- CreateIndex
CREATE UNIQUE INDEX "product_spu_tag_spu_id_tag_id_key" ON "catalog"."product_spu_tag"("spu_id", "tag_id");

-- CreateIndex
CREATE UNIQUE INDEX "comment_code_key" ON "catalog"."comment"("code");

-- CreateIndex
CREATE UNIQUE INDEX "sku_serial_serial_number_key" ON "inventory"."sku_serial"("serial_number");

-- CreateIndex
CREATE INDEX "sku_serial_sku_id_idx" ON "inventory"."sku_serial"("sku_id");

-- CreateIndex
CREATE UNIQUE INDEX "stock_ref_id_ref_type_key" ON "inventory"."stock"("ref_id", "ref_type");

-- CreateIndex
CREATE INDEX "stock_history_stock_id_idx" ON "inventory"."stock_history"("stock_id");

-- CreateIndex
CREATE INDEX "stock_history_date_created_idx" ON "inventory"."stock_history"("date_created");

-- CreateIndex
CREATE UNIQUE INDEX "base_code_key" ON "order"."base"("code");

-- CreateIndex
CREATE UNIQUE INDEX "item_code_key" ON "order"."item"("code");

-- CreateIndex
CREATE INDEX "item_order_id_idx" ON "order"."item"("order_id");

-- CreateIndex
CREATE INDEX "item_sku_id_idx" ON "order"."item"("sku_id");

-- CreateIndex
CREATE UNIQUE INDEX "item_serial_order_item_id_product_serial_id_key" ON "order"."item_serial"("order_item_id", "product_serial_id");

-- CreateIndex
CREATE UNIQUE INDEX "refund_code_key" ON "order"."refund"("code");

-- CreateIndex
CREATE INDEX "refund_order_item_id_idx" ON "order"."refund"("order_item_id");

-- CreateIndex
CREATE INDEX "refund_reviewed_by_id_idx" ON "order"."refund"("reviewed_by_id");

-- CreateIndex
CREATE UNIQUE INDEX "refund_dispute_code_key" ON "order"."refund_dispute"("code");

-- CreateIndex
CREATE INDEX "refund_dispute_refund_id_idx" ON "order"."refund_dispute"("refund_id");

-- CreateIndex
CREATE INDEX "refund_dispute_issued_by_id_idx" ON "order"."refund_dispute"("issued_by_id");

-- CreateIndex
CREATE UNIQUE INDEX "invoice_code_key" ON "order"."invoice"("code");

-- CreateIndex
CREATE UNIQUE INDEX "invoice_hash_key" ON "order"."invoice"("hash");

-- CreateIndex
CREATE INDEX "invoice_item_invoice_id_idx" ON "order"."invoice_item"("invoice_id");

-- CreateIndex
CREATE UNIQUE INDEX "base_code_key" ON "promotion"."base"("code");

-- CreateIndex
CREATE INDEX "resource_owner_id_owner_type_idx" ON "shared"."resource"("owner_id", "owner_type");

-- CreateIndex
CREATE INDEX "event_aggregate_id_aggregate_type_idx" ON "system"."event"("aggregate_id", "aggregate_type");

-- CreateIndex
CREATE INDEX "event_date_created_idx" ON "system"."event"("date_created");

-- CreateIndex
CREATE INDEX "search_sync_last_synced_idx" ON "system"."search_sync"("last_synced");

-- AddForeignKey
ALTER TABLE "account"."profile" ADD CONSTRAINT "profile_id_fkey" FOREIGN KEY ("id") REFERENCES "account"."base"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."customer" ADD CONSTRAINT "customer_id_fkey" FOREIGN KEY ("id") REFERENCES "account"."base"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."vendor" ADD CONSTRAINT "vendor_id_fkey" FOREIGN KEY ("id") REFERENCES "account"."base"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."income_history" ADD CONSTRAINT "income_history_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."vendor"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."notification" ADD CONSTRAINT "notification_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."base"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."cart_item" ADD CONSTRAINT "cart_item_cart_id_fkey" FOREIGN KEY ("cart_id") REFERENCES "account"."customer"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."cart_item" ADD CONSTRAINT "cart_item_sku_id_fkey" FOREIGN KEY ("sku_id") REFERENCES "catalog"."product_sku"("id") ON DELETE NO ACTION ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."address" ADD CONSTRAINT "address_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."base"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."product_spu" ADD CONSTRAINT "product_spu_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."vendor"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."product_spu" ADD CONSTRAINT "product_spu_category_id_fkey" FOREIGN KEY ("category_id") REFERENCES "catalog"."category"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."product_spu" ADD CONSTRAINT "product_spu_brand_id_fkey" FOREIGN KEY ("brand_id") REFERENCES "catalog"."brand"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."product_sku" ADD CONSTRAINT "product_sku_spu_id_fkey" FOREIGN KEY ("spu_id") REFERENCES "catalog"."product_spu"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."product_sku_attribute" ADD CONSTRAINT "product_sku_attribute_sku_id_fkey" FOREIGN KEY ("sku_id") REFERENCES "catalog"."product_sku"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."product_spu_tag" ADD CONSTRAINT "product_spu_tag_spu_id_fkey" FOREIGN KEY ("spu_id") REFERENCES "catalog"."product_spu"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."product_spu_tag" ADD CONSTRAINT "product_spu_tag_tag_id_fkey" FOREIGN KEY ("tag_id") REFERENCES "catalog"."tag"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."comment" ADD CONSTRAINT "comment_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."base"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "inventory"."sku_serial" ADD CONSTRAINT "sku_serial_sku_id_fkey" FOREIGN KEY ("sku_id") REFERENCES "catalog"."product_sku"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "inventory"."stock_history" ADD CONSTRAINT "stock_history_stock_id_fkey" FOREIGN KEY ("stock_id") REFERENCES "inventory"."stock"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."base" ADD CONSTRAINT "base_customer_id_fkey" FOREIGN KEY ("customer_id") REFERENCES "account"."customer"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."item" ADD CONSTRAINT "item_order_id_fkey" FOREIGN KEY ("order_id") REFERENCES "order"."base"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."item" ADD CONSTRAINT "item_sku_id_fkey" FOREIGN KEY ("sku_id") REFERENCES "catalog"."product_sku"("id") ON DELETE NO ACTION ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."item_serial" ADD CONSTRAINT "item_serial_order_item_id_fkey" FOREIGN KEY ("order_item_id") REFERENCES "order"."item"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."item_serial" ADD CONSTRAINT "item_serial_product_serial_id_fkey" FOREIGN KEY ("product_serial_id") REFERENCES "inventory"."sku_serial"("id") ON DELETE NO ACTION ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."vnpay" ADD CONSTRAINT "vnpay_id_fkey" FOREIGN KEY ("id") REFERENCES "order"."base"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."refund" ADD CONSTRAINT "refund_order_item_id_fkey" FOREIGN KEY ("order_item_id") REFERENCES "order"."item"("id") ON DELETE NO ACTION ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."refund" ADD CONSTRAINT "refund_reviewed_by_id_fkey" FOREIGN KEY ("reviewed_by_id") REFERENCES "account"."vendor"("id") ON DELETE SET NULL ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."refund_dispute" ADD CONSTRAINT "refund_dispute_refund_id_fkey" FOREIGN KEY ("refund_id") REFERENCES "order"."refund"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."refund_dispute" ADD CONSTRAINT "refund_dispute_issued_by_id_fkey" FOREIGN KEY ("issued_by_id") REFERENCES "account"."vendor"("id") ON DELETE NO ACTION ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "order"."invoice_item" ADD CONSTRAINT "invoice_item_invoice_id_fkey" FOREIGN KEY ("invoice_id") REFERENCES "order"."invoice"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "promotion"."base" ADD CONSTRAINT "base_owner_id_fkey" FOREIGN KEY ("owner_id") REFERENCES "account"."vendor"("id") ON DELETE SET NULL ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "promotion"."discount" ADD CONSTRAINT "discount_id_fkey" FOREIGN KEY ("id") REFERENCES "promotion"."base"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "system"."event" ADD CONSTRAINT "event_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."base"("id") ON DELETE SET NULL ON UPDATE CASCADE;

