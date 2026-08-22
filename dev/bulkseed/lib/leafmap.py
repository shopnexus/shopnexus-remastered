"""Which Tiki categories feed each of our catalogue leaves.

The map is by hand and it has to be: Tiki's tree and ours disagree on where things go —
a phone case is "Phụ Kiện Điện Thoại" three levels under "Thiết Bị Số" there and a leaf
of "Điện tử" here — and no name-similarity trick gets that right. Ids come from
`https://tiki.vn/api/v2/categories?parent_id=<id>`, walked once and recorded, because the
crawler asking for the tree on every run would be three hundred requests before the first
product name.

A Tiki id may feed two leaves. That is not a bug: "Phụ kiện đồng hồ" is watch parts, which
belong with the watches and also with the fashion accessories, and a name landing in both
pools only means it can be drawn for either.
"""

# ROOT -> {LEAF: [tiki category ids]}. The roots are ours; nothing is crawled for them,
# they hold no listings, they exist so the storefront has a two-level menu.
TREE = {
    "Điện tử": {
        "Điện thoại & Máy tính bảng": [1795, 1794, 8061, 1796, 28856],
        "Máy tính & Thiết bị mạng":   [8095, 8093, 2663, 8060, 12884, 8129],
        "Máy ảnh & Quay phim":        [28806, 1818, 28794, 28814, 28822, 28834, 4077, 8047],
        "Âm thanh & Tai nghe":        [8215, 26568],
        "Trò chơi điện tử & Gaming":  [2667, 6742, 9013, 8251, 12676, 7273, 12884],
        "Phần mềm & Hàng hóa số":     [11312, 69482, 11332, 11327],
        "Phụ kiện điện tử":           [8214, 28670, 28432, 8039],
    },
    "Thời trang & Phụ kiện": {
        "Thời trang & Quần áo": [941, 5404, 27600, 936, 1702, 4554, 1508, 934, 935, 933, 6179,
                                 917, 918, 925, 10382, 4546, 27562, 27548, 67309, 67329, 27570],
        "Giày dép":             [27572, 5340, 1581, 5341, 5342, 8337,
                                 8355, 27604, 4550, 1192, 984, 4551, 981, 1008],
        "Túi xách & Vali":      [27608, 27612, 8387, 6526, 68140,
                                 4559, 4558, 4560, 4561, 8350, 5337, 958, 959, 49650],
        "Đồng hồ":              [1778, 977, 11375],
        "Trang sức":            [8374, 15318, 15320, 15250, 15236, 15936, 68613],
        "Phụ kiện thời trang":  [8370, 975, 27550, 8352, 8338, 8357, 27542],
    },
    "Sắc đẹp & Sức khỏe": {
        "Sắc đẹp & Chăm sóc cá nhân": [1582, 1584, 1594, 1592, 1591, 1595, 2306, 5873, 1625],
        "Sức khỏe & Thể chất":        [2307, 2322, 8142, 10803],
    },
    "Nhà cửa & Đời sống": {
        "Nội thất gia đình":                  [2150, 8313],
        "Trang trí nhà cửa & Đèn chiếu sáng": [1973, 2015, 23054],
        "Nhà bếp & Ăn uống":                  [1951, 1954, 1884, 4399],
        "Chăn ga gối đệm & Phòng tắm":        [1966, 4400, 4388],
        "Thiết bị gia dụng lớn":              [5015, 3862, 3863, 3864, 3865, 3866, 2328, 3868, 3869, 8074, 1946],
        "Thiết bị gia dụng nhỏ":              [53050, 54514, 4386, 4387, 4441],
        "Sân vườn & Ngoại thất":              [2223, 3879, 5995, 23422, 8316, 23446, 23434],
    },
    "Bách hóa & Mẹ bé": {
        "Tạp hóa & Thực phẩm":  [4421, 15074, 4422, 22998, 53562, 53582, 68576, 24024,
                                 44824, 54276, 54290, 54302, 54452, 69118, 69032, 54430],
        "Mẹ & Bé":              [2551, 8339, 6568, 10416, 11601, 2640, 5164, 10418],
        "Đồ dùng cho thú cưng": [5451, 54466],
    },
    "Thể thao & Giải trí": {
        "Thể thao & Dã ngoại":  [8411, 4227, 23120, 24002, 6826, 6827, 24306, 8413,
                                 67485, 67647, 67703, 69498, 24064],
        "Đồ chơi & Trò chơi":   [5250, 1948, 4493, 2848, 4506, 10445, 4594, 4505, 1938, 10538,
                                  4504, 8469, 10523],
        "Nhạc cụ":              [10068, 23468, 23552, 23450, 23466],
        "Nghệ thuật & Thủ công": [18328, 18344, 18342, 8265, 18332, 18334, 68208, 18346, 6215, 6541,
                                  4624, 6246, 68205],
    },
    "Sách & Văn phòng phẩm": {
        "Sách & Ấn phẩm":                  [316, 320, 21298],
        "Văn phòng phẩm & Dụng cụ học tập": [7741, 1858, 1862, 2365, 2538, 2368, 1899, 5674, 1907, 18378, 2452],
    },
    "Xe cộ & Công nghiệp": {
        "Ô tô & Xe máy":            [8597, 6070, 8431, 8595, 24832, 17208],
        "Công cụ & Phần cứng":      [1974, 2760, 12986, 12980, 2806, 27188, 12984, 4452, 2759, 2327],
        "Công nghiệp & Thương mại": [20824, 21166, 21268, 21442, 4452, 12986, 27188, 12984, 2327,
                                    4399, 4441],
    },
}

