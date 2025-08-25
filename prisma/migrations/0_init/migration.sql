-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "account";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "catalog";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "inventory";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "payment";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "promotion";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "shared";

-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "system";

-- CreateEnum
CREATE TYPE "account"."type" AS ENUM ('Customer', 'Vendor');

-- CreateEnum
CREATE TYPE "account"."status" AS ENUM ('ACTIVE', 'SUSPENDED');

-- CreateEnum
CREATE TYPE "account"."gender" AS ENUM ('Male', 'Female', 'Other');

-- CreateEnum
CREATE TYPE "account"."address_type" AS ENUM ('HOME', 'WORK');

-- CreateEnum
CREATE TYPE "catalog"."comment_dest_type" AS ENUM ('ProductSPU', 'Comment');

-- CreateEnum
CREATE TYPE "inventory"."stock_type" AS ENUM ('ProductSKU', 'Promotion');

-- CreateEnum
CREATE TYPE "inventory"."product_status" AS ENUM ('Active', 'Inactive', 'Sold', 'Damaged');

-- CreateEnum
CREATE TYPE "payment"."payment_method" AS ENUM ('COD', 'Card', 'EWallet', 'Crypto');

-- CreateEnum
CREATE TYPE "payment"."refund_method" AS ENUM ('PickUp', 'DropOff');

-- CreateEnum
CREATE TYPE "payment"."invoice_type" AS ENUM ('Sale', 'Service', 'Adjustment');

-- CreateEnum
CREATE TYPE "payment"."invoice_ref_type" AS ENUM ('Order', 'Fee');

-- CreateEnum
CREATE TYPE "promotion"."promotion_type" AS ENUM ('Voucher', 'FlashSale', 'Bundle', 'BuyXGetY', 'Cashback');

-- CreateEnum
CREATE TYPE "promotion"."promotion_ref_type" AS ENUM ('OrderItem', 'Order');

-- CreateEnum
CREATE TYPE "shared"."resource_type" AS ENUM ('Avatar', 'ProductImage', 'BrandLogo', 'Refund', 'ReturnDispute');

-- CreateEnum
CREATE TYPE "shared"."status" AS ENUM ('Pending', 'Processing', 'Success', 'Canceled', 'Failed');

