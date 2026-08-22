-- Merge the duplicate categories the live catalogue grew, then give what is left a
-- two-level tree.
--
-- Two id ranges had accumulated for one set of ideas — 9357..9389 from cmd/seed's
-- dataset.json and 11830..11840 from somewhere else — so "Laptop & Máy tính" and
-- "Máy tính & Thiết bị mạng" were separate rows a buyer had to choose between, and
-- "Chung" was a bucket for six listings nobody had classified. Duplicates are matched
-- by name and not by id: the ids are what the two sources happened to be assigned and
-- carry no meaning, while the names are the actual claim about what the category is.
--
-- Four of the old rows are kept and promoted to roots rather than deleted and replaced.
-- A category id is in URLs, in the listing rows pointing at it and in category_embedding;
-- reusing the row whose name is already the root's name costs nothing and keeps those.
--
-- Idempotent: every step is conditioned on the state it creates, so a second run is a
-- no-op. Run it once against a migrated database:
--
--   docker exec -i server-db-1 psql -U app -d shopnexus -v ON_ERROR_STOP=1 \
--     -f - < 01_categories.sql
--
-- Marking touched rows embedding_stale_at is on purpose: a renamed category is a
-- different category to the retrieval side, and there are only 42 of them.

\set ON_ERROR_STOP on
BEGIN;

SET search_path TO catalog;

-- ---------------------------------------------------------------- 1. the new leaf
-- "Điện tử" and "Chung" both held listings that are neither, and both rows are wanted
-- for something else. Their contents need a leaf that does describe them.
INSERT INTO category (parent_id, name, description, embedding_stale_at)
VALUES (NULL, 'Phụ kiện điện tử',
        'Sạc, cáp, ốp lưng, pin dự phòng, phụ kiện điện thoại, máy tính và thiết bị đeo.',
        now())
ON CONFLICT (name) DO NOTHING;

-- ------------------------------------------------------- 2. move the listings across
CREATE TEMP TABLE merge_map (src text, dst text) ON COMMIT DROP;
INSERT INTO merge_map VALUES
  ('Laptop & Máy tính',     'Máy tính & Thiết bị mạng'),
  ('Đồ gia dụng',           'Thiết bị gia dụng nhỏ'),
  ('Nội thất & Trang trí',  'Nội thất gia đình'),
  ('Sách & Văn phòng phẩm', 'Sách & Ấn phẩm'),
  ('Thời trang & Phụ kiện', 'Phụ kiện thời trang'),
  ('Xe cộ & Phụ tùng',      'Ô tô & Xe máy'),
  ('Chung',                 'Phụ kiện điện tử'),
  ('Điện tử',               'Phụ kiện điện tử');

UPDATE listing l
   SET category_id = d.id
  FROM merge_map m
  JOIN category s ON s.name = m.src
  JOIN category d ON d.name = m.dst
 WHERE l.category_id = s.id;

-- ------------------------------------- 3. drop the rows that were only ever duplicates
-- Empty by now, so listing_category_id_fkey's RESTRICT has nothing to object to. The
-- four names not listed here are the ones step 4 promotes.
DELETE FROM category
 WHERE name IN ('Laptop & Máy tính', 'Đồ gia dụng', 'Nội thất & Trang trí', 'Chung');

-- ------------------------------------------------------------ 4. the eight roots
-- Promoted from an existing row where its name is already what the root should be
-- called, inserted otherwise. 'Xe cộ & Phụ tùng' is renamed: it is the same idea one
-- word wider, and the row is worth keeping for its id.
UPDATE category SET name = 'Xe cộ & Công nghiệp',
                    description = 'Ô tô, xe máy, xe đạp, phụ tùng, dụng cụ và thiết bị công nghiệp.',
                    embedding_stale_at = now()
 WHERE name = 'Xe cộ & Phụ tùng';

UPDATE category SET description = 'Điện thoại, máy tính, máy ảnh, âm thanh, gaming và phụ kiện số.',
                    embedding_stale_at = now()
 WHERE name = 'Điện tử';
UPDATE category SET description = 'Quần áo, giày dép, túi xách, đồng hồ, trang sức và phụ kiện.',
                    embedding_stale_at = now()
 WHERE name = 'Thời trang & Phụ kiện';
UPDATE category SET description = 'Sách, ấn phẩm, văn phòng phẩm và dụng cụ học tập.',
                    embedding_stale_at = now()
 WHERE name = 'Sách & Văn phòng phẩm';

INSERT INTO category (parent_id, name, description, embedding_stale_at) VALUES
  (NULL, 'Sắc đẹp & Sức khỏe',   'Mỹ phẩm, chăm sóc cá nhân, thực phẩm chức năng và thiết bị sức khỏe.', now()),
  (NULL, 'Nhà cửa & Đời sống',   'Nội thất, trang trí, nhà bếp, phòng tắm, gia dụng và sân vườn.',       now()),
  (NULL, 'Bách hóa & Mẹ bé',     'Thực phẩm, đồ uống, hàng tiêu dùng, sản phẩm cho mẹ, bé và thú cưng.', now()),
  (NULL, 'Thể thao & Giải trí',  'Dụng cụ thể thao, dã ngoại, đồ chơi, nhạc cụ và đồ thủ công.',         now())
