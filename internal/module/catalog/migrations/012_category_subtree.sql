-- Filtering by a category has to mean the category and everything under it.
--
-- The tree was flat when the browse feed was written, so "this category" and "this category's
-- id" were the same thing and `l.category_id = $1` was right. It is not flat now: eight roots
-- with thirty-four leaves under them, and every listing sits on a leaf, because a listing
-- classified as "Điện tử" rather than as "Điện thoại & Máy tính bảng" is a listing nobody has
-- classified. An equality test against a root therefore matches nothing at all — the top level
-- of the menu returns an empty page.
--
-- A function rather than the CTE written out at each call site: there are two of them today,
-- the feed and the search predicate, and the next one to filter by category should not have to
-- know the tree is recursive. It is also the difference between one place to fix and several
-- when the tree grows a third level.
--
-- Unqualified "category" on purpose. The pool sets search_path to the module's schema
-- (infra/postgres/pool.go), a SQL function with SECURITY INVOKER resolves names against the
-- caller's, and so this keeps working if catalog moves from a shared database to its own —
-- which is the whole reason the modules are schema-isolated in the first place.
--
-- STABLE and PARALLEL SAFE so the planner treats it as a constant subquery: called as
--   l.category_id IN (SELECT category_subtree($1))
-- it runs once as an InitPlan and the result is hashed, not re-evaluated per row.
CREATE OR REPLACE FUNCTION "category_subtree" ("root" BIGINT) RETURNS SETOF BIGINT LANGUAGE sql STABLE PARALLEL SAFE AS $$
  WITH RECURSIVE "descendants" AS (
    SELECT "id" FROM "category" WHERE "id" = "root"
    UNION ALL
    SELECT "child"."id"
      FROM "category" "child"
      JOIN "descendants" ON "child"."parent_id" = "descendants"."id"
  )
  SELECT "id" FROM "descendants";
$$;

COMMENT ON FUNCTION "category_subtree" (BIGINT) IS
  'A category id and every id beneath it. Use for category filters: an equality test against a root matches no listings, because listings sit on leaves.';
