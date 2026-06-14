ALTER TABLE "account"."contact"
    ADD COLUMN "address_detail" VARCHAR(255); -- unit/floor/notes; free text, never geocoded (pin is source of truth)