-- CreateTable
CREATE TABLE "account"."account" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "type" "account"."type" NOT NULL,
    "status" "account"."status" NOT NULL DEFAULT 'ACTIVE',
    "phone" VARCHAR(50),
    "email" VARCHAR(255),
    "username" VARCHAR(100),
    "password" VARCHAR(255),
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "account_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."profile" (
    "id" BIGSERIAL NOT NULL,
    "account_id" BIGINT NOT NULL,
    "gender" "account"."gender",
    "name" VARCHAR(100),
    "date_of_birth" DATE,
    "avatar_rs_id" BIGINT,
    "email_verified" BOOLEAN NOT NULL DEFAULT false,
    "phone_verified" BOOLEAN NOT NULL DEFAULT false,

    CONSTRAINT "profile_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."customer" (
    "id" BIGSERIAL NOT NULL,
    "account_id" BIGINT NOT NULL,
    "default_address_id" BIGINT,

    CONSTRAINT "customer_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."cart_item" (
    "id" BIGSERIAL NOT NULL,
    "cart_id" BIGINT NOT NULL,
    "sku_id" BIGINT NOT NULL,
    "quantity" BIGINT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "cart_item_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."vendor" (
    "id" BIGSERIAL NOT NULL,
    "account_id" BIGINT NOT NULL,

    CONSTRAINT "vendor_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "account"."address" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "account_id" BIGINT NOT NULL,
    "type" "account"."address_type" NOT NULL DEFAULT 'HOME',
    "full_name" VARCHAR(100) NOT NULL,
    "phone" VARCHAR(20) NOT NULL,
    "phone_verified" BOOLEAN NOT NULL DEFAULT false,
    "street_address" VARCHAR(255) NOT NULL,
    "country" VARCHAR(2) NOT NULL,
    "city" VARCHAR(100) NOT NULL,
    "district" VARCHAR(100) NOT NULL,
    "ward" VARCHAR(100) NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

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
CREATE TABLE "catalog"."spu" (
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

    CONSTRAINT "spu_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."sku" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "spu_id" BIGINT NOT NULL,
    "price" BIGINT NOT NULL,
    "can_combine" BOOLEAN NOT NULL DEFAULT false,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_deleted" TIMESTAMPTZ(3),
    "version" BIGINT NOT NULL DEFAULT 1,

    CONSTRAINT "sku_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."sku_attribute" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "sku_id" BIGINT NOT NULL,
    "name" VARCHAR(100) NOT NULL,
    "value" VARCHAR(255) NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_updated" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "sku_attribute_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."tag" (
    "id" BIGSERIAL NOT NULL,
    "tag" VARCHAR(50) NOT NULL,
    "description" TEXT NOT NULL DEFAULT '',

    CONSTRAINT "tag_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."spu_tag" (
    "id" BIGSERIAL NOT NULL,
    "spu_id" BIGINT NOT NULL,
    "tag_id" BIGINT NOT NULL,

    CONSTRAINT "spu_tag_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "catalog"."comment" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "account_id" BIGINT NOT NULL,
    "ref_type" "catalog"."comment_dest_type" NOT NULL,
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
CREATE TABLE "payment"."order" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "customer_id" BIGINT NOT NULL,
    "invoice_id" BIGINT,
    "payment_method" "payment"."payment_method" NOT NULL,
    "status" "shared"."status" NOT NULL,
    "address" TEXT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "order_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "payment"."order_item" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "order_id" BIGINT NOT NULL,
    "sku_id" BIGINT NOT NULL,
    "quantity" BIGINT NOT NULL,

    CONSTRAINT "order_item_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "payment"."order_item_serial" (
    "id" BIGSERIAL NOT NULL,
    "order_item_id" BIGINT NOT NULL,
    "product_serial_id" BIGINT NOT NULL,

    CONSTRAINT "order_item_serial_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "payment"."vnpay" (
    "id" BIGSERIAL NOT NULL,
    "order_id" BIGINT NOT NULL,
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
CREATE TABLE "payment"."refund" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "order_item_id" BIGINT NOT NULL,
    "reviewed_by_id" BIGINT,
    "method" "payment"."refund_method" NOT NULL,
    "status" "shared"."status" NOT NULL,
    "reason" TEXT NOT NULL,
    "address" TEXT,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "refund_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "payment"."refund_dispute" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "refund_id" BIGINT NOT NULL,
    "vendor_id" BIGINT NOT NULL,
    "reason" TEXT NOT NULL,
    "status" "shared"."status" NOT NULL DEFAULT 'Pending',
    "created_at" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" TIMESTAMP(3) NOT NULL,

    CONSTRAINT "refund_dispute_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "payment"."invoice" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "type" "payment"."invoice_type" NOT NULL,
    "ref_type" "payment"."invoice_ref_type" NOT NULL,
    "ref_id" BIGINT NOT NULL,
    "seller_account_id" BIGINT,
    "buyer_account_id" BIGINT NOT NULL,
    "status" "shared"."status" NOT NULL,
    "payment_method" "payment"."payment_method" NOT NULL,
    "address" TEXT NOT NULL,
    "phone" TEXT NOT NULL,
    "subtotal" BIGINT NOT NULL,
    "total" BIGINT NOT NULL,
    "file_rs_id" TEXT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "hash" BYTEA NOT NULL,
    "prev_hash" BYTEA,

    CONSTRAINT "invoice_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "payment"."invoice_item" (
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
CREATE TABLE "promotion"."promotion" (
    "id" BIGSERIAL NOT NULL,
    "code" TEXT NOT NULL,
    "type" "promotion"."promotion_type" NOT NULL,
    "is_active" BOOLEAN NOT NULL DEFAULT true,
    "date_started" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "date_ended" TIMESTAMPTZ(3),
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "promotion_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "promotion"."promotion_voucher" (
    "id" BIGSERIAL NOT NULL,
    "promotion_id" BIGINT NOT NULL,
    "min_spend" BIGINT NOT NULL DEFAULT 0,
    "max_discount" BIGINT NOT NULL DEFAULT 0,
    "discount_percent" INTEGER,
    "discount_price" BIGINT,

    CONSTRAINT "promotion_voucher_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "promotion"."promotion_redemption" (
    "id" BIGSERIAL NOT NULL,
    "promotion_id" BIGINT NOT NULL,
    "version" BIGINT NOT NULL,
    "ref_type" "promotion"."promotion_ref_type" NOT NULL,
    "ref_id" BIGINT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "promotion_redemption_pkey" PRIMARY KEY ("id")
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
    "event_type" VARCHAR(100) NOT NULL,
    "payload" JSONB NOT NULL,
    "version" BIGINT NOT NULL,
    "date_created" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT "event_pkey" PRIMARY KEY ("id")
);

-- CreateIndex
CREATE UNIQUE INDEX "account_code_key" ON "account"."account"("code");

-- CreateIndex
CREATE UNIQUE INDEX "account_phone_key" ON "account"."account"("phone");

-- CreateIndex
CREATE UNIQUE INDEX "account_email_key" ON "account"."account"("email");

-- CreateIndex
CREATE UNIQUE INDEX "account_username_key" ON "account"."account"("username");

-- CreateIndex
CREATE UNIQUE INDEX "profile_account_id_key" ON "account"."profile"("account_id");

-- CreateIndex
CREATE UNIQUE INDEX "profile_avatar_rs_id_key" ON "account"."profile"("avatar_rs_id");

-- CreateIndex
CREATE INDEX "profile_account_id_idx" ON "account"."profile"("account_id");

-- CreateIndex
CREATE UNIQUE INDEX "customer_account_id_key" ON "account"."customer"("account_id");

-- CreateIndex
CREATE INDEX "customer_account_id_idx" ON "account"."customer"("account_id");

-- CreateIndex
CREATE INDEX "customer_default_address_id_idx" ON "account"."customer"("default_address_id");

-- CreateIndex
CREATE INDEX "cart_item_cart_id_idx" ON "account"."cart_item"("cart_id");

-- CreateIndex
CREATE INDEX "cart_item_sku_id_idx" ON "account"."cart_item"("sku_id");

-- CreateIndex
CREATE UNIQUE INDEX "cart_item_cart_id_sku_id_key" ON "account"."cart_item"("cart_id", "sku_id");

-- CreateIndex
CREATE UNIQUE INDEX "vendor_account_id_key" ON "account"."vendor"("account_id");

-- CreateIndex
CREATE INDEX "vendor_account_id_idx" ON "account"."vendor"("account_id");

-- CreateIndex
CREATE UNIQUE INDEX "address_code_key" ON "account"."address"("code");

-- CreateIndex
CREATE INDEX "address_account_id_idx" ON "account"."address"("account_id");

-- CreateIndex
CREATE INDEX "address_country_city_district_ward_idx" ON "account"."address"("country", "city", "district", "ward");

-- CreateIndex
CREATE INDEX "address_type_idx" ON "account"."address"("type");

-- CreateIndex
CREATE UNIQUE INDEX "brand_code_key" ON "catalog"."brand"("code");

-- CreateIndex
CREATE UNIQUE INDEX "category_name_key" ON "catalog"."category"("name");

-- CreateIndex
CREATE INDEX "category_parent_id_idx" ON "catalog"."category"("parent_id");

-- CreateIndex
CREATE UNIQUE INDEX "spu_code_key" ON "catalog"."spu"("code");

-- CreateIndex
CREATE INDEX "spu_account_id_idx" ON "catalog"."spu"("account_id");

-- CreateIndex
CREATE INDEX "spu_category_id_idx" ON "catalog"."spu"("category_id");

-- CreateIndex
CREATE INDEX "spu_brand_id_idx" ON "catalog"."spu"("brand_id");

-- CreateIndex
CREATE UNIQUE INDEX "sku_code_key" ON "catalog"."sku"("code");

-- CreateIndex
CREATE INDEX "sku_spu_id_idx" ON "catalog"."sku"("spu_id");

-- CreateIndex
CREATE UNIQUE INDEX "sku_attribute_code_key" ON "catalog"."sku_attribute"("code");

-- CreateIndex
CREATE INDEX "sku_attribute_sku_id_idx" ON "catalog"."sku_attribute"("sku_id");

-- CreateIndex
CREATE INDEX "sku_attribute_name_idx" ON "catalog"."sku_attribute"("name");

-- CreateIndex
CREATE UNIQUE INDEX "tag_tag_key" ON "catalog"."tag"("tag");

-- CreateIndex
CREATE INDEX "spu_tag_spu_id_idx" ON "catalog"."spu_tag"("spu_id");

-- CreateIndex
CREATE INDEX "spu_tag_tag_id_idx" ON "catalog"."spu_tag"("tag_id");

-- CreateIndex
CREATE UNIQUE INDEX "spu_tag_spu_id_tag_id_key" ON "catalog"."spu_tag"("spu_id", "tag_id");

-- CreateIndex
CREATE UNIQUE INDEX "comment_code_key" ON "catalog"."comment"("code");

-- CreateIndex
CREATE UNIQUE INDEX "comment_account_id_ref_type_ref_id_key" ON "catalog"."comment"("account_id", "ref_type", "ref_id");

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
CREATE UNIQUE INDEX "order_code_key" ON "payment"."order"("code");

-- CreateIndex
CREATE UNIQUE INDEX "order_item_code_key" ON "payment"."order_item"("code");

-- CreateIndex
CREATE INDEX "order_item_order_id_idx" ON "payment"."order_item"("order_id");

-- CreateIndex
CREATE INDEX "order_item_sku_id_idx" ON "payment"."order_item"("sku_id");

-- CreateIndex
CREATE UNIQUE INDEX "order_item_serial_order_item_id_product_serial_id_key" ON "payment"."order_item_serial"("order_item_id", "product_serial_id");

-- CreateIndex
CREATE UNIQUE INDEX "vnpay_order_id_key" ON "payment"."vnpay"("order_id");

-- CreateIndex
CREATE UNIQUE INDEX "refund_code_key" ON "payment"."refund"("code");

-- CreateIndex
CREATE INDEX "refund_order_item_id_idx" ON "payment"."refund"("order_item_id");

-- CreateIndex
CREATE INDEX "refund_reviewed_by_id_idx" ON "payment"."refund"("reviewed_by_id");

-- CreateIndex
CREATE UNIQUE INDEX "refund_dispute_code_key" ON "payment"."refund_dispute"("code");

-- CreateIndex
CREATE INDEX "refund_dispute_refund_id_idx" ON "payment"."refund_dispute"("refund_id");

-- CreateIndex
CREATE INDEX "refund_dispute_vendor_id_idx" ON "payment"."refund_dispute"("vendor_id");

-- CreateIndex
CREATE UNIQUE INDEX "invoice_code_key" ON "payment"."invoice"("code");

-- CreateIndex
CREATE UNIQUE INDEX "invoice_hash_key" ON "payment"."invoice"("hash");

-- CreateIndex
CREATE INDEX "invoice_item_invoice_id_idx" ON "payment"."invoice_item"("invoice_id");

-- CreateIndex
CREATE UNIQUE INDEX "promotion_code_key" ON "promotion"."promotion"("code");

-- CreateIndex
CREATE UNIQUE INDEX "promotion_voucher_promotion_id_key" ON "promotion"."promotion_voucher"("promotion_id");

-- CreateIndex
CREATE INDEX "promotion_redemption_promotion_id_idx" ON "promotion"."promotion_redemption"("promotion_id");

-- CreateIndex
CREATE INDEX "promotion_redemption_ref_type_ref_id_idx" ON "promotion"."promotion_redemption"("ref_type", "ref_id");

-- CreateIndex
CREATE INDEX "resource_owner_id_owner_type_idx" ON "shared"."resource"("owner_id", "owner_type");

-- AddForeignKey
ALTER TABLE "account"."profile" ADD CONSTRAINT "profile_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."account"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."customer" ADD CONSTRAINT "customer_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."account"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."cart_item" ADD CONSTRAINT "cart_item_cart_id_fkey" FOREIGN KEY ("cart_id") REFERENCES "account"."customer"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."cart_item" ADD CONSTRAINT "cart_item_sku_id_fkey" FOREIGN KEY ("sku_id") REFERENCES "catalog"."sku"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."vendor" ADD CONSTRAINT "vendor_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."account"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "account"."address" ADD CONSTRAINT "address_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."account"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."spu" ADD CONSTRAINT "spu_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."vendor"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."spu" ADD CONSTRAINT "spu_category_id_fkey" FOREIGN KEY ("category_id") REFERENCES "catalog"."category"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."spu" ADD CONSTRAINT "spu_brand_id_fkey" FOREIGN KEY ("brand_id") REFERENCES "catalog"."brand"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."sku" ADD CONSTRAINT "sku_spu_id_fkey" FOREIGN KEY ("spu_id") REFERENCES "catalog"."spu"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."sku_attribute" ADD CONSTRAINT "sku_attribute_sku_id_fkey" FOREIGN KEY ("sku_id") REFERENCES "catalog"."sku"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."spu_tag" ADD CONSTRAINT "spu_tag_spu_id_fkey" FOREIGN KEY ("spu_id") REFERENCES "catalog"."spu"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."spu_tag" ADD CONSTRAINT "spu_tag_tag_id_fkey" FOREIGN KEY ("tag_id") REFERENCES "catalog"."tag"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "catalog"."comment" ADD CONSTRAINT "comment_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."customer"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "inventory"."sku_serial" ADD CONSTRAINT "sku_serial_sku_id_fkey" FOREIGN KEY ("sku_id") REFERENCES "catalog"."sku"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "inventory"."stock_history" ADD CONSTRAINT "stock_history_stock_id_fkey" FOREIGN KEY ("stock_id") REFERENCES "inventory"."stock"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."order" ADD CONSTRAINT "order_customer_id_fkey" FOREIGN KEY ("customer_id") REFERENCES "account"."customer"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."order_item" ADD CONSTRAINT "order_item_order_id_fkey" FOREIGN KEY ("order_id") REFERENCES "payment"."order"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."order_item" ADD CONSTRAINT "order_item_sku_id_fkey" FOREIGN KEY ("sku_id") REFERENCES "catalog"."sku"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."order_item_serial" ADD CONSTRAINT "order_item_serial_order_item_id_fkey" FOREIGN KEY ("order_item_id") REFERENCES "payment"."order_item"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."order_item_serial" ADD CONSTRAINT "order_item_serial_product_serial_id_fkey" FOREIGN KEY ("product_serial_id") REFERENCES "inventory"."sku_serial"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."vnpay" ADD CONSTRAINT "vnpay_order_id_fkey" FOREIGN KEY ("order_id") REFERENCES "payment"."order"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."refund" ADD CONSTRAINT "refund_order_item_id_fkey" FOREIGN KEY ("order_item_id") REFERENCES "payment"."order_item"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."refund" ADD CONSTRAINT "refund_reviewed_by_id_fkey" FOREIGN KEY ("reviewed_by_id") REFERENCES "account"."vendor"("id") ON DELETE SET NULL ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."refund_dispute" ADD CONSTRAINT "refund_dispute_refund_id_fkey" FOREIGN KEY ("refund_id") REFERENCES "payment"."refund"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."refund_dispute" ADD CONSTRAINT "refund_dispute_vendor_id_fkey" FOREIGN KEY ("vendor_id") REFERENCES "account"."vendor"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "payment"."invoice_item" ADD CONSTRAINT "invoice_item_invoice_id_fkey" FOREIGN KEY ("invoice_id") REFERENCES "payment"."invoice"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "promotion"."promotion_voucher" ADD CONSTRAINT "promotion_voucher_promotion_id_fkey" FOREIGN KEY ("promotion_id") REFERENCES "promotion"."promotion"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "promotion"."promotion_redemption" ADD CONSTRAINT "promotion_redemption_promotion_id_fkey" FOREIGN KEY ("promotion_id") REFERENCES "promotion"."promotion"("id") ON DELETE CASCADE ON UPDATE CASCADE;

-- AddForeignKey
ALTER TABLE "system"."event" ADD CONSTRAINT "event_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "account"."account"("id") ON DELETE SET NULL ON UPDATE CASCADE;

