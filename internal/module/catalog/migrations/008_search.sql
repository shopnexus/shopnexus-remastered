-- The lexical half of search is now bge-m3's sparse vector, ranked through
-- "listing_embedding_sparse_idx", so the trigram index over the listing name has no reader: the
-- one clause left that matches a name is the recommended feed's ILIKE, which no index serves and
-- which runs over a pool its ANN legs have already cut to a few hundred rows.
--
-- The "f_unaccent" function stays — that ILIKE calls it on both sides. So do the "unaccent" and
-- "pg_trgm" extensions: account's own migration creates pg_trgm, and dropping a shared extension
-- from one module's schema is a cross-module trap.
DROP INDEX IF EXISTS "listing_name_unaccent_trgm_idx";
