-- One listing per name. Everything else goes.
--
-- Duplicate names were the thing a reader noticed first: two product pages, same title, different
-- id. Three attempts narrowed it — crawling four times as many real names took the average repeat
-- from 4.8x to 2.45x, and jittering prices stopped same-name-same-price pairs sitting adjacent in
-- search — but narrowing is not removing, and the requirement is that there are none.
--
-- What this costs, and it is not small: 592 165 of a million listings, and the even distribution
-- over the leaves. `Nhạc cụ` keeps 1 889 rows against `Thời trang & Quần áo`'s 17 492, because
-- Tiki lists about six hundred distinct musical instruments and no amount of crawling changes
-- that. A catalogue shaped by what actually exists is lopsided; the real thing is too.
--
-- Which row survives a group: the one that already carries a vector, then the lowest id.
-- Embedding is the expensive thing here — six hours of model time so far — and picking the
-- embedded row out of each group keeps most of it instead of deleting it and paying again.
--
--   psql -f sql/dedup_names.sql

\set ON_ERROR_STOP on
\timing on
SET work_mem = '512MB';
SET maintenance_work_mem = '1GB';

DROP TABLE IF EXISTS doomed;
CREATE UNLOGGED TABLE doomed AS
SELECT id FROM (
  SELECT l.id,
         row_number() OVER (
           PARTITION BY l.name
           -- A real listing beats a generated twin, whatever either one carries: it has the
           -- genuine reviews and interactions, and it is what the storefront held before any
           -- of this ran. Then an embedded row wins, then the lowest id so the choice is
           -- deterministic.
           ORDER BY (a.username LIKE 'bulk\_%'), (e.listing_id IS NULL), l.id
         ) AS rn
    FROM catalog.listing l
    LEFT JOIN catalog.listing_embedding e
           ON e.listing_id = l.id AND e.dense IS NOT NULL
    LEFT JOIN account.account a ON a.id = l.account_id
   WHERE l.deleted_at IS NULL
) t
WHERE rn > 1;
ALTER TABLE doomed ADD PRIMARY KEY (id);
ANALYZE doomed;

SELECT count(*) AS se_xoa FROM doomed;

-- Cross-schema rows first: no foreign key takes them with the listing.
DELETE FROM trust.review r USING doomed d WHERE d.id = r.listing_id;
DELETE FROM observability.listing_popularity p USING doomed d WHERE d.id = p.listing_id;

-- The listings, in batches. Each takes its variants, stock, tag links, favourites, signals and
-- embedding with it.
DO $$
DECLARE n bigint; total bigint := 0;
BEGIN
  LOOP
    DELETE FROM catalog.listing l WHERE l.id IN (SELECT id FROM doomed LIMIT 50000);
    GET DIAGNOSTICS n = ROW_COUNT;
    EXIT WHEN n = 0;
    DELETE FROM doomed d WHERE NOT EXISTS (SELECT 1 FROM catalog.listing l WHERE l.id = d.id);
    total := total + n;
    RAISE NOTICE 'deleted % (% so far)', n, total;
    COMMIT;
  END LOOP;
  RAISE NOTICE 'gone: %', total;
END $$;

DROP TABLE doomed;

DELETE FROM catalog.tag t
 WHERE NOT EXISTS (SELECT 1 FROM catalog.listing_tag lt WHERE lt.tag = t.id);

VACUUM (ANALYZE) catalog.listing;
VACUUM (ANALYZE) catalog.variant;
VACUUM (ANALYZE) trust.review;

SELECT count(*) AS listing, count(DISTINCT name) AS ten_khac_nhau,
       count(*) - count(DISTINCT name) AS con_trung
  FROM catalog.listing WHERE deleted_at IS NULL;
SELECT min(k) AS it_nhat, round(avg(k)) AS trung_binh, max(k) AS nhieu_nhat
  FROM (SELECT count(*) k FROM catalog.listing l
          JOIN catalog.category c ON c.id = l.category_id
         WHERE c.parent_id IS NOT NULL AND l.deleted_at IS NULL
         GROUP BY c.id) t;
SELECT count(*) AS vector_con_lai FROM catalog.listing_embedding WHERE dense IS NOT NULL;
