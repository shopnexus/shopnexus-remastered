-- Mark a bounded subset of the catalogue for embedding, spread evenly over the leaves.
--
-- Why a subset rather than all of it. Retrieval here is bge-m3 dense+sparse only — migration
-- 008 dropped the trigram index — so an unembedded listing is one search never finds, and the
-- obvious move is to embed everything. Two measurements say otherwise at a million rows:
--
--   * The dense HNSW index measures 9 725 bytes per row on this schema. At a million listings
--     that is 9.7GB against a shared_buffers of 4.85GB on a 19GB machine, and HNSW wants its
--     graph in memory: past that every ANN query and every insert is random I/O. At a hundred
--     thousand the same index is under a gigabyte and sits in cache comfortably.
--   * Embedding runs at about 9 documents a second against the configured bge-m3 service on
--     text the length these listings produce. A hundred thousand is around three hours. A
--     million is thirty-one.
--
-- So: a million rows for the browse feed, the category pages, the price and area filters and
-- the paginator, which do not care about vectors at all, and a hundred thousand of them
-- searchable. Evenly over the leaves rather than the first hundred thousand by id, because a
-- subset that skips whole categories is a search that silently has nothing in them.
--
-- Ordered by cached_sold so the rows that get embedded are the ones a buyer is most likely to
-- be looking for, not an arbitrary slice.
--
--   psql -v per_leaf=2941 -f 02_embed_subset.sql        # ~100k over 34 leaves
--   psql -v per_leaf=0    -f 02_embed_subset.sql        # unmark everything not yet embedded
--
-- Then run the embedder. It drains the queue in one pass rather than one batch per interval,
-- so this can be left alone:
--
--   docker compose --profile embed up -d embedder
--   docker logs -f server-embedder-1

\set ON_ERROR_STOP on
\if :{?per_leaf}
\else
  \set per_leaf 2941
\endif

BEGIN;
SET search_path TO catalog;

-- Anything previously queued but not yet drained is cleared first, so this statement decides
-- the whole queue rather than adding to whatever an earlier run left behind.
UPDATE listing SET embedding_stale_at = NULL
 WHERE embedding_stale_at IS NOT NULL;

WITH ranked AS (
  SELECT l.id,
         row_number() OVER (PARTITION BY l.category_id
                            ORDER BY l.cached_sold DESC, l.cached_rating DESC, l.id) AS rn
    FROM listing l
    -- Only what a buyer can actually reach. Embedding a draft spends three hours of model
    -- time on rows no search is allowed to return.
   WHERE l.status = 'active'
     AND l.deleted_at IS NULL
     -- Rows that already carry a vector are left alone: re-embedding them costs the same as a
     -- new one and changes nothing. A listing whose text was edited is marked stale by the
     -- service that edited it, which is a different path from this one.
     AND NOT EXISTS (SELECT 1 FROM listing_embedding e
                      WHERE e.listing_id = l.id AND e.dense IS NOT NULL)
)
UPDATE listing l
   SET embedding_stale_at = now()
  FROM ranked
 WHERE l.id = ranked.id AND ranked.rn <= :per_leaf;

COMMIT;

SELECT count(*) AS queued,
       count(DISTINCT category_id) AS leaves_covered,
       min(cached_sold) AS min_sold,
       max(cached_sold) AS max_sold
  FROM catalog.listing
 WHERE embedding_stale_at IS NOT NULL;

SELECT round(count(*) / 9.0 / 60) AS est_minutes_at_9_docs_per_sec
  FROM catalog.listing
 WHERE embedding_stale_at IS NOT NULL;
