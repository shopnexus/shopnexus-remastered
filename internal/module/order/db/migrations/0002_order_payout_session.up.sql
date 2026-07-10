ALTER TABLE "order"."order"
    ADD COLUMN "payout_session_id" UUID; -- Seller payout (escrow) session (kind='seller-payout'); NULL until escrow opens

ALTER TABLE "order"."order"
    ADD CONSTRAINT "order_payout_session_id_fkey" FOREIGN KEY ("payout_session_id")
        REFERENCES "order"."payment_session" ("id") ON DELETE SET NULL ON UPDATE CASCADE;
