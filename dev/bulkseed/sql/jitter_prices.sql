-- Break exact (name, price) ties without touching anything that was embedded.
--
-- The generator rounded prices to a thousand đồng, which left about 170 reachable prices inside
-- one product's jitter range. A base name reused forty-odd times in a thin leaf then collides by
-- birthday paradox, and the catalogue ended up with groups of as many as twenty listings sharing
-- a name *and* a price — which turn up side by side in a search result and read as a bug rather
-- than as two sellers. generate.py rounds to a hundred now; this repairs rows already loaded.
--
-- Safe to run while the embedder is working. `ListStale` embeds
-- name + category + tags + spec values + description — price is not in that text, so nudging a
-- price invalidates no vector and the embedding queue is untouched.
--
-- The nudge is ±4% at a hundred đồng, derived from the variant id, so it is deterministic and a
-- second run changes nothing further.
--
--   psql -f sql/jitter_prices.sql

\set ON_ERROR_STOP on
\timing on
SET work_mem = '256MB';

DO $$
DECLARE lo bigint := 0; hi bigint; n bigint; total bigint := 0;
BEGIN
  SELECT max(id) INTO hi FROM catalog.variant;
  WHILE lo <= hi LOOP
    UPDATE catalog.variant v
       SET price = greatest(100, ((v.price * (960 + (v.id * 7919) % 81) / 1000) / 100) * 100)
     WHERE v.id >= lo AND v.id < lo + 200000
       AND v.deleted_at IS NULL
       -- Only where it would change something, so a re-run is a no-op rather than a rewrite.
       AND v.price <> greatest(100, ((v.price * (960 + (v.id * 7919) % 81) / 1000) / 100) * 100);
    GET DIAGNOSTICS n = ROW_COUNT;
    total := total + n;
    RAISE NOTICE 'ids % .. %: % variants', lo, lo + 199999, n;
    lo := lo + 200000;
    COMMIT;
  END LOOP;
  RAISE NOTICE 'nudged % variants', total;
END $$;

ANALYZE catalog.variant;

-- What it bought: how many listings still share a name and a price with another.
WITH p AS (
  SELECT l.name,
         (SELECT min(v.price) FROM catalog.variant v
           WHERE v.listing_id = l.id AND v.deleted_at IS NULL) AS price
    FROM catalog.listing l
   WHERE l.deleted_at IS NULL
)
SELECT count(*) AS nhom_trung, max(cnt) AS nhom_lon_nhat, sum(cnt) AS listing_bi_anh_huong
  FROM (SELECT name, price, count(*) cnt FROM p GROUP BY name, price HAVING count(*) > 1) t;