# Eight leaves started with a single source category each and came out thin. They are the
# widened ones — "Trang sức" reads its seven subcategories rather than just "Đồ chơi và Trang
# sức", and so on. More categories is about variety and not about volume: there is no
# pagination cap, page 40 of a category still answers 40 rows, so depth is what --max-pages
# buys. What one subcategory cannot give is a spread of *kinds* — crawling "Vòng tay" and
# "Bông tai" and "Nhẫn" separately is what stops a leaf being three thousand bracelets.
#
# "Trò chơi điện tử & Gaming" is the exception that stayed thin. Tiki lists about a hundred
# products under "Thiết Bị Chơi Game" and its five children together, so 12884 — the
# peripherals branch, where keyboards, mice and headsets actually sit — feeds it as well as
# feeding "Máy tính & Thiết bị mạng".

LEAVES = [leaf for kids in TREE.values() for leaf in kids]

# The duplicate categories the live database grew, and where their listings go. Applied by
# 01_categories.sql, not here — this is the record of the decision, the SQL is the edit.
MERGE = {
    "Laptop & Máy tính":     "Máy tính & Thiết bị mạng",
    "Đồ gia dụng":           "Thiết bị gia dụng nhỏ",
    "Nội thất & Trang trí":  "Nội thất gia đình",
    "Sách & Văn phòng phẩm": "Sách & Ấn phẩm",   # the old leaf, not the new root
    "Thời trang & Phụ kiện": "Phụ kiện thời trang",
    "Xe cộ & Phụ tùng":      "Ô tô & Xe máy",
    "Chung":                 "Phụ kiện điện tử",
    "Điện tử":               "Phụ kiện điện tử",  # 9357 is promoted to root; its 27 listings move
}

if __name__ == "__main__":
    ids = [i for kids in TREE.values() for v in kids.values() for i in v]
    print(f"{len(TREE)} roots, {len(LEAVES)} leaves, {len(ids)} tiki ids ({len(set(ids))} distinct)")
    for root, kids in TREE.items():
        print(f"\n{root}")
        for leaf, src in kids.items():
            print(f"  {leaf:38s} <- {len(src)} tiki cats")
