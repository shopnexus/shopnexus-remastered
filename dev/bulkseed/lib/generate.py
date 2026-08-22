#!/usr/bin/env python3
"""Generate a large, evenly spread catalogue and the SQL that loads it.

Why this exists next to cmd/seed rather than inside it: cmd/seed writes a demo, and a demo
is a hundred listings whose history somebody invented on purpose — orders in each state,
negotiations, reviews with replies. This writes volume. Nothing here has a history; every
listing is a listing and nothing else, because the point is a catalogue with enough rows,
spread evenly enough, to tell whether a browse feed, a category page and a paginator hold
up. Running 100 000 listings through cmd/seed's path would also generate 100 000 photos and
a quarter million orders, which is not a bigger demo, it is a stuck laptop.

Output is COPY-ready TSV plus a load.sql that stages it and inserts into the real tables.
Not INSERTs: 100 000 listings and their variants and stock is around 600 000 rows, and COPY
into an unlogged staging table is the difference between a minute and an hour.

Ids are assigned here rather than left to the identity columns, because variant.listing_id
and stock.variant_id have to point at rows that do not exist yet. So the generator reads the
current high-water mark, counts up from it, and load.sql inserts with OVERRIDING SYSTEM
VALUE and then moves the sequences past what was used. That is safe exactly as long as
nothing else is writing to these tables while it runs, which for a dev database being
loaded is the case, and load.sql re-checks the mark before it starts rather than trusting it.

    ./generate.py                          # 100 000 listings over every leaf, evenly
    ./generate.py --total 5000             # a smaller pass to check the shape
    ./generate.py --sellers 300            # how many synthetic sellers own them
    ./generate.py --embed-queue            # mark them stale so the indexer picks them up

Then load, from the host:

    ./run.sh

The vocabulary comes from crawl_tiki.py's vocab.json where it exists, from the names already
in the database next, and from profiles.FALLBACK last, so this runs with no network at all —
just with more repetition in the names.
"""

import argparse
import itertools
import json
import random
import re
import subprocess
import sys
import unicodedata
from datetime import datetime, timedelta, timezone
from pathlib import Path

from lib import paths
from lib.geo import JITTER, PROVINCE_POINTS
from lib.leafmap import LEAVES, TREE
from lib.profiles import FALLBACK, PROFILES, TAGS


# How many of the catalogue's own names to take per leaf as base material. See load_vocab.
VOCAB_DB_CAP = 3000
DB = ("docker", "exec", "-i", "server-db-1", "psql", "-U", "app", "-d", "shopnexus",
      "-tAq", "-F", "\t", "-c")


def q(sql):
    """One query, tab-separated rows, through the container's psql.

    psql and not a driver: this script has no dependencies on purpose, and the DSNs only
    resolve inside the compose network anyway.
    """
    p = subprocess.run(DB + (sql,), capture_output=True, text=True)
    if p.returncode != 0:
        sys.exit(f"psql failed:\n{p.stderr.strip()}")
    return [line.split("\t") for line in p.stdout.strip().split("\n") if line]


# --------------------------------------------------------------------------- TSV encoding

def tsv(value):
    """One field in COPY's text format. None is NULL; the three escapes are COPY's own."""
    if value is None:
        return r"\N"
    if isinstance(value, bool):
        return "t" if value else "f"
    if isinstance(value, (int, float)):
        return str(value)
    return (str(value).replace("\\", "\\\\").replace("\t", "\\t")
            .replace("\n", "\\n").replace("\r", ""))


def row(*values):
    return "\t".join(tsv(v) for v in values) + "\n"


def jsonb(obj):
    return json.dumps(obj, ensure_ascii=False)


def int_array(ids):
    return "{" + ",".join(str(i) for i in ids) + "}"


# ------------------------------------------------------------------------------ the names

def slugify(name, salt):
    """A URL slug. Not unique by construction — listing.slug lost its unique index in
    migration 006 — but salted anyway so two rows with one name are still two URLs."""
    s = unicodedata.normalize("NFD", name.lower())
    s = "".join(c for c in s if unicodedata.category(c) != "Mn")
    s = s.replace("đ", "d")
    s = re.sub(r"[^a-z0-9]+", "-", s).strip("-")
    return f"{s[:180]}-{salt}"


