-- Hide the listings a demo should not put on a projector: underwear, contraception, adult
-- products. `hidden` and not deleted — the catalogue keeps them, search and browse do not show
-- them (`feedWhere` tests `l.status = 'active'`), and their vectors survive so unhiding costs
-- nothing.
--
-- Matched on the name with f_unaccent, because a crawled Tiki name may or may not carry
-- diacritics. Three rules earned their place the hard way:
--
--   * short tokens need word boundaries. Plain LIKE '%rung%' matched 2 640 rows — "khử trùng",
--     "Nội Địa Trung" — because unaccenting "trung" leaves "rung" inside it. '%sex%' matched
--     1 190, nearly all of them "unisex".
--   * "trinh nu" is not a rule at all: it matches "Trà Trinh Nữ Hoàng Cung", a medicinal tea,
--     and "Hành Trình Nuôi con" once the accents are gone.
--   * "tang kich thuoc" matched a paddling pool ("Bể Bơi 3 Tầng Kích Thước") and a box of
--     crayons. The enhancement products it was meant for are named by brand, so `titan gel`
--     and `cau nho` do that job without the collateral.
--
-- A second pass found more, and four more traps. Rejected: `lo the` ("Balo Thể Thao", "áo ba lỗ
-- thể thao"), `bao cao` ("Bài Báo Cáo"), `nguoi lon` (a bicycle or a tin of milk "cho người
-- lớn"), and two that are medical rather than awkward — `hau mon` is haemorrhoid gel and a
-- bidet, `gen nit` is postpartum shapewear. `may rung` is half massage guns, so the sex toys
-- among them are caught by `\mdiem g\M` instead: the boundary is what stops it matching
-- "điểm giao".
--
-- A third pass, after reading what the second one actually hid, removed three more. `ho nguc`
-- was the worst of the run: it hid a bag of parrot food ("HẠT TRỘN … CHO NGỰC HỒNG") and, twice,
-- a vest top sold as "chống hở ngực" — a garment hidden for advertising that it does not expose
-- anything. `nguc gia` was a jacket's false chest panel. `nhu hoa` needs the whole phrase
-- `dan nhu hoa`, because a word boundary cannot separate "nhũ hoa" from "đẹp như hoa" on a ring:
-- unaccented they are the same two words.
--
-- The lesson those three share: a keyword list is only as good as reading the rows it caught.
-- Every trap here was invisible in the counts and obvious in the names.
--
-- Swimwear is deliberately absent. `bikini` (182) and `ao tam` (37) are ordinary retail; if a
-- demo wants them gone too, add them to `word_kw` and `sub_kw` and re-run.
--
--   psql -f sql/hide_sensitive.sql
--
-- To undo, restore from the table this leaves behind:
--   UPDATE catalog.listing l SET status = h.was
--     FROM catalog.hidden_sensitive h WHERE h.id = l.id;

\set ON_ERROR_STOP on
\timing on
SET search_path = catalog, public;

CREATE TABLE IF NOT EXISTS hidden_sensitive (
  id     BIGINT PRIMARY KEY,
  was    TEXT   NOT NULL,
  matched TEXT  NOT NULL,
  at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Substring rules: long enough that a boundary adds nothing.
CREATE TEMP TABLE sub_kw(k TEXT) ;
INSERT INTO sub_kw VALUES
  ('quan lot'),('ao lot'),('noi y'),('lingerie'),('quan sip'),
  ('bao cao su'),('boi tron'),('do choi nguoi lon'),('bup be tinh'),
  ('duong vat'),('am dao'),('thu dam'),('kich duc'),('cuong duong'),
  ('mang trinh'),('lot khe'),('xuyen thau'),('goi cam'),('trung rung'),
  ('massage diem g'),('titan gel'),('cau nho'),('18\+'),
  -- second pass
  ('ao nguc'),('ao bra'),('do lot'),('mieng dan nguc'),
  ('corset'),('quan chip'),('tinh duc'),('condom'),('dan nhu hoa');

-- Boundary rules: brand names and short words that live inside innocent ones.
CREATE TEMP TABLE word_kw(k TEXT);
INSERT INTO word_kw VALUES ('sip'),('durex'),('sagami'),('sexy'),('diem g');

CREATE TEMP TABLE hit AS
SELECT l.id, l.status::text AS was, min(m.k) AS matched
  FROM listing l
  JOIN LATERAL (
    SELECT k FROM sub_kw  WHERE f_unaccent(lower(l.name)) ~ k
    UNION ALL
    SELECT k FROM word_kw WHERE f_unaccent(lower(l.name)) ~ ('\m'||k||'\M')
  ) m ON true
 WHERE l.deleted_at IS NULL AND l.status = 'active'
 GROUP BY l.id, l.status;

SELECT count(*) AS se_an FROM hit;
SELECT matched, count(*) FROM hit GROUP BY 1 ORDER BY 2 DESC;

INSERT INTO hidden_sensitive (id, was, matched)
SELECT id, was, matched FROM hit
ON CONFLICT (id) DO NOTHING;

UPDATE listing l SET status = 'hidden' FROM hit h WHERE h.id = l.id;

SELECT status, count(*) FROM listing WHERE deleted_at IS NULL GROUP BY 1 ORDER BY 2 DESC;
