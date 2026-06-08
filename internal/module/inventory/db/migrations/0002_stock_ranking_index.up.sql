-- Serves ListMostTakenSku: WHERE ref_type = ? ORDER BY taken DESC.
-- Index range scan stops at LIMIT; avoids seq scan + full sort of stock.
CREATE INDEX IF NOT EXISTS "stock_ref_type_taken_idx"
    ON "inventory"."stock" ("ref_type", "taken" DESC);