ON CONFLICT (name) DO NOTHING;

-- --------------------------------------------------------- 5. hang the leaves on them
CREATE TEMP TABLE leaf_map (leaf text, root text) ON COMMIT DROP;
INSERT INTO leaf_map VALUES
  ('Điện thoại & Máy tính bảng',         'Điện tử'),
  ('Máy tính & Thiết bị mạng',           'Điện tử'),
  ('Máy ảnh & Quay phim',                'Điện tử'),
  ('Âm thanh & Tai nghe',                'Điện tử'),
  ('Trò chơi điện tử & Gaming',          'Điện tử'),
  ('Phần mềm & Hàng hóa số',             'Điện tử'),
  ('Phụ kiện điện tử',                   'Điện tử'),
  ('Thời trang & Quần áo',               'Thời trang & Phụ kiện'),
  ('Giày dép',                           'Thời trang & Phụ kiện'),
  ('Túi xách & Vali',                    'Thời trang & Phụ kiện'),
  ('Đồng hồ',                            'Thời trang & Phụ kiện'),
  ('Trang sức',                          'Thời trang & Phụ kiện'),
  ('Phụ kiện thời trang',                'Thời trang & Phụ kiện'),
  ('Sắc đẹp & Chăm sóc cá nhân',         'Sắc đẹp & Sức khỏe'),
  ('Sức khỏe & Thể chất',                'Sắc đẹp & Sức khỏe'),
  ('Nội thất gia đình',                  'Nhà cửa & Đời sống'),
  ('Trang trí nhà cửa & Đèn chiếu sáng', 'Nhà cửa & Đời sống'),
  ('Nhà bếp & Ăn uống',                  'Nhà cửa & Đời sống'),
  ('Chăn ga gối đệm & Phòng tắm',        'Nhà cửa & Đời sống'),
  ('Thiết bị gia dụng lớn',              'Nhà cửa & Đời sống'),
  ('Thiết bị gia dụng nhỏ',              'Nhà cửa & Đời sống'),
  ('Sân vườn & Ngoại thất',              'Nhà cửa & Đời sống'),
  ('Tạp hóa & Thực phẩm',                'Bách hóa & Mẹ bé'),
  ('Mẹ & Bé',                            'Bách hóa & Mẹ bé'),
  ('Đồ dùng cho thú cưng',               'Bách hóa & Mẹ bé'),
  ('Thể thao & Dã ngoại',                'Thể thao & Giải trí'),
  ('Đồ chơi & Trò chơi',                 'Thể thao & Giải trí'),
  ('Nhạc cụ',                            'Thể thao & Giải trí'),
  ('Nghệ thuật & Thủ công',              'Thể thao & Giải trí'),
  ('Sách & Ấn phẩm',                     'Sách & Văn phòng phẩm'),
  ('Văn phòng phẩm & Dụng cụ học tập',   'Sách & Văn phòng phẩm'),
  ('Ô tô & Xe máy',                      'Xe cộ & Công nghiệp'),
  ('Công cụ & Phần cứng',                'Xe cộ & Công nghiệp'),
  ('Công nghiệp & Thương mại',           'Xe cộ & Công nghiệp');

-- Every leaf in the map has to exist: a typo here would otherwise leave that leaf a
-- second root and the mistake would only show up as a stray top-level menu entry.
DO $$
DECLARE missing text;
BEGIN
  SELECT string_agg(m.leaf, ', ') INTO missing
    FROM leaf_map m LEFT JOIN category c ON c.name = m.leaf
   WHERE c.id IS NULL;
  IF missing IS NOT NULL THEN
    RAISE EXCEPTION 'leaf_map names no such category: %', missing;
  END IF;
END $$;

UPDATE category c
   SET parent_id = r.id
  FROM leaf_map m
  JOIN category r ON r.name = m.root
 WHERE c.name = m.leaf AND c.parent_id IS DISTINCT FROM r.id;

COMMIT;

-- ------------------------------------------------------------------------ the result
SELECT r.name AS root,
       count(*) AS leaves,
       (SELECT count(*) FROM listing l
          JOIN category c2 ON c2.id = l.category_id
         WHERE c2.parent_id = r.id AND l.deleted_at IS NULL) AS listings
  FROM catalog.category c
  JOIN catalog.category r ON r.id = c.parent_id
 GROUP BY r.name, r.id
 ORDER BY r.name;

SELECT count(*) FILTER (WHERE parent_id IS NULL) AS roots,
       count(*) FILTER (WHERE parent_id IS NOT NULL) AS leaves,
       (SELECT count(*) FROM catalog.listing l
          JOIN catalog.category c ON c.id = l.category_id
         WHERE c.parent_id IS NULL) AS listings_still_on_a_root
  FROM catalog.category;
