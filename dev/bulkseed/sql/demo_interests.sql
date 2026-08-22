-- Give one account four healthy interest rails, for a demo.
--
-- Four slots exist (domain.NumInterests) but a slot only becomes a *named row* if the category
-- tree has a name near it and some listing sits inside domain.InterestMaxDistance of it. A slot
-- built from a scattering of unrelated views fails both: `interestSignals` averages the vectors
-- of everything touched in one category, and the mean of a diverse group is a point no product
-- occupies. So this does not scatter signals — it picks one listing per category and favourites
-- its nearest neighbours, which makes the average land in the middle of a real cluster.
--
-- Favourites and not views, because the arithmetic is unforgiving: a view weighs 0.2 and a
-- favourite ~1.0 (its decay term alone), and the account this was written for already carried
-- 24.49 of accumulated strength in one category. Reaching a comparable number with views would
-- need ~35 rows per group, and `interestSignals` reads only the newest 200 signals — the older
-- real history would have been pushed out of its own window. The favourite branch has a separate
-- cap, so seven favourites per group cost nothing that matters.
--
-- Nothing here is destructive and nothing is invented beyond the interaction rows themselves:
-- the listings are real, active and in the catalogue.
--
--   psql -v email="'someone@example.com'" -f sql/demo_interests.sql

\set ON_ERROR_STOP on
\timing on
SET search_path = catalog, public;
-- Session scope, not SET LOCAL: this script runs outside a transaction, where SET LOCAL is a
-- warning and no more. Without it the neighbour search is post-filtered — HNSW hands back
-- ef_search (40) candidates, the category test keeps whichever of those forty happen to be in
-- the leaf, and a narrow leaf yielded 2 rows where 12 were asked for.
SET hnsw.iterative_scan = relaxed_order;

\if :{?email}
\else
  \set email '''khoakomlem@gmail.com'''
\endif

CREATE TEMP TABLE who AS SELECT id FROM account.account WHERE email = :email;
SELECT count(*) AS account_tim_thay FROM who;

-- Three dense leaves, distinct from each other and from what the account already leans on, each
-- with a pattern that names what the row should be about.
--
-- The pattern is not decoration. The first attempt took the lowest id in the leaf as its seed,
-- and in `Máy tính & Thiết bị mạng` that turned out to be a kitchen shelf — the catalogue's leaf
-- assignment comes from a Tiki category mapping and it is loose, so a leaf holds a few things
-- that do not belong (a live cat sits in `Phụ kiện điện tử`). The neighbours of a misfiled seed
-- are its misfiled neighbours, so the rail came back full of spice racks under a computing
-- heading, duplicating the kitchen rail beside it. Anchoring the seed on the name fixes it at
-- the source; nothing downstream can tell a tight cluster of the wrong thing from a right one.
CREATE TEMP TABLE grp(cat BIGINT, anchor TEXT);
INSERT INTO grp VALUES
  (9358, '^laptop '),
  (9371, '(noi |chao |am dun)'),
  (9377, '(do choi|bong nhua|thu bong)');

-- One seed per leaf, then its nearest neighbours *within the same leaf*. Ordering the seed by id
-- among the matches keeps a re-run idempotent: the same catalogue gives the same demo.
CREATE TEMP TABLE cluster AS
SELECT g.cat, n.id, n.rn
  FROM grp g
  CROSS JOIN LATERAL (
    SELECT e.dense FROM listing l JOIN listing_embedding e ON e.listing_id = l.id
     WHERE l.category_id = g.cat AND l.deleted_at IS NULL AND l.status = 'active'
       AND f_unaccent(lower(l.name)) ~ g.anchor
     ORDER BY l.id LIMIT 1) seed
  CROSS JOIN LATERAL (
    SELECT l2.id, row_number() OVER (ORDER BY e2.dense <=> seed.dense) AS rn
      FROM listing l2 JOIN listing_embedding e2 ON e2.listing_id = l2.id
     WHERE l2.category_id = g.cat AND l2.deleted_at IS NULL AND l2.status = 'active'
     ORDER BY e2.dense <=> seed.dense LIMIT 12) n;

SELECT cat, count(*) AS trong_cum FROM cluster GROUP BY cat ORDER BY cat;

-- The favourites carry the weight. Dated in the recent past, never the future: a favourite ahead
-- of now() makes the decay exponent positive and its weight ~2^(months), and it also pins
-- StaleInterests on `created_at > i.updated_at` for ever.
INSERT INTO favorite (account_id, listing_id, created_at)
SELECT w.id, c.id, now() - (c.rn * interval '3 hours')
  FROM who w, cluster c WHERE c.rn <= 7
ON CONFLICT DO NOTHING;

-- A little browsing on top, so the rails have a plausible story behind them rather than a
-- wishlist and nothing else.
INSERT INTO listing_signal (account_id, listing_id, type, created_at)
SELECT w.id, c.id, CASE WHEN c.rn <= 10 THEN 'view' ELSE 'click-from-category' END,
       now() - (c.rn * interval '2 hours')
  FROM who w, cluster c WHERE c.rn BETWEEN 8 AND 12;

-- One row dated now(), which is what actually wakes the sweep. Everything above is back-dated so
-- the decay term behaves, and back-dated rows cannot trigger a recompute at all: StaleInterests
-- asks for `source.created_at > i.updated_at`, and a favourite three hours old is older than an
-- interest computed a minute ago. Without this the rails stay as they were and the script looks
-- like it did nothing.
INSERT INTO listing_signal (account_id, listing_id, type, created_at)
SELECT w.id, c.id, 'view', now() FROM who w, cluster c WHERE c.rn = 1;

-- What the next recompute will see.
SELECT c2.name AS nhom, round(sum(x.w)::numeric, 2) AS strength_tho
  FROM (
    SELECT l.category_id, exp(-ln(2) * least(extract(epoch FROM now() - f.created_at) / 2592000, 50)) AS w
      FROM who w2 JOIN favorite f ON f.account_id = w2.id
      JOIN listing l ON l.id = f.listing_id AND l.deleted_at IS NULL
      JOIN listing_embedding e ON e.listing_id = l.id AND e.dense IS NOT NULL
    UNION ALL
    SELECT l.category_id, wt.weight * exp(-ln(2) * least(extract(epoch FROM now() - s.created_at) / 2592000, 50))
      FROM who w3 JOIN listing_signal s ON s.account_id = w3.id
      JOIN unnest(ARRAY['view','click-from-search','click-from-recommended','click-from-category','purchase'],
                  ARRAY[0.2,0.4,0.3,0.3,0.8]) AS wt(type, weight) ON wt.type = s.type
      JOIN listing l ON l.id = s.listing_id AND l.deleted_at IS NULL
      JOIN listing_embedding e ON e.listing_id = l.id AND e.dense IS NOT NULL
  ) x JOIN category c2 ON c2.id = x.category_id
 GROUP BY c2.name ORDER BY 2 DESC LIMIT 6;
