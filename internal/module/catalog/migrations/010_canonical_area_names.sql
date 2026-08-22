-- Set province_name/ward_name to what the vendored division list (account/areas/vn.json)
-- spells, for the code pairs that existed when this was written. They were taken from the
-- request that wrote the row, so one code carried several names and a seed's invented ward
-- names outlived it; account.Service now resolves every write, so this repairs only the past.
-- Wards 22213 and 24505 were filed under the wrong province, so here the province follows the
-- ward — the narrower, unambiguous half.

UPDATE "listing" AS "t"
   SET "province_code" = "a"."province_code",
       "province_name" = "a"."province_name",
       "ward_name"     = "a"."ward_name"
  FROM (VALUES
	       ('00001', '01', 'Thành phố Hà Nội', 'Phường Phúc Xá'),
	       ('00019', '01', 'Thành phố Hà Nội', 'Phường Điện Biên'),
	       ('00109', '01', 'Thành phố Hà Nội', 'Phường Bưởi'),
	       ('00256', '01', 'Thành phố Hà Nội', 'Phường Lê Đại Hành'),
	       ('01270', '04', 'Tỉnh Cao Bằng', 'Phường Sông Bằng'),
	       ('11365', '31', 'Thành phố Hải Phòng', 'Phường Lạch Tray'),
	       ('20194', '48', 'Thành phố Đà Nẵng', 'Phường Hòa Hiệp Bắc'),
	       ('20263', '48', 'Thành phố Đà Nẵng', 'Phường Thọ Quang'),
	       ('20305', '48', 'Thành phố Đà Nẵng', 'Phường Hòa Phát'),
	       ('22213', '54', 'Tỉnh Phú Yên', 'Xã Đức Bình Tây'),
	       ('24505', '66', 'Tỉnh Đắk Lắk', 'Xã Ea KNuec'),
	       ('25747', '74', 'Tỉnh Bình Dương', 'Phường Phú Cường'),
	       ('26050', '75', 'Tỉnh Đồng Nai', 'Phường An Bình'),
	       ('26734', '79', 'Thành phố Hồ Chí Minh', 'Phường Tân Định'),
	       ('26743', '79', 'Thành phố Hồ Chí Minh', 'Phường Bến Thành'),
	       ('27154', '79', 'Thành phố Hồ Chí Minh', 'Phường 3'),
	       ('27289', '79', 'Thành phố Hồ Chí Minh', 'Phường 16'),
	       ('27478', '79', 'Thành phố Hồ Chí Minh', 'Phường Bình Thuận'),
	       ('31117', '92', 'Thành phố Cần Thơ', 'Phường Cái Khế')
       ) AS "a" ("ward_code", "province_code", "province_name", "ward_name")
 WHERE "t"."ward_code" = "a"."ward_code"
   AND ("t"."province_code" IS DISTINCT FROM "a"."province_code"
     OR "t"."province_name" IS DISTINCT FROM "a"."province_name"
     OR "t"."ward_name"     IS DISTINCT FROM "a"."ward_name");

-- The area index never covered the filter clients send: district_code is NULL on every row, so a
-- browse narrowed to one ward had no index and scanned every active listing.
DROP INDEX IF EXISTS "listing_area_idx";
CREATE INDEX "listing_area_idx" ON "listing" ("province_code", "ward_code")
    WHERE "status" = 'active' AND "deleted_at" IS NULL;
