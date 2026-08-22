-- Remove everything generate.py wrote, and nothing else.
--
-- The counterpart cmd/seed has and this did not, which is a gap worth closing rather than a
-- convenience: a loader that can only add leaves "start over" meaning "restore a backup", and
-- the whole point of a generated catalogue is that it is cheap to throw away and rebuild.
--
-- What identifies a generated row is its owner: every seller and buyer this tool creates is
-- `bulk_seller_%` / `bulk_buyer_%`, and every listing belongs to one of them. So the delete
-- walks from the accounts, not from an id range — an id range is a guess that goes wrong the
-- first time somebody loads in two passes.
--
-- Separate from a load and refusing to run inside one, the same way cmd/seed keeps -wipe apart:
--
--   psql -v yes_i_mean_it=1 -f 06_wipe_bulk.sql
--
-- Kept out of run.sh on purpose. A loader that quietly wiped first is one bad afternoon away
-- from being pointed at the wrong database.

\set ON_ERROR_STOP on
\timing on

\if :{?yes_i_mean_it}
\else
\echo 'refusing: pass -v yes_i_mean_it=1. This deletes every generated listing, review, tag'
\echo 'link, favourite, signal, popularity row, seller and buyer. The hand-written catalogue'
\echo 'and the real accounts are left alone.'
\quit
\endif

-- Room to work. The default work_mem here is under ten megabytes, and every step below is a
-- hash semi-join against a table with a million rows in it: at that size the hash spills to a
-- temp file, and a delete that should take a minute takes twenty. Session-scoped, so nothing
-- else inherits it.
SET work_mem = '512MB';
SET maintenance_work_mem = '1GB';

-- Deliberately not one transaction. Deleting a million listings cascades into variants, stock,
-- tag links, favourites, signals and embeddings — several million rows — and carrying all of
-- that in one transaction is gigabytes of WAL and a rollback nobody wants to sit through. Every
-- step is idempotent, so an interrupted wipe is finished by running it again.

DROP TABLE IF EXISTS bulk_account, bulk_listing;

CREATE UNLOGGED TABLE bulk_account AS
SELECT id FROM account.account
 WHERE username LIKE 'bulk\_seller\_%' OR username LIKE 'bulk\_buyer\_%';
ALTER TABLE bulk_account ADD PRIMARY KEY (id);

CREATE UNLOGGED TABLE bulk_listing AS
SELECT l.id FROM catalog.listing l JOIN bulk_account a ON a.id = l.account_id;
ALTER TABLE bulk_listing ADD PRIMARY KEY (id);

ANALYZE bulk_account;
ANALYZE bulk_listing;

SELECT (SELECT count(*) FROM bulk_account) AS accounts_to_go,
       (SELECT count(*) FROM bulk_listing) AS listings_to_go;

-- Reviews first: another schema, so no foreign key takes them with the listing.
--
-- Three statements and not one with an OR. The first version was
--   WHERE listing_id IN (...) OR author_id IN (...) OR seller_id IN (...)
-- which no index can serve: a disjunction of three semi-joins forces a scan of every review
-- with all three hashes live at once, and on two and a half million rows it spilled and sat
-- there for a quarter of an hour. Split, each leg gets its own plan — listing_id has an index,
-- and the other two are one sequential pass each, which is seconds.
DELETE FROM trust.review r USING bulk_listing b WHERE b.id = r.listing_id;
DELETE FROM trust.review r USING bulk_account a WHERE a.id = r.author_id;
DELETE FROM trust.review r USING bulk_account a WHERE a.id = r.seller_id;

DELETE FROM observability.listing_popularity p USING bulk_listing b WHERE b.id = p.listing_id;

-- Favourites and signals a generated buyer left on a listing that is staying.
DELETE FROM catalog.favorite f USING bulk_account a WHERE a.id = f.account_id;
DELETE FROM catalog.listing_signal s USING bulk_account a WHERE a.id = s.account_id;

-- The listings, in batches, each committing. One batch of fifty thousand listings is closer to
-- half a million rows once the cascades are counted, which is as much as one transaction should
-- carry here.
DO $$
DECLARE n bigint; total bigint := 0;
BEGIN
  LOOP
    DELETE FROM catalog.listing l
     WHERE l.id IN (SELECT id FROM bulk_listing LIMIT 50000);
    GET DIAGNOSTICS n = ROW_COUNT;
    EXIT WHEN n = 0;
    DELETE FROM bulk_listing b
     WHERE NOT EXISTS (SELECT 1 FROM catalog.listing l WHERE l.id = b.id);
    total := total + n;
    RAISE NOTICE 'deleted % listings (% so far)', n, total;
    COMMIT;
  END LOOP;
  RAISE NOTICE 'listings gone: %', total;
END $$;

DELETE FROM account.contact c USING bulk_account a WHERE a.id = c.account_id;
DELETE FROM account.account acc USING bulk_account a WHERE a.id = acc.id;

-- A tag nothing references any more; each takes its vector with it (tag_embedding cascades).
DELETE FROM catalog.tag t
 WHERE NOT EXISTS (SELECT 1 FROM catalog.listing_tag lt WHERE lt.tag = t.id);

DROP TABLE bulk_account, bulk_listing;

VACUUM (ANALYZE) catalog.listing;
VACUUM (ANALYZE) catalog.variant;
VACUUM (ANALYZE) trust.review;

SELECT (SELECT count(*) FROM catalog.listing) AS listings,
       (SELECT count(*) FROM catalog.variant) AS variants,
       (SELECT count(*) FROM trust.review) AS reviews,
       (SELECT count(*) FROM account.account) AS accounts,
       (SELECT count(*) FROM catalog.tag) AS tags,
       pg_size_pretty(pg_database_size(current_database())) AS db_size;