def load_vocab(path, per_leaf_floor):
    """Merge the three name sources into one pool per leaf.

    Order matters only for how much of each ends up in the pool: the crawl is the widest and
    the most real, the database's own names are next, and FALLBACK is there so a leaf the
    crawl could not reach is thin rather than empty.
    """
    pools = {leaf: [] for leaf in LEAVES}
    crawled = 0
    # The photograph 04_fetch_photos.py downloaded for this exact product, if it is in the
    # store. That is the whole point of having fetched them: the cover of a listing generated
    # from "Nồi cơm điện Sharp KS-11ET" is the picture of that rice cooker, not of a rice
    # cooker, and certainly not of a bicycle.
    own_photo = {}
    manifest = paths.MANIFEST
    if manifest.exists():
        by_key = {k: int(i) for k, i in q(
            """SELECT object_key, id FROM catalog.resource
                WHERE provider = 'local' AND metadata->>'source' = 'tiki'
                  AND deleted_at IS NULL""")}
        for row in json.loads(manifest.read_text(encoding="utf-8")):
            rid = by_key.get(row["key"])
            if rid:
                own_photo[row["name"]] = rid
        print(f"{len(own_photo)} product names have their own photograph "
              f"({len(by_key)} tiki resources in the store)", file=sys.stderr)

    if path.exists():
        data = json.loads(path.read_text(encoding="utf-8"))
        for leaf, items in data.items():
            if leaf not in pools:
                continue
            for it in items:
                pools[leaf].append({"name": it["name"], "brand": it.get("brand"),
                                    "price": it["price"],
                                    "photo": own_photo.get(it["name"])})
                crawled += 1

    # What is already in the catalogue, written by hand. Free, and real.
    #
    # Explicitly *not* what an earlier run of this command wrote. Two reasons, and the first
    # is the one that matters: a generated name may already carry an axis value, so reading it
    # back as a base name and appending another compounds — "iPhone 13 - 256GB" becomes
    # "iPhone 13 - 256GB Đen", and a third pass makes it worse. Topping a catalogue up from
    # 300k to 600k to a million is exactly the path that does it. The second is size: at six
    # hundred thousand rows this query is six hundred thousand names parsed out of psql's
    # text output, to be told the same things the crawl already said.
    #
    # Capped per leaf as well. A leaf pool exists to give rng.choice something to draw from;
    # past a few thousand entries another one changes nothing and every one costs memory.
    existing = 0
    rows = q(f"""SELECT leaf, name, price FROM (
                   SELECT c.name AS leaf, l.name AS name,
                          coalesce((SELECT min(v.price) FROM catalog.variant v
                                     WHERE v.listing_id = l.id AND v.deleted_at IS NULL), 0)
                            AS price,
                          row_number() OVER (PARTITION BY c.id ORDER BY l.id) AS rn
                     FROM catalog.listing l
                     JOIN catalog.category c ON c.id = l.category_id
                    WHERE l.deleted_at IS NULL
                      AND c.parent_id IS NOT NULL
                      AND l.account_id NOT IN (SELECT id FROM account.account
                                                WHERE username LIKE 'bulk\\_%')
                 ) t WHERE rn <= {VOCAB_DB_CAP}""")
    for leaf, name, price in rows:
        if leaf in pools:
            pools[leaf].append({"name": name, "brand": None, "price": int(price) or 0,
                                "photo": own_photo.get(name)})
            existing += 1

    fallback = 0
    for leaf, names in FALLBACK.items():
        lo, hi = PROFILES[leaf]["price_band"]
        for n in names:
            pools[leaf].append({"name": n, "brand": None, "photo": None,
                                "price": (lo * 3 + hi) // 6})
            fallback += 1

    # A price the band rejects is a price this leaf does not really have.
    for leaf, pool in pools.items():
        lo, hi = PROFILES[leaf]["price_band"]
        for it in pool:
            if not (lo <= it["price"] <= hi):
                it["price"] = random.randint(lo, min(hi, lo * 20))

    print(f"vocabulary: {crawled} crawled + {existing} from the database + {fallback} fallback "
          f"= {sum(len(v) for v in pools.values())} names", file=sys.stderr)
    thin = {k: len(v) for k, v in pools.items() if len(v) < per_leaf_floor}
    if thin:
        print(f"  {len(thin)} leaves under {per_leaf_floor} names — their listings will repeat "
              f"base names more often:", file=sys.stderr)
        for k, n in sorted(thin.items(), key=lambda x: x[1])[:12]:
            print(f"    {n:>5}  {k}", file=sys.stderr)
    return pools


# ------------------------------------------------------------------------------- the seller

FIRST = ["An", "Bình", "Chi", "Dũng", "Duy", "Giang", "Hà", "Hải", "Hằng", "Hiếu", "Hoa",
         "Hoàng", "Huy", "Khoa", "Lan", "Linh", "Long", "Mai", "Minh", "Nam", "Nga", "Ngọc",
         "Nhung", "Phong", "Phúc", "Quân", "Quỳnh", "Sơn", "Tâm", "Thảo", "Thắng", "Thu",
         "Thùy", "Tiến", "Trang", "Trung", "Tú", "Tuấn", "Vân", "Việt", "Yến"]
SURNAME = ["Nguyễn", "Trần", "Lê", "Phạm", "Hoàng", "Huỳnh", "Phan", "Vũ", "Võ", "Đặng",
           "Bùi", "Đỗ", "Hồ", "Ngô", "Dương", "Lý"]
SHOP_STYLE = ["Shop {n}", "{n} Store", "{n} Mart", "Cửa hàng {n}", "{n} Official",
              "Kho {n}", "{n} Sài Gòn", "{n} Hà Nội", "Tiệm {n}", "{n} Shop"]


def make_sellers(n, base_id, base_contact_id, areas, pw_hash, now):
    """Synthetic sellers with a pickup contact each.

    The contact is not optional decoration: listing.province_code and ward_code are copied
    from the seller's default pickup contact, exactly as PublishListing does it, and the
    browse feed's area filter reads them. A seller without one owns listings that no area
    filter can see.
    """
    accounts, contacts, sellers = [], [], []
    for i in range(n):
        aid = base_id + i
        cid = base_contact_id + i
        person = f"{random.choice(SURNAME)} {random.choice(FIRST)}"
        shop = random.choice(SHOP_STYLE).format(n=random.choice(FIRST))
        # Identity from the account id and not from i, which restarts at zero every run.
        # account.email and account.phone are both UNIQUE, so an index-derived identity made
        # the second load of an incremental run — 100k now, the rest later — fail outright on
        # account_email_key. The id is monotonic across runs, so this cannot collide.
        phone = f"+8498{aid:07d}"
        created = now - timedelta(days=random.randint(60, 1000),
                                 minutes=random.randint(0, 1440))
        accounts.append(row(
            aid, "active", "user", phone, f"seller{aid:06d}@bulkseed.local",
            f"bulk_seller_{aid:06d}", pw_hash, True, created.isoformat(),
            shop, f"{shop} — {person}. Giao nhanh, đổi trả trong 7 ngày.",
            None, "VN", "vi-VN", "Asia/Ho_Chi_Minh"))
        province_code, province_name, ward_code, ward_name = random.choice(areas)
        contacts.append(row(
            cid, aid, person, phone, "home", True, True, created.isoformat(),
            "VN", province_code, province_name, ward_code, ward_name,
            f"{random.randint(1, 400)} {random.choice(['Đường', 'Hẻm', 'Ngõ'])} "
            f"{random.randint(1, 60)}, {ward_name}", jsonb({})))
        sellers.append({"id": aid, "area": (province_code, province_name, ward_code, ward_name)})
    return accounts, contacts, sellers


def make_buyers(n, base_id, pw_hash, now):
    """Accounts that exist to have written a review, favourited something, or looked at it.

    Separate from the sellers, and there are more of them, because the alternative is three
    hundred authors sharing a few hundred thousand reviews — a thousand each, which is not a
    marketplace, it is three hundred very busy people. No contact row: nothing about a review
    or a favourite reads one, and a pickup address for somebody who does not sell is noise.
    """
    accounts, ids = [], []
    for i in range(n):
        aid = base_id + i
        created = now - timedelta(days=random.randint(30, 1000),
                                  minutes=random.randint(0, 1440))
        accounts.append(row(
            aid, "active", "user", f"+8497{aid:07d}", f"buyer{aid:06d}@bulkseed.local",
            f"bulk_buyer_{aid:06d}", pw_hash, True, created.isoformat(),
            f"{random.choice(SURNAME)} {random.choice(FIRST)}", None,
            random.choice([None, "male", "female"]), "VN", "vi-VN", "Asia/Ho_Chi_Minh"))
        ids.append(aid)
    return accounts, ids


# ------------------------------------------------------------------------------ the listing

CONDITION = (["new"] * 55 + ["used"] * 42 + ["damaged"] * 3)
STATUS = (["active"] * 92 + ["hidden"] * 4 + ["pending"] * 3 + ["draft"] * 1)
PRICE_MODE = (["fixed"] * 80 + ["negotiable"] * 20)

DESC_OPEN = [
    "{name}. Hàng có sẵn, giao trong 1-2 ngày.",
    "{name} — mô tả đúng thực tế, không tráo hàng.",
    "Bán {name}. Ảnh chụp thật, xem kỹ trước khi đặt.",
    "{name}, đã kiểm tra kỹ trước khi đăng.",
    "Thanh lý {name} vì không còn dùng tới.",
]
DESC_CONDITION = {
    "new": ["Sản phẩm mới, chưa qua sử dụng, còn nguyên seal và phụ kiện.",
            "Hàng mới 100%, nguyên hộp, đầy đủ phụ kiện theo máy.",
            "Mới chưa bóc, còn tem niêm phong."],
    "used": ["Đã qua sử dụng, ngoại hình còn đẹp, hoạt động bình thường.",
             "Máy đã dùng vài tháng, có vài vết xước nhỏ ở góc, chức năng đầy đủ.",
             "Hàng cũ nhưng còn tốt, đã vệ sinh và kiểm tra trước khi đăng."],
    "damaged": ["Sản phẩm có lỗi, mô tả rõ trong phần thông số. Bán cho ai cần lấy phụ kiện.",
                "Có hư hỏng nhẹ, không ảnh hưởng chức năng chính. Giá đã trừ lỗi.",
                "Hàng lỗi, cần sửa thêm. Không nhận đổi trả."],
}
DESC_CLOSE = [
    "Nhận chuyển khoản hoặc COD. Đóng gói chống sốc cẩn thận.",
    "Hỗ trợ kiểm tra hàng khi nhận. Inbox để xem thêm ảnh.",
    "Có thể xem hàng trực tiếp tại {ward}, {province}.",
    "Đổi trả trong 7 ngày nếu không đúng mô tả.",
    "Ưu tiên khách lấy nhiều, có giá tốt hơn cho đơn từ 2 sản phẩm.",
]


# Review prose, composed rather than picked.
#
# The first version was eighteen fixed sentences, one drawn per review, which at a million
# listings meant each sentence appeared a hundred and thirty-six thousand times. The
# descriptions never had that problem because the product name is interpolated into them — the
# same template reads differently for every listing — so the reviews are built the same way:
# an opening, a middle that names something concrete about *this* product, and a closing.
#
# Three slots of seven or eight each is a few hundred skeletons per rating, and the middle
# carries a product name, a specification value or the variant the buyer chose, so the number
# of distinct bodies is bounded by the catalogue rather than by this file.
REVIEW_OPEN = {
    5: ["Rất hài lòng.", "Quá ổn so với giá.", "Hàng đẹp hơn mong đợi.", "Mua lần thứ hai rồi.",
        "Đúng như mô tả.", "Shop giao nhanh, đóng gói kỹ.", "Sản phẩm tốt, không có gì phải nói."],
    4: ["Nhìn chung là ổn.", "Hàng tốt, giao hơi lâu.", "Đúng mô tả, trừ một điểm nhỏ.",
        "Dùng được, giá hợp lý.", "Ổn trong tầm giá.", "Tạm hài lòng.", "Không tệ."],
    3: ["Tạm được.", "Bình thường.", "Không xuất sắc nhưng cũng không tệ.",
        "Đúng tiền nào của nấy.", "Hơi khác kỳ vọng.", "Dùng được nhưng không như ảnh.",
        "Trung bình."],
    2: ["Hơi thất vọng.", "Chất lượng kém hơn mô tả.", "Không như kỳ vọng.",
        "Phải nhắn shop nhiều lần.", "Giao thiếu thứ.", "Dùng tạm chứ không hài lòng."],
    1: ["Rất thất vọng.", "Hàng lỗi.", "Không giống mô tả chút nào.", "Đã yêu cầu đổi trả.",
        "Shop phản hồi rất chậm.", "Không nên mua."],
}
# {name} the product, {attr} the variant the buyer took, {spec_key}/{spec_val} one line off its
# spec sheet. Every entry names at least one of them, which is the whole point.
REVIEW_DETAIL = {
    5: ["{name} dùng được đúng như hình, {spec_key} đúng là {spec_val}.",
        "Mình lấy bản {attr}, vừa ý.", "{spec_key} {spec_val} đúng như shop ghi.",
        "Đặt {attr} và nhận đúng {attr}, không bị đổi.",
        "{name} chắc chắn, hoàn thiện tốt.", "Xem kỹ thì {spec_key} là {spec_val}, đúng nhu cầu.",
        "Dùng {name} được hai tuần, chưa thấy vấn đề gì."],
    4: ["{name} ổn, chỉ là hộp hơi móp khi nhận.",
        "Bản {attr} hơi khác ảnh một chút về màu.",
        "{spec_key} ghi {spec_val} nhưng thực tế mình thấy nhỉnh hơn.",
        "Mình lấy {attr}, dùng được nhưng chờ hơi lâu.",
        "{name} tốt, trừ một sao vì shop trả lời chậm.",
        "Chất lượng {name} khá, đóng gói tạm.",
        "Đúng {spec_key} {spec_val}, chỉ giao chậm hai ngày."],
    3: ["{name} tạm, không như ảnh lắm.",
        "Bản {attr} mình nhận khác màu trên hình.",
        "{spec_key} thực tế không đúng {spec_val} như ghi.",
        "Mình đặt {attr} mà giao sai, đổi lại mất mấy ngày.",
        "{name} dùng được nhưng hoàn thiện chưa kỹ.",
        "Trong tầm giá thì {name} chấp nhận được.",
        "Hỏi shop về {spec_key} mà không ai trả lời."],
    2: ["{name} kém hơn mô tả nhiều.",
        "Bản {attr} bị lỗi nhẹ, phải tự sửa.",
        "{spec_key} không đúng {spec_val} chút nào.",
        "Giao thiếu phụ kiện của {name}.",
        "Đặt {attr} nhưng nhận hàng khác.",
        "{name} dùng vài ngày là có vấn đề."],
    1: ["{name} không dùng được, phải trả lại.",
        "Nhận {attr} bị hỏng ngay khi mở hộp.",
        "{spec_key} ghi {spec_val} là sai hoàn toàn.",
        "Giao sai hẳn sản phẩm, không phải {name}.",
        "{name} lỗi mà shop không chịu đổi."],
}
REVIEW_CLOSE = {
    5: ["Sẽ mua lại.", "Recommend cho mọi người.", "Cảm ơn shop.", "5 sao.",
        "Ai đang cân nhắc thì mua đi.", "Đóng gói chống sốc rất kỹ.", ""],
    4: ["Vẫn sẽ mua lại.", "Nhìn chung đáng tiền.", "Trừ một sao thôi.",
        "Shop nên ghi rõ hơn.", "Ổn để dùng hằng ngày.", ""],
    3: ["Cân nhắc trước khi mua.", "Không mua lại.", "Tuỳ nhu cầu thôi.",
        "Mong shop mô tả kỹ hơn.", ""],
    2: ["Không mua lại.", "Shop nên kiểm hàng trước khi gửi.", "Khá mất thời gian.", ""],
    1: ["Tuyệt đối không mua.", "Đã báo cáo shop.", "Mất tiền mất thời gian.", ""],
}
# Ratings on a marketplace are not uniform — most people who bother are happy — so the draw
# is skewed and cached_rating comes out of the reviews rather than being invented beside them.
RATING_DRAW = [5] * 58 + [4] * 24 + [3] * 10 + [2] * 5 + [1] * 3


def review_body(rating, product, attr, specs, rng):
    """One review, composed from the three slots with this product's own facts in the middle."""
    spec_key, spec_val = "", ""
    concrete = [(k, v) for k, v in specs.items() if v and k != "Tình trạng"]
    if concrete:
        spec_key, spec_val = rng.choice(concrete)
    # A shortened product name: a review that quotes 90 characters of listing title reads like
    # a listing, not like a person.
    short = product if len(product) <= 40 else product[:40].rsplit(" ", 1)[0]
    detail = rng.choice(REVIEW_DETAIL[rating]).format(
        name=short, attr=attr, spec_key=spec_key.lower() or "thông số",
        spec_val=spec_val or "như mô tả")
    parts = [rng.choice(REVIEW_OPEN[rating]), detail, rng.choice(REVIEW_CLOSE[rating])]
    return " ".join(p for p in parts if p)


SIGNAL_TYPES = (["view"] * 70 + ["click-from-search"] * 14
                + ["click-from-recommended"] * 12 + ["purchase"] * 4)


# Prices land on a hundred đồng, not a thousand.
#
# Rounding to a thousand left only about 170 reachable prices inside one product's jitter range,
# and with a base name reused forty-odd times in a thin leaf that is a birthday-paradox collision
# every few listings — two cards with the same name *and* the same price side by side, which
# reads as a bug rather than as two sellers. A hundred is ten times the room and still a price a
# Vietnamese shop would print.
def price_of(base, band, factor):
    lo, hi = band
    return max(lo, min(hi, int(base * factor / 100) * 100))


def make_listing(lid, leaf, leaf_id, pool, seller, buyers, photos, now, embed, rng,
                 name_axes=1, order_seq=None, base=None):
    prof = PROFILES[leaf]
    # `base` given means the caller is walking the vocabulary one entry at a time — one listing
    # per real product. Drawing at random instead is the older shape, where several listings
    # come from one name and are told apart by an axis value.
    if base is None:
        base = rng.choice(pool)
    axes = prof["axes"]

    # The name is the crawled base plus what distinguishes this listing from the others drawn
    # from it.
    #
    # Getting this wrong twice is instructive. The first version appended an axis value to
    # *every* name and produced "Sữa tắm Dettol 850ml - Free size": one axis set per leaf
    # cannot fit every product in it, and a shower gel does not come in a size. The fix was to
    # make the suffix occasional — 35% — and that was worse in a way that only showed up at a
    # million rows: 65% of listings then carried the *bare* base name, and with twenty thousand
    # base names covering a million listings the worst name appeared **fifty-two times
    # identically**. A category page of the same product over and over.
    #
    # So: always differentiate, and lean on the leaf being right rather than on hedging. Almost
    # every base name now comes from the crawl into the leaf Tiki filed it under, which is what
    # made the mismatched-axis problem rare — it was mostly hand-written names in the wrong
    # category that produced it. The value is still skipped when the name already contains it,
    # so "iPhone 13 128GB" does not become "iPhone 13 128GB - 128GB".
    axis_name, axis_values = axes[0]
    lead = rng.choice(axis_values)
    name = base["name"]
    # name_axes == 0: the name is the product's, untouched.
    #
    # The axis suffix is defined per *leaf*, not per product, and one leaf holds things the same
    # axis cannot describe: "Thiết bị gia dụng lớn" varies by "Dung tích" with fridge volumes, so
    # a washing machine in it came out as "MÁY GIẶT TOSHIBA 10KG - 500L". A fixed-lens security
    # camera became "Camera IP Hikvision - Kit 18-55mm"; milk powder became "Sữa ... - Newborn
    # Lẻ 1". Three of four sampled groups were nonsense like that. No per-leaf axis set fixes it,
    # because one leaf legitimately contains a fridge, a washing machine, a television and an
    # air conditioner. The variation belongs in variant.attributes, which is where a storefront's
    # colour and size pickers read it from anyway.
    if name_axes >= 1 and lead.lower() not in name.lower():
        name = f"{name} - {lead}"
    # The second axis on about half of them. Always would read as a generated string — real
    # titles do not list every attribute — and never leaves the distinct-name count at
    # bases × |axis1|, which for a thin leaf is under its target. Half is enough to cover the
    # target while most titles stay the length a seller would actually type.
    if name_axes >= 1 and len(axes) > 1 and (name_axes > 1 or rng.random() < 0.5):
        second_name, second_values = axes[1]
        extra = rng.choice(second_values)
        if extra.lower() not in name.lower():
            name = f"{name} {extra}"
    if len(name) > 250:
        name = name[:250].rsplit(" ", 1)[0]

    condition = rng.choice(CONDITION)
    status = rng.choice(STATUS)
    price = price_of(base["price"], prof["price_band"], rng.uniform(0.72, 1.45))
    if condition == "used":
        price = price_of(price, prof["price_band"], rng.uniform(0.55, 0.85))
    elif condition == "damaged":
        price = price_of(price, prof["price_band"], rng.uniform(0.25, 0.5))

    specs = {}
    for key, values in prof["specs"]:
        if values is None:                      # the "Hãng" key: the crawled brand or nothing
            if base.get("brand"):
                specs[key] = base["brand"]
            continue
        specs[key] = rng.choice(values)
    specs["Tình trạng"] = {"new": "Mới", "used": "Đã sử dụng", "damaged": "Có lỗi"}[condition]

    province_code, province_name, ward_code, ward_name = seller["area"]
    desc = " ".join([
        rng.choice(DESC_OPEN).format(name=base["name"]),
        rng.choice(DESC_CONDITION[condition]),
        rng.choice(DESC_CLOSE).format(ward=ward_name, province=province_name),
    ])

    # The cover is this product's own photograph where one was fetched; the rest of the gallery
    # comes from the leaf's pool, which is at least a picture of the right kind of thing. A
    # base name with no photograph of its own falls back to the pool entirely.
    #
    # Cover first because listing.attachments is ordered and attachments[1] is what the card
    # renders — a gallery whose first slot is a sibling's photo looks exactly as wrong as the
    # random draw this replaced.
    extra = rng.choice([0, 0, 1, 2, 2, 3])
    rest = rng.sample(photos, min(len(photos), extra)) if photos else []
    own = base.get("photo")
    if own:
        gallery = [own] + [r for r in rest if r != own]
    else:
        gallery = rest or (rng.sample(photos, min(len(photos), 1)) if photos else [])

    # Sales, then the reviews those sales produced, then the rating those reviews average to.
    # In that order and not the other way round: an earlier version drew cached_rating and
    # cached_review_count straight onto the listing, which put "43 đánh giá" on a product page
    # whose review list was empty — trust.review had no rows at all. Deriving them means the
    # number on the card and the list under it cannot disagree.
    sold = max(0, int(rng.lognormvariate(1.6, 1.5)) - 1) if status == "active" else 0
    created = now - timedelta(days=rng.randint(0, 730), minutes=rng.randint(0, 1440))

    reviews = []
    if sold:
        # Not every buyer writes one. A third of them, give or take, and never more than the
        # units that actually moved.
        n = min(sold, max(0, int(rng.triangular(0, sold * 0.45, sold * 0.2))))
        for _ in range(n):
            score = rng.choice(RATING_DRAW)
            author = rng.choice(buyers)
            if author == seller["id"]:                 # nobody reviews their own listing
                continue
            written = created + timedelta(days=rng.randint(1, 400),
                                          minutes=rng.randint(0, 1440))
            if written > now:
                written = now - timedelta(minutes=rng.randint(0, 10000))
            helpful = max(0, int(rng.lognormvariate(0.7, 1.1)) - 1)
            reviews.append((lid, next(order_seq), author, seller["id"], score,
                            review_body(score, base["name"], lead, specs, rng), helpful,
                            max(0, helpful // rng.choice([3, 5, 8])), 0,
                            written.isoformat()))
    review_count = len(reviews)
    rating = round(sum(r[4] for r in reviews) / review_count, 2) if review_count else 0.0

    # Where the goods are, as a point. The radius filter treats a NULL as outside every
    # radius, so a listing without one is a listing "near me" never returns. Province-centre
    # plus jitter: ward-level accuracy would want a ward-level dataset, and for a radius
    # search being in the right province within a few tens of kilometres is the question.
    lat, lng = PROVINCE_POINTS.get(province_code, (16.0, 107.5))
    point = (f"SRID=4326;POINT({lng + rng.uniform(-JITTER, JITTER):.6f} "
             f"{lat + rng.uniform(-JITTER, JITTER):.6f})")

    listing = row(
        lid, slugify(name, lid), seller["id"], leaf_id, status, name, desc,
        jsonb(specs), int_array(gallery), rng.choice(PRICE_MODE), condition, "VND",
        rating, review_count, sold,
        province_code, province_name, ward_code, ward_name, point,
        created.isoformat(), created.isoformat() if embed else None)

    # Variants: the first axis is fixed by the name, so the second is what varies. One axis
    # means one variant, which is the ordinary case for most of this catalogue.
    if len(axes) > 1:
        _, second = axes[1]
        picks = rng.sample(second, min(len(second), rng.choice([1, 1, 2, 2, 3, 4])))
        combos = [{axis_name: lead, axes[1][0]: v} for v in picks]
    else:
        combos = [{axis_name: lead}]

    variants, stocks = [], []
    featured = rng.randrange(len(combos))
    for j, attrs in enumerate(combos):
        vprice = price if j == featured else price_of(price, prof["price_band"],
                                                     rng.uniform(0.9, 1.18))
        quantity = rng.choice([1, 1, 2, 3, 5, 5, 10, 20, 50])
        vsold = min(quantity, sold // len(combos)) if sold else 0
        variants.append((vprice, jsonb(attrs), jsonb({
            "weight_grams": rng.choice([200, 400, 600, 1000, 1500, 3000]),
            "length_cm": rng.randint(10, 60), "width_cm": rng.randint(8, 40),
            "height_cm": rng.randint(3, 30)}), j == featured, created.isoformat()))
        stocks.append((quantity, 0, vsold, created.isoformat()))

    # Tags: the brand the crawl found, plus a few of the leaf's own. The brand is the useful
    # half — it is what a buyer actually filters by — and it costs nothing extra because the
    # crawl already carried it.
    tags = []
    if base.get("brand"):
        slug = re.sub(r"[^a-z0-9]+", "-",
                      unicodedata.normalize("NFKD", base["brand"].lower())
                      .encode("ascii", "ignore").decode()).strip("-")
        if slug and len(slug) <= 100:
            tags.append(slug)
    tags += rng.sample(TAGS[leaf], rng.choice([1, 2, 2, 3]))
    tags = list(dict.fromkeys(tags))          # listing_tag is UNIQUE (listing_id, tag)

    # Favourites and signals: the input account_interest is built from, and without them the
    # recommended feed has nothing to recommend from. Sparse on purpose — a catalogue where
    # every listing is favourited is not one a ranking can tell anything from.
    favorites, signals = [], []
    if status == "active":
        for account in rng.sample(buyers, rng.choice([0, 0, 0, 1, 1, 2, 3])):
            # Clamped to now, exactly as the signals below are. Unclamped this put 25 302 of
            # 124 279 favourites in the future — up to 2027 — and the damage was not cosmetic:
            # `interestSignals` weights a favourite by exp(-ln2 * (now - created)/half_life), so a
            # negative age makes the exponent positive and the weight 2^(months/1), around 1 000×
            # for ten months ahead. One future row then decided an account's whole interest
            # centroid. It also pinned `StaleInterests` on `created_at > i.updated_at` for ever,
            # and since that query is `LIMIT 500` with no ORDER BY, the same 500 unclearable
            # accounts came back every sweep and the other 5 494 were never recomputed at all.
            when = min(created + timedelta(days=rng.randint(1, 300)), now)
            favorites.append((account, lid, when.isoformat()))
        for _ in range(rng.choice([0, 0, 1, 1, 2, 3, 5, 8])):
            seen = created + timedelta(days=rng.randint(0, 400), minutes=rng.randint(0, 1440))
            signals.append((rng.choice(buyers), lid, rng.choice(SIGNAL_TYPES),
                            min(seen, now).isoformat()))

    # Popularity, for the platform-wide trending list. Derived from the numbers already
    # decided rather than drawn again: a listing that sold forty units and collected eight
    # reviews did not get there without being looked at.
    popularity = None
    if status == "active" and (sold or signals):
        views = sold * rng.randint(8, 60) + len(signals) * rng.randint(2, 15) + rng.randint(0, 40)
        clicks = max(sold, int(views * rng.uniform(0.04, 0.18)))
        popularity = (lid, round(sold * 3.0 + clicks * 0.4 + views * 0.05
                                 + review_count * 1.5, 4),
                      views, clicks, int(views * rng.uniform(0.0, 0.05)), sold,
                      now.isoformat())

    return listing, variants, stocks, tags, reviews, favorites, signals, popularity


# ----------------------------------------------------------------------------------- main

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--total", type=int, default=100_000,
                    help="how many listings the catalogue should END UP with, "
                         "spread evenly over the leaves")
    ap.add_argument("--sellers", type=int, default=300, help="synthetic sellers to own them")
    ap.add_argument("--buyers", type=int, default=2000,
                    help="synthetic accounts that write the reviews, favourite and browse")
    ap.add_argument("--seed", type=int, default=20260820, help="rng seed; same seed, same data")
    ap.add_argument("--one-per-name", action="store_true",
                    help="one listing per crawled product name, name untouched. The vocabulary "
                         "becomes the ceiling, and photographs stop repeating")
    ap.add_argument("--ignore-existing", action="store_true",
                    help="add --total/leaves to every leaf instead of topping each up to "
                         "the target; keeps whatever skew the catalogue already has")
    ap.add_argument("--embed-queue", action="store_true",
                    help="mark the new listings embedding-stale so the indexer picks them up")
    ap.add_argument("--vocab", default=str(paths.VOCAB))
    ap.add_argument("--out", default=str(paths.OUT))
    args = ap.parse_args()

    # The seed is mixed with where this run starts, not used bare.
    #
    # A bare seed made every step of an incremental load replay the same random stream: three
    # steps meant listing k of each step drew the same axis value, the same condition and the
    # same price factor, so the catalogue held exact (name, price) triplets by construction —
    # groups of twenty identical name-and-price rows, turning up adjacent in search results. The
    # high-water mark differs per step, so mixing it in gives each step its own stream while a
    # single-step load with the same --seed stays reproducible.
    start = int(q("SELECT coalesce(max(id), 0) FROM catalog.listing")[0][0])
    seed = args.seed ^ (start * 2654435761)
    rng = random.Random(seed)
    random.seed(seed)
    out = Path(args.out)
    out.mkdir(exist_ok=True)
    now = datetime.now(timezone.utc)

    leaves = q("""SELECT c.id, c.name FROM catalog.category c
                   WHERE c.parent_id IS NOT NULL ORDER BY c.name""")
    leaf_id = {name: int(i) for i, name in leaves}
    unknown = [n for n in leaf_id if n not in PROFILES]
    if unknown:
        sys.exit(f"the database has leaves profiles.py does not know: {unknown}\n"
                 f"add them to profiles.PROFILES and leafmap.TREE, or re-run 01_categories.sql")
    print(f"{len(leaf_id)} leaves in the database", file=sys.stderr)

    # --total is the size the whole catalogue should end up, not how many rows to add. The
    # database this runs against is already lopsided — one leaf had 319 listings and three
    # had none — and adding an equal number to each preserves that difference exactly. So
    # each leaf is topped up to the target instead, and a leaf already over it is left alone
    # rather than having listings deleted: an even catalogue is worth having, not worth
    # destroying real rows for.
    existing = {name: int(n) for name, n in q(
        """SELECT c.name, count(l.id)
             FROM catalog.category c
             LEFT JOIN catalog.listing l ON l.category_id = c.id AND l.deleted_at IS NULL
            WHERE c.parent_id IS NOT NULL
            GROUP BY c.name""")}
    # The target is per leaf and absolute, remainder included, so it is the same number every
    # run. An earlier version added the remainder on top of `need` — which already subtracts
    # what is there — so every step of an incremental load handed another +1 to the same
    # alphabetically-first leaves and the spread grew by one per step. The distribution check in
    # `seed verify` is what caught it: 19 607 .. 19 609 after two of three passes.
    base, rem = divmod(args.total, len(leaf_id))
    order = sorted(leaf_id)
    target = {leaf: base + (1 if i < rem else 0) for i, leaf in enumerate(order)}
    if args.ignore_existing:
        need = dict(target)
    else:
        need = {leaf: max(0, target[leaf] - existing.get(leaf, 0)) for leaf in leaf_id}
        over = {k: existing[k] for k in leaf_id if existing.get(k, 0) > target[k]}
        if over:
            print(f"{len(over)} leaves already exceed their target and are left alone: "
                  f"{', '.join(f'{k} ({v})' for k, v in over.items())}", file=sys.stderr)
    per_leaf = base
    print(f"target {per_leaf} per leaf; {sum(existing.values())} listings already there, "
          f"{sum(need.values())} to generate", file=sys.stderr)

    pools = load_vocab(Path(args.vocab), per_leaf // 8)

    # Photos, pooled per leaf rather than one bag for the whole catalogue.
    #
    # The first version drew from every resource at random and put a bicycle on a rice cooker.
    # The pools exist because the original listings were scraped with their photographs, so a
    # resource used by a listing in "Nhà bếp & Ăn uống" is a picture of kitchenware — the
    # category of the listing that used it is the only label these rows carry, and it is
    # enough. A leaf with no photos of its own falls back to its root's, which is still a
    # picture of roughly the right kind of thing, and then to the whole set.
    pool_rows = q("""SELECT c.name, a.rid
                       FROM catalog.listing l
                       JOIN catalog.category c ON c.id = l.category_id
                       CROSS JOIN LATERAL unnest(l.attachments) AS a(rid)
                      WHERE l.account_id NOT IN (SELECT id FROM account.account
                                                  WHERE username LIKE 'bulk\\_%')
                        AND c.parent_id IS NOT NULL""")
    leaf_photos = {}
    for leaf, rid in pool_rows:
        leaf_photos.setdefault(leaf, set()).add(int(rid))
    root_of = {leaf: root for root, kids in TREE.items() for leaf in kids}
    root_photos = {}
    for leaf, ids in leaf_photos.items():
        root_photos.setdefault(root_of.get(leaf, ""), set()).update(ids)
    every = sorted({r for ids in leaf_photos.values() for r in ids})
    photos = {}
    for leaf in leaf_id:
        own = leaf_photos.get(leaf)
        if own:
            photos[leaf] = sorted(own)
        else:
            photos[leaf] = sorted(root_photos.get(root_of.get(leaf, ""), set())) or every
    borrowed = [l for l in leaf_id if not leaf_photos.get(l)]
    print(f"photos: {len(every)} resources over {len(leaf_photos)} leaves; "
          f"{len(borrowed)} leaves borrow from their root" +
          (f" ({', '.join(borrowed)})" if borrowed else ""), file=sys.stderr)

    areas = [tuple(r) for r in q("""SELECT DISTINCT province_code, province_name,
                                           ward_code, ward_name
                                      FROM account.contact""")]
    if len(areas) < 20:
        # Only a handful of demo contacts exist, so every generated seller would sit in one
        # of three wards and the area filter would have nothing to separate.
        vn = json.loads((paths.ROOT.parents[1] / "internal" / "module" / "account"
                         / "areas" / "vn.json").read_text(encoding="utf-8"))
        areas = [(p["code"], p["name"], w["code"], w["name"])
                 for p in vn for w in random.sample(p["wards"], min(6, len(p["wards"])))]
    print(f"{len(areas)} distinct wards for seller pickup addresses", file=sys.stderr)

    pw = q("SELECT password_hash FROM account.account WHERE password_hash IS NOT NULL LIMIT 1")
    pw_hash = pw[0][0] if pw else None

    max_account = int(q("SELECT coalesce(max(id), 0) FROM account.account")[0][0])
    max_contact = int(q("SELECT coalesce(max(id), 0) FROM account.contact")[0][0])
    max_listing = int(q("SELECT coalesce(max(id), 0) FROM catalog.listing")[0][0])
    max_variant = int(q("SELECT coalesce(max(id), 0) FROM catalog.variant")[0][0])
    max_review = int(q("SELECT coalesce(max(id), 0) FROM trust.review")[0][0])
    max_order = int(q("SELECT coalesce(max(id), 0) FROM \"order\".\"order\"")[0][0])

    accounts, contacts, sellers = make_sellers(
        args.sellers, max_account + 1, max_contact + 1, areas, pw_hash, now)
    buyer_rows, buyers = make_buyers(
        args.buyers, max_account + 1 + args.sellers, pw_hash, now)
    accounts += buyer_rows
    (out / "accounts.tsv").write_text("".join(accounts), encoding="utf-8")
    (out / "contacts.tsv").write_text("".join(contacts), encoding="utf-8")

    # Whatever an earlier run created is used too, not just this run's. Topping a catalogue up
    # from a hundred thousand to a million otherwise means the new nine hundred thousand are
    # owned by this run's three hundred sellers while the first run's three hundred keep their
    # original share — and reviewed by this run's buyers only. Accumulating both pools keeps
    # listings-per-seller falling as the catalogue grows, which is what a growing marketplace
    # looks like.
    prior_sellers = [
        {"id": int(aid), "area": (pc, pn, wc, wn)}
        for aid, pc, pn, wc, wn in q(
            """SELECT a.id, c.province_code, c.province_name, c.ward_code, c.ward_name
                 FROM account.account a
                 JOIN account.contact c ON c.account_id = a.id AND c.is_default_pickup
                WHERE a.username LIKE 'bulk_seller_%'""")]
    prior_buyers = [int(r[0]) for r in q(
        "SELECT id FROM account.account WHERE username LIKE 'bulk_buyer_%'")]
    if prior_sellers or prior_buyers:
        print(f"reusing {len(prior_sellers)} sellers and {len(prior_buyers)} buyers from "
              f"earlier runs", file=sys.stderr)
    sellers += prior_sellers
    buyers += prior_buyers

    print(f"{len(sellers)} sellers + {len(buyers)} buyers in the pool "
          f"({args.sellers} + {args.buyers} new, ids {max_account + 1}.."
          f"{max_account + args.sellers + args.buyers})", file=sys.stderr)

    lid = max_listing + 1
    vid = max_variant + 1
    rid = max_review + 1
    # review.order_id is NOT NULL and has no foreign key — the order module is another schema
    # and cannot be joined — so these count on from the real high-water mark. They name no
    # order row. That is the one thing here that is not internally consistent, and it is the
    # price of reviews existing at all without generating four hundred thousand orders
    # through the order and finance schemas.
    order_seq = itertools.count(max_order + max_review + 1_000_000)
    n_variants = n_reviews = n_tags = n_fav = n_sig = n_pop = 0
    tag_vocab = set()
    # Every product name that must not be used again, so --one-per-name means one across the
    # *catalogue* and not one per leaf, nor one per run.
    #
    # It starts from the names already on a live listing rather than from empty. `wipe` removes
    # only bulk-owned rows, so a re-run over an existing database begins with the real listings
    # still there — and the database vocabulary above reads its names from exactly those rows.
    # Starting empty therefore guaranteed one duplicate per surviving listing whose name the
    # pool offered: measured 857 of them, each a real listing paired with a generated twin.
    used_names = set()
    if args.one_per_name:
        used_names = {r[0] for r in q("""SELECT name FROM catalog.listing
                                          WHERE deleted_at IS NULL""") if r[0]}
        print(f"reserved {len(used_names)} names already on a live listing", file=sys.stderr)
    with open(out / "listings.tsv", "w", encoding="utf-8") as fl, \
         open(out / "variants.tsv", "w", encoding="utf-8") as fv, \
         open(out / "stock.tsv", "w", encoding="utf-8") as fs, \
         open(out / "listing_tags.tsv", "w", encoding="utf-8") as ft, \
         open(out / "reviews.tsv", "w", encoding="utf-8") as fr, \
         open(out / "favorites.tsv", "w", encoding="utf-8") as ff, \
         open(out / "signals.tsv", "w", encoding="utf-8") as fg, \
         open(out / "popularity.tsv", "w", encoding="utf-8") as fp:
        for k, (leaf, cid) in enumerate(sorted(leaf_id.items())):
            # Already includes this leaf's share of the remainder; see the target above.
            want = need[leaf]
            pool = pools[leaf] or [{"name": leaf, "brand": None, "photo": None,
                                    "price": PROFILES[leaf]["price_band"][0] * 2}]
            if args.one_per_name:
                # One listing per real product, and the vocabulary is the ceiling: a leaf with
                # 622 crawled names produces 622 listings and no more. Names are Tiki's own and
                # photographs land 1:1 — the two things sharing a base name used to break.
                # De-duplicated across the whole run, not per leaf: leafmap deliberately lets
                # one Tiki category feed two leaves, so the same product reaches two pools and a
                # per-leaf set left about 1% of names appearing twice.
                bases = []
                for it in pool:
                    if it["name"] not in used_names:
                        used_names.add(it["name"])
                        bases.append(it)
                rng.shuffle(bases)
                bases = bases[:want]
                name_axes = 0
            else:
                # How many distinct names one axis can make out of this pool. Under the target,
                # and the second axis joins the name — see make_listing.
                reach = len(pool) * len(PROFILES[leaf]["axes"][0][1])
                name_axes = 2 if reach < want else 1
                bases = [None] * want
            for base in bases:
                (listing, variants, stocks, tags, reviews, favorites, signals,
                 popularity) = make_listing(
                    lid, leaf, cid, pool, rng.choice(sellers), buyers, photos[leaf],
                    now, args.embed_queue, rng, name_axes, order_seq, base)
                fl.write(listing)
                for (vprice, attrs, pkg, feat, created), (qty, res, vsold, screated) \
                        in zip(variants, stocks):
                    fv.write(row(vid, lid, vprice, attrs, pkg, feat, created))
                    fs.write(row(vid, qty, res, vsold, screated))
                    vid += 1
                    n_variants += 1
                for tag in tags:
                    ft.write(row(lid, tag))
                    tag_vocab.add(tag)
                    n_tags += 1
                for r in reviews:
                    fr.write(row(rid, *r))
                    rid += 1
                    n_reviews += 1
                for f in favorites:
                    ff.write(row(*f))
                    n_fav += 1
                for g in signals:
                    fg.write(row(*g))
                    n_sig += 1
                if popularity:
                    fp.write(row(*popularity))
                    n_pop += 1
                lid += 1
            want = len(bases)
            print(f"  {leaf:38s} +{want:<6} -> {existing.get(leaf, 0) + want:>6}"
                  f"   ({len(pool)} base names"
                  f"{', 2 axes in name' if name_axes > 1 else ''})", file=sys.stderr)

    # The tag dictionary the join rows reference: listing_tag.tag has a foreign key onto it.
    # Marked embedding-stale because a tag with no vector is a tag the sparse half of search
    # cannot match — and there are only a few hundred, which is minutes rather than hours.
    with open(out / "tags.tsv", "w", encoding="utf-8") as f:
        for tag in sorted(tag_vocab):
            f.write(row(tag, None, now.isoformat()))

    total = lid - max_listing - 1
    (out / "load.sql").write_text(LOAD_SQL.format(
        max_account=max_account, max_contact=max_contact,
        max_listing=max_listing, max_variant=max_variant,
        max_review=max_review), encoding="utf-8")
    print(f"\n{total} listings, {n_variants} variants, {n_reviews} reviews, {n_tags} tag rows "
          f"over {len(tag_vocab)} tags, {n_fav} favourites, {n_sig} signals, "
          f"{n_pop} popularity rows -> {out}/", file=sys.stderr)
    print(f"ids: listing {max_listing + 1}..{lid - 1}, variant {max_variant + 1}..{vid - 1}, "
          f"review {max_review + 1}..{rid - 1}", file=sys.stderr)
    print("load it with ./run.sh", file=sys.stderr)


LOAD_SQL = r"""-- Written by generate.py. Loads out/*.tsv into the catalogue.
--
-- Staging tables first, then one INSERT ... SELECT into each real table. The staging tables
-- are UNLOGGED and have no indexes, so COPY into them is close to the speed of the disk;
-- inserting from there into the real tables pays for the indexes once, in bulk, instead of
-- per row.
--
-- The ids in the TSVs were chosen by generate.py against the high-water mark it read. If
-- something else has written to these tables since, they collide, so the mark is re-checked
-- here before anything is inserted and the whole thing is one transaction.

\set ON_ERROR_STOP on
\timing on
BEGIN;

DO $$
BEGIN
  IF (SELECT coalesce(max(id), 0) FROM catalog.listing) <> {max_listing} THEN
    RAISE EXCEPTION 'catalog.listing moved since generate.py ran (expected max id {max_listing}, found %). Re-run generate.py.',
      (SELECT coalesce(max(id), 0) FROM catalog.listing);
  END IF;
  IF (SELECT coalesce(max(id), 0) FROM catalog.variant) <> {max_variant} THEN
    RAISE EXCEPTION 'catalog.variant moved since generate.py ran (expected max id {max_variant}, found %). Re-run generate.py.',
      (SELECT coalesce(max(id), 0) FROM catalog.variant);
  END IF;
  IF (SELECT coalesce(max(id), 0) FROM account.account) <> {max_account} THEN
    RAISE EXCEPTION 'account.account moved since generate.py ran (expected max id {max_account}, found %). Re-run generate.py.',
      (SELECT coalesce(max(id), 0) FROM account.account);
  END IF;
  IF (SELECT coalesce(max(id), 0) FROM trust.review) <> {max_review} THEN
    RAISE EXCEPTION 'trust.review moved since generate.py ran (expected max id {max_review}, found %). Re-run generate.py.',
      (SELECT coalesce(max(id), 0) FROM trust.review);
  END IF;
END $$;

CREATE UNLOGGED TABLE stage_account (
  id bigint, status text, role text, phone text, email text, username text,
  password_hash text, email_verified boolean, created_at timestamptz, name text,
  description text, gender text, country text, locale text, timezone text);
CREATE UNLOGGED TABLE stage_contact (
  id bigint, account_id bigint, full_name text, phone text, address_type text,
  is_default_delivery boolean, is_default_pickup boolean, created_at timestamptz,
  country text, province_code text, province_name text, ward_code text, ward_name text,
  address text, provider_codes jsonb);
CREATE UNLOGGED TABLE stage_listing (
  id bigint, slug text, account_id bigint, category_id bigint, status text, name text,
  description text, specifications jsonb, attachments bigint[], price_mode text,
  condition text, currency text, cached_rating double precision,
  cached_review_count bigint, cached_sold bigint, province_code text, province_name text,
  ward_code text, ward_name text, location text, created_at timestamptz,
  embedding_stale_at timestamptz);
CREATE UNLOGGED TABLE stage_variant (
  id bigint, listing_id bigint, price bigint, attributes jsonb, package_details jsonb,
  is_featured boolean, created_at timestamptz);
CREATE UNLOGGED TABLE stage_stock (
  variant_id bigint, quantity bigint, reserved bigint, sold bigint, created_at timestamptz);
CREATE UNLOGGED TABLE stage_tag (id text, description text, embedding_stale_at timestamptz);
CREATE UNLOGGED TABLE stage_listing_tag (listing_id bigint, tag text);
CREATE UNLOGGED TABLE stage_review (
  id bigint, listing_id bigint, order_id bigint, author_id bigint, seller_id bigint,
  rating smallint, body text, helpful_count bigint, not_helpful_count bigint,
  reply_count bigint, created_at timestamptz);
CREATE UNLOGGED TABLE stage_favorite (account_id bigint, listing_id bigint, created_at timestamptz);
CREATE UNLOGGED TABLE stage_signal (
  account_id bigint, listing_id bigint, type text, created_at timestamptz);
CREATE UNLOGGED TABLE stage_popularity (
  listing_id bigint, score double precision, view_count bigint, click_count bigint,
  dismiss_count bigint, purchase_count bigint, updated_at timestamptz);

\copy stage_account from 'accounts.tsv'
\copy stage_contact from 'contacts.tsv'
\copy stage_listing from 'listings.tsv'
\copy stage_variant from 'variants.tsv'
\copy stage_stock   from 'stock.tsv'
\copy stage_tag from 'tags.tsv'
\copy stage_listing_tag from 'listing_tags.tsv'
\copy stage_review from 'reviews.tsv'
\copy stage_favorite from 'favorites.tsv'
\copy stage_signal from 'signals.tsv'
\copy stage_popularity from 'popularity.tsv'

INSERT INTO account.account
  (id, status, role, phone, email, username, password_hash, email_verified, created_at,
   name, description, gender, country, locale, timezone)
OVERRIDING SYSTEM VALUE
SELECT id, status::account.account_status, role::account.account_role, phone, email,
       username, password_hash, email_verified, created_at, name, description,
       gender::account.profile_gender, country, locale, timezone
  FROM stage_account;

INSERT INTO account.contact
  (id, account_id, full_name, phone, address_type, is_default_delivery, is_default_pickup,
   created_at, country, province_code, province_name, ward_code, ward_name, address,
   provider_codes)
OVERRIDING SYSTEM VALUE
SELECT id, account_id, full_name, phone, address_type::account.contact_address_type,
       is_default_delivery, is_default_pickup, created_at, country, province_code,
       province_name, ward_code, ward_name, address, provider_codes
  FROM stage_contact;

INSERT INTO catalog.listing
  (id, slug, account_id, category_id, status, name, description, specifications,
   attachments, price_mode, condition, currency, cached_rating, cached_review_count,
   cached_sold, province_code, province_name, ward_code, ward_name, location, created_at,
   embedding_stale_at)
OVERRIDING SYSTEM VALUE
SELECT id, slug, account_id, category_id, status::catalog.listing_status, name, description,
       specifications, attachments, price_mode::catalog.price_mode,
       condition::catalog.listing_condition, currency, cached_rating, cached_review_count,
       cached_sold, province_code, province_name, ward_code, ward_name,
       ST_GeogFromText(location), created_at, embedding_stale_at
  FROM stage_listing;

INSERT INTO catalog.variant
  (id, listing_id, price, attributes, package_details, is_featured, created_at)
OVERRIDING SYSTEM VALUE
SELECT id, listing_id, price, attributes, package_details, is_featured, created_at
  FROM stage_variant;

INSERT INTO catalog.stock (variant_id, quantity, reserved, sold, created_at)
SELECT variant_id, quantity, reserved, sold, created_at FROM stage_stock;

-- Tags before the join rows: listing_tag.tag has a foreign key onto them. ON CONFLICT
-- because most of these already exist — the brands especially — and an existing tag keeps
-- whatever description and embedding it already earned.
INSERT INTO catalog.tag (id, description, embedding_stale_at)
SELECT id, description, embedding_stale_at FROM stage_tag
ON CONFLICT (id) DO NOTHING;

-- No explicit id: nothing references listing_tag.id, and the gateway is writing this table
-- while a load runs — an interaction recorded mid-load takes the next sequence value and
-- collides with a hand-picked one. Only listing, variant and review get chosen ids, because
-- only those are pointed at by rows that do not exist yet.
INSERT INTO catalog.listing_tag (listing_id, tag)
SELECT listing_id, tag FROM stage_listing_tag;

INSERT INTO trust.review
  (id, listing_id, order_id, author_id, seller_id, rating, body, helpful_count,
   not_helpful_count, reply_count, created_at)
OVERRIDING SYSTEM VALUE
SELECT id, listing_id, order_id, author_id, seller_id, rating, body, helpful_count,
       not_helpful_count, reply_count, created_at
  FROM stage_review;

INSERT INTO catalog.favorite (account_id, listing_id, created_at)
SELECT account_id, listing_id, created_at FROM stage_favorite
ON CONFLICT (account_id, listing_id) DO NOTHING;

-- Same reason as listing_tag above, and this is the table it actually happened on:
-- listing_signal_pkey, mid-load, because the gateway had recorded a view.
INSERT INTO catalog.listing_signal (account_id, listing_id, type, created_at)
SELECT account_id, listing_id, type, created_at FROM stage_signal;

INSERT INTO observability.listing_popularity
  (listing_id, score, view_count, click_count, dismiss_count, purchase_count, updated_at)
SELECT listing_id, score, view_count, click_count, dismiss_count, purchase_count, updated_at
  FROM stage_popularity
ON CONFLICT (listing_id) DO NOTHING;

-- Move the identity sequences past what was just inserted, or the next ordinary INSERT
-- picks an id one of these rows already has.
SELECT setval(pg_get_serial_sequence('account.account', 'id'),
              (SELECT max(id) FROM account.account));
SELECT setval(pg_get_serial_sequence('account.contact', 'id'),
              (SELECT max(id) FROM account.contact));
SELECT setval(pg_get_serial_sequence('catalog.listing', 'id'),
              (SELECT max(id) FROM catalog.listing));
SELECT setval(pg_get_serial_sequence('catalog.variant', 'id'),
              (SELECT max(id) FROM catalog.variant));
SELECT setval(pg_get_serial_sequence('trust.review', 'id'),
              (SELECT max(id) FROM trust.review));

DROP TABLE stage_account, stage_contact, stage_listing, stage_variant, stage_stock,
           stage_tag, stage_listing_tag, stage_review, stage_favorite, stage_signal,
           stage_popularity;
COMMIT;

ANALYZE catalog.listing;
ANALYZE catalog.variant;
ANALYZE catalog.stock;
ANALYZE catalog.listing_tag;
ANALYZE catalog.listing_signal;
ANALYZE trust.review;

-- The one invariant worth asserting after a load this size: a listing's review count is the
-- number of reviews it actually has. That is the bug this load was changed to stop shipping.
DO $$
DECLARE bad bigint;
BEGIN
  SELECT count(*) INTO bad FROM catalog.listing l
   WHERE l.cached_review_count <> (SELECT count(*) FROM trust.review r WHERE r.listing_id = l.id);
  IF bad > 0 THEN
    RAISE WARNING 'cached_review_count disagrees with trust.review on % listings', bad;
  ELSE
    RAISE NOTICE 'cached_review_count matches trust.review on every listing';
  END IF;
END $$;

SELECT c.name AS leaf, count(*) AS listings,
       count(*) FILTER (WHERE l.status = 'active') AS active
  FROM catalog.listing l JOIN catalog.category c ON c.id = l.category_id
 WHERE l.deleted_at IS NULL
 GROUP BY c.name ORDER BY c.name;
"""


if __name__ == "__main__":
    main()
