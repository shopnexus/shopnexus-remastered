"""Per-leaf shape: what a listing in this leaf varies by, and what its spec sheet says.

The generator needs two things a product name does not carry. First, the axis a listing
varies along — a t-shirt comes in sizes, a phone in storage tiers, a bag of coffee in
weights — because that is what makes a variant a variant, and giving every listing
"màu: đen / trắng" would leave the whole catalogue with one shape. Second, the spec keys a
buyer expects to see, since `listing.specifications` is JSONB with no schema and an empty
object renders as an empty table.

`axes` is ordered: the first is used when a listing gets one variant dimension, the first
two when it gets two. Nothing uses three — a listing with colour × size × capacity is
24 variants and no C2C seller lists that.

`price_band` is the sanity fence, in đồng. The crawled price is real but it is one product's;
jittering it can walk somewhere silly, and a phone case at 40 million đ is worse for a demo
than a boring one. Bands are wide on purpose — they catch nonsense, not variety.
"""

CLOTHES_SIZES = ["S", "M", "L", "XL", "XXL"]
COLORS = ["Đen", "Trắng", "Xám", "Xanh navy", "Be", "Nâu", "Xanh rêu", "Đỏ đô"]
SHOE_SIZES = ["36", "37", "38", "39", "40", "41", "42", "43"]

# leaf -> (axes, spec keys, price band)
#   axes:  [(attribute name, [values])]
#   specs: [(key, [candidate values])]  — a listing takes a random value for each key
PROFILES = {
    "Điện thoại & Máy tính bảng": {
        "axes": [("Dung lượng", ["32GB", "64GB", "128GB", "256GB", "512GB", "1TB"]),
                 ("Màu", ["Đen", "Trắng", "Xanh dương", "Xanh rêu", "Tím", "Vàng đồng",
                          "Hồng", "Bạc", "Đỏ", "Xám titan"])],
        "specs": [("Hãng", None), ("Màn hình", ["6.1 inch", "6.5 inch", "6.7 inch", "10.9 inch"]),
                  ("RAM", ["4GB", "6GB", "8GB", "12GB"]),
                  ("Pin", ["4000mAh", "4500mAh", "5000mAh", "6000mAh"]),
                  ("Bảo hành", ["Còn bảo hành hãng", "Hết bảo hành", "Bảo hành shop 3 tháng"])],
        "price_band": (400_000, 60_000_000),
    },
    "Máy tính & Thiết bị mạng": {
        "axes": [("Cấu hình", ["8GB/256GB", "8GB/512GB", "16GB/512GB", "16GB/1TB", "32GB/1TB"]),
                 ("Màu", ["Bạc", "Xám", "Đen"])],
        "specs": [("Hãng", None), ("CPU", ["Core i5", "Core i7", "Ryzen 5", "Ryzen 7", "Apple M2"]),
                  ("Màn hình", ["13.3 inch", "14 inch", "15.6 inch", "16 inch"]),
                  ("Tình trạng máy", ["Đẹp 98%", "Đẹp 95%", "Trầy nhẹ", "Như mới"])],
        "price_band": (80_000, 80_000_000),
    },
    "Máy ảnh & Quay phim": {
        "axes": [("Bộ sản phẩm", ["Body", "Kit 18-55mm", "Kit 16-50mm", "Combo 2 lens"])],
        "specs": [("Hãng", None), ("Độ phân giải", ["20MP", "24MP", "26MP", "33MP"]),
                  ("Số shot", ["dưới 5k", "5k-15k", "15k-30k"]),
                  ("Phụ kiện kèm", ["Đủ box, sạc, pin", "Chỉ body và sạc", "Kèm thẻ 64GB"])],
        "price_band": (150_000, 90_000_000),
    },
    "Âm thanh & Tai nghe": {
        "axes": [("Màu", ["Đen", "Trắng", "Xanh", "Hồng"])],
        "specs": [("Hãng", None), ("Kiểu kết nối", ["Bluetooth 5.0", "Bluetooth 5.3", "Có dây 3.5mm", "USB-C"]),
                  ("Thời lượng pin", ["6 giờ", "8 giờ", "24 giờ", "40 giờ"]),
                  ("Chống ồn", ["Có ANC", "Không"])],
        "price_band": (50_000, 25_000_000),
    },
    "Trò chơi điện tử & Gaming": {
        "axes": [("Phiên bản", ["Bản thường", "Bản Pro", "Bản Lite"]),
                 ("Màu", ["Đen", "Trắng", "Xanh neon"])],
        "specs": [("Hãng", None), ("Kết nối", ["Có dây", "Không dây 2.4G", "Bluetooth"]),
                  ("Tương thích", ["PC", "PC / PS5", "Nintendo Switch", "Đa nền tảng"])],
        "price_band": (60_000, 30_000_000),
    },
    "Phần mềm & Hàng hóa số": {
        "axes": [("Thời hạn", ["1 tháng", "3 tháng", "6 tháng", "1 năm", "2 năm",
                               "Vĩnh viễn"]),
                 ("Số thiết bị", ["1 thiết bị", "2 thiết bị", "5 thiết bị", "không giới hạn"])],
        "specs": [("Loại", ["Key bản quyền", "Tài khoản", "Voucher điện tử"]),
                  ("Giao hàng", ["Gửi qua email", "Gửi qua chat"]),
                  ("Bảo hành", ["1 tháng", "3 tháng", "Không"])],
        "price_band": (20_000, 12_000_000),
    },
    "Phụ kiện điện tử": {
        "axes": [("Màu", COLORS), ("Dài cáp", ["0.5m", "1m", "1.5m", "2m"])],
        "specs": [("Hãng", None), ("Chuẩn cổng", ["USB-C", "Lightning", "Micro USB", "USB-A"]),
                  ("Công suất", ["18W", "20W", "33W", "65W", "không áp dụng"])],
        "price_band": (10_000, 5_000_000),
    },
    "Thời trang & Quần áo": {
        "axes": [("Size", CLOTHES_SIZES), ("Màu", COLORS)],
        "specs": [("Chất liệu", ["Cotton 100%", "Cotton pha", "Thun lạnh", "Linen", "Jean", "Nỉ bông"]),
                  ("Kiểu dáng", ["Regular", "Oversize", "Slim fit", "Suông"]),
                  ("Xuất xứ", ["Việt Nam", "Hàng Quảng Châu", "Thái Lan"])],
        "price_band": (30_000, 6_000_000),
    },
    "Giày dép": {
        "axes": [("Size", SHOE_SIZES), ("Màu", ["Đen", "Trắng", "Nâu", "Xám", "Kem"])],
        "specs": [("Chất liệu", ["Da bò thật", "Da PU", "Canvas", "Vải dệt", "Cao su"]),
                  ("Đế", ["Đế cao su", "Đế phylon", "Đế EVA"]),
                  ("Xuất xứ", ["Việt Nam", "Trung Quốc"])],
        "price_band": (50_000, 12_000_000),
    },
    "Túi xách & Vali": {
        "axes": [("Màu", COLORS), ("Kích cỡ", ["20 inch", "24 inch", "28 inch", "Size nhỏ", "Size lớn"])],
        "specs": [("Chất liệu", ["Da PU", "Vải canvas", "Nhựa ABS", "Nhựa PC", "Vải oxford"]),
                  ("Ngăn chính", ["1 ngăn", "2 ngăn", "3 ngăn"]),
                  ("Chống nước", ["Có", "Không"])],
        "price_band": (60_000, 15_000_000),
    },
    "Đồng hồ": {
        "axes": [("Màu dây", ["Đen", "Nâu", "Bạc", "Vàng hồng"])],
        "specs": [("Hãng", None), ("Đường kính mặt", ["36mm", "38mm", "40mm", "42mm", "44mm"]),
                  ("Máy", ["Automatic", "Quartz", "Pin điện tử"]),
                  ("Chống nước", ["3ATM", "5ATM", "10ATM"])],
        "price_band": (100_000, 80_000_000),
    },
    "Trang sức": {
        "axes": [("Kích cỡ", ["Size 6", "Size 7", "Size 8", "Free size"])],
        "specs": [("Chất liệu", ["Bạc 925", "Vàng 10K", "Vàng 18K", "Thép không gỉ", "Inox mạ vàng"]),
                  ("Đá", ["Đá CZ", "Ngọc trai", "Không đá"]),
                  ("Kèm theo", ["Hộp đựng", "Giấy kiểm định", "Không"])],
        "price_band": (50_000, 40_000_000),
    },
    "Phụ kiện thời trang": {
        "axes": [("Màu", COLORS)],
        "specs": [("Chất liệu", ["Da thật", "Da PU", "Kim loại", "Nhựa acetate", "Vải"]),
                  ("Kiểu", ["Unisex", "Nam", "Nữ"])],
        "price_band": (20_000, 8_000_000),
    },
    "Sắc đẹp & Chăm sóc cá nhân": {
        "axes": [("Dung tích", ["30ml", "50ml", "100ml", "150ml", "250ml"])],
        "specs": [("Hãng", None), ("Loại da", ["Mọi loại da", "Da dầu", "Da khô", "Da hỗn hợp"]),
                  ("Xuất xứ", ["Hàn Quốc", "Nhật Bản", "Việt Nam", "Pháp", "Mỹ"]),
                  ("Hạn dùng", ["Còn trên 2 năm", "Còn trên 1 năm"])],
        "price_band": (25_000, 8_000_000),
    },
    "Sức khỏe & Thể chất": {
        "axes": [("Quy cách", ["Hộp 30 viên", "Hộp 60 viên", "Hộp 90 viên", "Lọ 500g", "Lọ 1kg"])],
        "specs": [("Hãng", None), ("Dạng", ["Viên nén", "Viên nang", "Bột", "Nước"]),
                  ("Xuất xứ", ["Mỹ", "Nhật Bản", "Việt Nam", "Đức"])],
        "price_band": (40_000, 12_000_000),
    },
    "Nội thất gia đình": {
        "axes": [("Kích thước", ["60x40cm", "80x60cm", "1m2", "1m6", "1m8"]),
                 ("Màu", ["Gỗ tự nhiên", "Trắng", "Đen", "Nâu walnut"])],
        "specs": [("Chất liệu", ["Gỗ MDF", "Gỗ cao su", "Gỗ sồi", "Sắt sơn tĩnh điện", "Nhựa"]),
                  ("Lắp đặt", ["Cần tự lắp", "Đã lắp sẵn"]),
                  ("Tải trọng", ["50kg", "100kg", "200kg"])],
        "price_band": (100_000, 60_000_000),
    },
    "Trang trí nhà cửa & Đèn chiếu sáng": {
        "axes": [("Màu ánh sáng", ["Trắng", "Vàng", "Trung tính"]), ("Công suất", ["5W", "9W", "12W", "18W"])],
        "specs": [("Chất liệu", ["Nhôm", "Nhựa", "Thủy tinh", "Gốm", "Gỗ"]),
                  ("Nguồn điện", ["220V", "USB", "Pin AA"])],
        "price_band": (20_000, 20_000_000),
    },
    "Nhà bếp & Ăn uống": {
        "axes": [("Dung tích", ["0.5L", "1L", "1.8L", "2.5L", "5L"]), ("Màu", ["Bạc", "Đen", "Trắng"])],
        "specs": [("Chất liệu", ["Inox 304", "Nhôm", "Thủy tinh", "Nhựa PP", "Gốm sứ"]),
                  ("Dùng được", ["Bếp từ", "Mọi loại bếp", "Lò vi sóng", "Máy rửa bát"])],
        "price_band": (25_000, 25_000_000),
    },
    "Chăn ga gối đệm & Phòng tắm": {
        "axes": [("Kích thước", ["1m2 x 2m", "1m6 x 2m", "1m8 x 2m", "Free size"]), ("Màu", COLORS)],
        "specs": [("Chất liệu", ["Cotton", "Tencel", "Microfiber", "Nhựa ABS", "Inox"]),
                  ("Bộ gồm", ["1 ga + 2 vỏ gối", "1 ga + 4 vỏ gối", "1 sản phẩm"])],
        "price_band": (30_000, 15_000_000),
    },
    "Thiết bị gia dụng lớn": {
        "axes": [("Dung tích", ["90L", "180L", "250L", "350L", "500L"])],
        "specs": [("Hãng", None), ("Công nghệ", ["Inverter", "Không inverter"]),
                  ("Điện năng", ["1 sao", "3 sao", "5 sao"]),
                  ("Bảo hành", ["Còn bảo hành hãng", "Hết bảo hành"])],
        "price_band": (500_000, 90_000_000),
    },
    "Thiết bị gia dụng nhỏ": {
        "axes": [("Màu", ["Trắng", "Đen", "Xanh mint"]), ("Công suất", ["300W", "600W", "1000W", "1500W"])],
        "specs": [("Hãng", None), ("Điện áp", ["220V"]),
                  ("Phụ kiện", ["Đủ phụ kiện", "Thiếu 1 phụ kiện"])],
        "price_band": (25_000, 20_000_000),
    },
    "Sân vườn & Ngoại thất": {
        "axes": [("Kích cỡ", ["Nhỏ", "Vừa", "Lớn"])],
        "specs": [("Chất liệu", ["Nhựa", "Sắt", "Gỗ", "Xi măng", "Composite"]),
                  ("Dùng ngoài trời", ["Có", "Có, cần che mưa"])],
        "price_band": (20_000, 20_000_000),
    },
    "Tạp hóa & Thực phẩm": {
        "axes": [("Khối lượng", ["200g", "500g", "1kg", "2kg", "Combo 3 gói"])],
        "specs": [("Hãng", None), ("Hạn sử dụng", ["Còn 6 tháng", "Còn 12 tháng", "Còn 18 tháng"]),
                  ("Bảo quản", ["Nơi khô ráo", "Ngăn mát", "Đông lạnh"]),
                  ("Xuất xứ", ["Việt Nam", "Thái Lan", "Hàn Quốc", "Nhật Bản"])],
        "price_band": (10_000, 5_000_000),
    },
    "Mẹ & Bé": {
        "axes": [("Size", ["Newborn", "S", "M", "L", "XL"]), ("Số lượng", ["Lẻ 1", "Combo 2", "Combo 5"])],
        "specs": [("Hãng", None), ("Độ tuổi", ["0-6 tháng", "6-12 tháng", "1-3 tuổi", "3-6 tuổi"]),
                  ("Chứng nhận", ["Có CO CQ", "Không"])],
        "price_band": (20_000, 15_000_000),
    },
    "Đồ dùng cho thú cưng": {
        "axes": [("Kích cỡ", ["Size S", "Size M", "Size L"]), ("Màu", ["Xám", "Nâu", "Hồng", "Xanh"])],
        "specs": [("Dành cho", ["Mèo", "Chó nhỏ", "Chó lớn", "Mèo và chó"]),
                  ("Chất liệu", ["Nhựa", "Vải bố", "Inox", "Cotton"])],
        "price_band": (20_000, 8_000_000),
    },
    "Thể thao & Dã ngoại": {
        "axes": [("Size", CLOTHES_SIZES), ("Màu", COLORS)],
        "specs": [("Hãng", None), ("Môn", ["Cầu lông", "Bóng đá", "Gym", "Chạy bộ", "Cắm trại", "Bơi"]),
                  ("Chất liệu", ["Polyester", "Nhôm", "Carbon", "Cao su", "Vải chống nước"])],
        "price_band": (30_000, 30_000_000),
    },
    "Đồ chơi & Trò chơi": {
        "axes": [("Phiên bản", ["Bản thường", "Bản giới hạn", "Set nhỏ", "Set lớn"])],
        "specs": [("Độ tuổi", ["3+", "6+", "8+", "12+", "14+"]),
                  ("Chất liệu", ["Nhựa ABS", "Gỗ", "Bông", "Giấy"]),
                  ("Số mảnh", ["dưới 100", "100-500", "500-1000", "trên 1000"])],
        "price_band": (15_000, 12_000_000),
    },
    "Nhạc cụ": {
        "axes": [("Màu", ["Gỗ tự nhiên", "Đen", "Sunburst"])],
        "specs": [("Hãng", None), ("Chất liệu", ["Gỗ thông", "Gỗ hồng đào", "Gỗ điệp", "Nhựa"]),
                  ("Kèm theo", ["Bao đựng", "Bao và capo", "Không"])],
        "price_band": (30_000, 40_000_000),
    },
    "Nghệ thuật & Thủ công": {
        "axes": [("Kích cỡ", ["20x30cm", "30x40cm", "40x60cm", "Free size"])],
        "specs": [("Chất liệu", ["Giấy mỹ thuật", "Canvas", "Gỗ", "Vải", "Đất sét"]),
                  ("Hình thức", ["Làm thủ công", "In sẵn", "Bộ tự làm"])],
        "price_band": (15_000, 10_000_000),
    },
    "Sách & Ấn phẩm": {
        "axes": [("Bản", ["Bìa mềm", "Bìa cứng", "Bản đặc biệt"])],
        "specs": [("Nhà xuất bản", ["NXB Trẻ", "NXB Kim Đồng", "Nhã Nam", "First News", "NXB Hội Nhà Văn"]),
                  ("Số trang", ["dưới 200", "200-400", "400-600", "trên 600"]),
                  ("Tình trạng sách", ["Mới nguyên seal", "Đã đọc, còn đẹp", "Có highlight"])],
        "price_band": (15_000, 3_000_000),
    },
    "Văn phòng phẩm & Dụng cụ học tập": {
        "axes": [("Màu", COLORS), ("Số lượng", ["Lẻ 1", "Bộ 5", "Bộ 10", "Hộp 12"])],
        "specs": [("Hãng", None), ("Chất liệu", ["Nhựa", "Kim loại", "Giấy", "Gỗ"]),
                  ("Quy cách", ["A4", "A5", "B5", "không áp dụng"])],
        "price_band": (5_000, 3_000_000),
    },
    "Ô tô & Xe máy": {
        "axes": [("Dòng xe", ["Xe số", "Xe ga", "Xe tay côn", "Ô tô 4 chỗ", "Ô tô 7 chỗ"]),
                 ("Màu", ["Đen", "Bạc", "Đỏ", "Trắng"])],
        "specs": [("Hãng", None), ("Lắp đặt", ["Tự lắp", "Cần ra tiệm"]),
                  ("Tương thích", ["Đa số xe", "Theo dòng xe cụ thể"])],
        "price_band": (20_000, 90_000_000),
    },
    "Công cụ & Phần cứng": {
        "axes": [("Kích cỡ", ["Số 6", "Số 8", "Số 10", "Bộ 12 chi tiết", "Bộ 24 chi tiết"])],
        "specs": [("Chất liệu", ["Thép CR-V", "Thép carbon", "Nhựa", "Nhôm"]),
                  ("Nguồn điện", ["Pin 12V", "Pin 20V", "Điện 220V", "Không dùng điện"])],
        "price_band": (15_000, 25_000_000),
    },
    "Công nghiệp & Thương mại": {
        "axes": [("Quy cách", ["Lẻ 1", "Bộ 5", "Thùng 10", "Thùng 20", "Thùng 50",
                               "Thùng 100", "Theo mét", "Theo kg"]),
                 ("Kích cỡ", ["Size nhỏ", "Size vừa", "Size lớn", "Đại", "Theo yêu cầu"])],
        "specs": [("Chất liệu", ["Thép", "Inox 304", "Nhựa PVC", "Nhôm"]),
                  ("Tiêu chuẩn", ["TCVN", "ISO 9001", "Không công bố"])],
        "price_band": (30_000, 90_000_000),
    },
}

# Fallback base names, used for a leaf the crawl left thin. Deliberately short: this is the
# smoke-test path so the generator runs with no network at all, not a second catalogue.
# Real volume comes from vocab.json.
FALLBACK = {
    "Điện thoại & Máy tính bảng": ["Điện thoại Samsung Galaxy A55", "iPhone 13", "Máy tính bảng Xiaomi Pad 6", "Điện thoại Oppo Reno11", "Máy đọc sách Kindle Paperwhite"],
    "Máy tính & Thiết bị mạng": ["Laptop Dell Inspiron 15", "Router wifi TP-Link Archer C6", "Ổ cứng di động WD 1TB", "Bàn phím cơ Akko 3068", "Chuột không dây Logitech M331"],
    "Máy ảnh & Quay phim": ["Máy ảnh Canon EOS M50", "Ống kính Sony 50mm F1.8", "Camera IP wifi Imou A22", "Gimbal chống rung DJI OM5", "Action camera Insta360 Go 3"],
    "Âm thanh & Tai nghe": ["Tai nghe Bluetooth Soundpeats Air4", "Loa bluetooth JBL Go 3", "Tai nghe chụp tai Edifier W820NB", "Micro thu âm Fifine K669", "Soundbar Samsung HW-C400"],
    "Trò chơi điện tử & Gaming": ["Tay cầm Xbox Series X", "Bàn phím gaming Logitech G413", "Ghế gaming E-Dra Hunter", "Máy chơi game Nintendo Switch Lite", "Lót chuột gaming Steelseries QcK"],
    "Phần mềm & Hàng hóa số": ["Key Windows 11 Pro bản quyền", "Tài khoản Office 365 1 năm", "Voucher Grab 100k", "Key Steam game bản quyền", "Tài khoản Spotify Premium"],
    "Phụ kiện điện tử": ["Sạc nhanh Anker 20W", "Cáp sạc Type-C Ugreen 1m", "Ốp lưng silicon iPhone 14", "Pin dự phòng Xiaomi 10000mAh", "Hub USB-C 6 in 1"],
    "Thời trang & Quần áo": ["Áo thun nam cổ tròn cotton", "Áo sơ mi nữ tay dài", "Quần jean nam ống suông", "Đầm suông nữ dáng dài", "Áo khoác gió unisex"],
    "Giày dép": ["Giày sneaker nam đế cao su", "Giày cao gót nữ 7cm", "Dép quai ngang unisex", "Giày tây nam da bò", "Sandal nữ đế bằng"],
    "Túi xách & Vali": ["Balo laptop 15.6 inch chống nước", "Vali kéo nhựa 24 inch", "Túi đeo chéo nam canvas", "Ví da nam đứng", "Túi tote nữ vải bố"],
    "Đồng hồ": ["Đồng hồ nam Casio MTP-1374", "Đồng hồ nữ dây kim loại", "Đồng hồ điện tử Casio G-Shock", "Đồng hồ automatic Orient Bambino", "Dây da đồng hồ 20mm"],
    "Trang sức": ["Dây chuyền bạc 925 nữ", "Nhẫn cặp bạc đôi", "Bông tai ngọc trai", "Vòng tay charm bạc", "Lắc chân bạc nữ"],
    "Phụ kiện thời trang": ["Mắt kính râm unisex UV400", "Thắt lưng da nam khóa kim", "Mũ lưỡi trai unisex", "Khăn lụa vuông nữ", "Găng tay da cảm ứng"],
    "Sắc đẹp & Chăm sóc cá nhân": ["Sữa rửa mặt Cerave 236ml", "Kem chống nắng Anessa SPF50", "Serum vitamin C Klairs", "Nước hoa nam Bleu de Chanel", "Dầu gội Tsubaki 490ml"],
    "Sức khỏe & Thể chất": ["Viên uống Vitamin C 1000mg", "Máy massage cầm tay", "Whey protein 2lbs", "Máy đo huyết áp Omron", "Dầu cá Omega 3 Nature Made"],
    "Nội thất gia đình": ["Kệ sách gỗ 5 tầng", "Bàn làm việc gỗ MDF 1m2", "Ghế xoay văn phòng lưới", "Tủ quần áo nhựa 3 tầng", "Giá treo tường gỗ"],
    "Trang trí nhà cửa & Đèn chiếu sáng": ["Đèn LED bulb 9W", "Đèn ngủ để bàn cảm ứng", "Tranh canvas treo tường", "Đèn LED dây trang trí 5m", "Bình hoa gốm Nhật"],
    "Nhà bếp & Ăn uống": ["Nồi cơm điện Sharp 1.8L", "Chảo chống dính đáy từ 26cm", "Bộ dao inox 5 món", "Bình giữ nhiệt Lock&Lock 500ml", "Máy xay sinh tố Philips"],
    "Chăn ga gối đệm & Phòng tắm": ["Bộ ga gối cotton 1m6", "Gối cao su non chống ngáy", "Kệ nhà tắm inox 2 tầng", "Rèm phòng tắm chống nước", "Thảm chân chống trượt"],
    "Thiết bị gia dụng lớn": ["Tủ lạnh Aqua 180L", "Máy giặt cửa trước LG 8kg", "Máy lạnh Daikin 1HP inverter", "Tivi Xiaomi A2 43 inch", "Bình nóng lạnh Ariston 20L"],
    "Thiết bị gia dụng nhỏ": ["Máy hút bụi cầm tay", "Nồi chiên không dầu 5L", "Quạt điện Senko lửng", "Máy lọc không khí Xiaomi 4 Lite", "Ấm siêu tốc inox 1.8L"],
    "Sân vườn & Ngoại thất": ["Chậu nhựa trồng cây size lớn", "Vòi phun tưới cây xoay 360", "Bộ dụng cụ làm vườn 5 món", "Lưới che nắng vườn 2x5m", "Đèn năng lượng mặt trời sân vườn"],
    "Tạp hóa & Thực phẩm": ["Cà phê rang xay 500g", "Mì gạo lứt hộp 500g", "Hạt điều rang muối 500g", "Nước mắm Nam Ngư 500ml", "Sữa đặc Ông Thọ 380g"],
    "Mẹ & Bé": ["Tã dán Bobby size M", "Bình sữa Pigeon 240ml", "Sữa bột Nan Optipro số 3", "Xe đẩy em bé gấp gọn", "Khăn sữa cotton cho bé"],
    "Đồ dùng cho thú cưng": ["Hạt cho mèo Whiskas 1.2kg", "Chuồng mèo 2 tầng", "Cát vệ sinh cho mèo 5L", "Dây dắt chó có đai", "Bát ăn đôi cho thú cưng"],
    "Thể thao & Dã ngoại": ["Vợt cầu lông Yonex Nanoflare", "Thảm yoga TPE 6mm", "Bóng đá size 5 khâu tay", "Lều cắm trại 2 người", "Dây nhảy thể dục có đếm"],
    "Đồ chơi & Trò chơi": ["Bộ Lego xếp hình 500 mảnh", "Xe điều khiển từ xa 4WD", "Gấu bông teddy 50cm", "Bộ cờ vua gỗ nam châm", "Rubik 3x3 Moyu"],
    "Nhạc cụ": ["Đàn guitar acoustic Rosen", "Đàn ukulele soprano gỗ", "Kèn harmonica 10 lỗ", "Bộ dây đàn guitar Alice", "Giá để sách nhạc"],
    "Nghệ thuật & Thủ công": ["Bộ màu acrylic 24 màu", "Khung tranh canvas 30x40", "Bộ đất sét nặn Nhật", "Bút vẽ calligraphy", "Bộ tranh sơn theo số"],
    "Sách & Ấn phẩm": ["Sách Đắc Nhân Tâm bìa mềm", "Truyện Doraemon tập 45", "Sách Nhà Giả Kim", "Từ điển Anh Việt Oxford", "Sách Tuổi Trẻ Đáng Giá Bao Nhiêu"],
    "Văn phòng phẩm & Dụng cụ học tập": ["Bút bi Thiên Long TL-027", "Vở kẻ ngang Campus 200 trang", "Bút lông kim Faber-Castell", "Máy tính Casio FX-580VN X", "Hộp bút vải canvas"],
    "Ô tô & Xe máy": ["Mũ bảo hiểm 3/4 Royal M20", "Bọc vô lăng ô tô da PU", "Nhớt Motul 300V 1L", "Camera hành trình VietMap C61", "Áo mưa bộ Rando"],
    "Công cụ & Phần cứng": ["Máy khoan pin Bosch 12V", "Bộ tua vít 24 chi tiết", "Thước dây 5m Stanley", "Kìm bấm cos đa năng", "Máy mài góc 100mm"],
    "Công nghiệp & Thương mại": ["Băng keo trong 5cm cuộn lớn", "Dây điện Cadivi 2.5mm", "Găng tay bảo hộ phủ PU", "Ống nhựa PVC 32mm", "Thùng nhựa công nghiệp 60L"],
}

# Tags per leaf, kebab-case because "tag"."id" is the slug and the schema checks it.
#
# Kept small on purpose. A tag is not free: every distinct one gets a row in "tag" and a
# vector in "tag_embedding", which measures 15KB per row on this schema, so a tag vocabulary
# that grew with the catalogue would cost more than the listings do. Ten or so per leaf plus
# the brands the crawl found is a facet worth having; a tag per listing is a tag nobody filters by.
TAGS = {
    "Điện thoại & Máy tính bảng": ["chinh-hang", "may-cu-gia-re", "con-bao-hanh", "fullbox", "gaming-phone", "pin-khoe", "chup-anh-dep", "5g"],
    "Máy tính & Thiết bị mạng": ["laptop-van-phong", "laptop-gaming", "linh-kien-pc", "wifi-6", "o-cung-ssd", "man-hinh", "hang-thanh-ly", "cho-sinh-vien"],
    "Máy ảnh & Quay phim": ["mirrorless", "ong-kinh", "camera-an-ninh", "action-cam", "chong-rung", "cho-nguoi-moi", "quay-vlog", "phu-kien-may-anh"],
    "Âm thanh & Tai nghe": ["tai-nghe-khong-day", "chong-on", "loa-bluetooth", "am-thanh-vong", "micro-thu-am", "hifi", "cho-the-thao", "pin-lau"],
    "Trò chơi điện tử & Gaming": ["tay-cam", "ban-phim-co", "chuot-gaming", "ghe-gaming", "ps5", "nintendo-switch", "rgb", "esports"],
    "Phần mềm & Hàng hóa số": ["ban-quyen", "key-vinh-vien", "tai-khoan", "voucher", "giao-ngay", "hoc-tap", "van-phong", "giai-tri"],
    "Phụ kiện điện tử": ["sac-nhanh", "cap-type-c", "op-lung", "pin-du-phong", "hub-usb", "gia-do", "chong-nuoc", "phu-kien-re"],
    "Thời trang & Quần áo": ["hang-viet-nam", "oversize", "cotton", "di-lam", "di-choi", "unisex", "size-lon", "thanh-ly-tu"],
    "Giày dép": ["sneaker", "da-that", "di-lam", "the-thao", "size-nho", "size-lon", "cao-got", "sandal"],
    "Túi xách & Vali": ["balo-laptop", "chong-nuoc", "du-lich", "di-hoc", "da-pu", "vali-keo", "vi-da", "tui-deo-cheo"],
    "Đồng hồ": ["automatic", "quartz", "day-da", "day-kim-loai", "chong-nuoc", "co-dien", "the-thao", "hang-nhat-bai"],
    "Trang sức": ["bac-925", "vang-18k", "ngoc-trai", "da-cz", "qua-tang", "cap-doi", "handmade", "khong-den-mau"],
    "Phụ kiện thời trang": ["mat-kinh", "that-lung", "mu-non", "khan", "gang-tay", "da-that", "unisex", "qua-tang"],
    "Sắc đẹp & Chăm sóc cá nhân": ["han-quoc", "nhat-ban", "duong-am", "chong-nang", "tri-mun", "nuoc-hoa", "cham-soc-toc", "thien-nhien"],
    "Sức khỏe & Thể chất": ["thuc-pham-chuc-nang", "vitamin", "whey-protein", "may-massage", "do-huyet-ap", "ho-tro-xuong-khop", "tang-de-khang", "giam-can"],
    "Nội thất gia đình": ["go-tu-nhien", "lap-ghep", "tiet-kiem-dien-tich", "ban-lam-viec", "ke-sach", "ghe-xoay", "phong-ngu", "chung-cu"],
    "Trang trí nhà cửa & Đèn chiếu sáng": ["den-led", "den-ngu", "tranh-treo-tuong", "cay-gia", "phong-cach-bac-au", "tiet-kiem-dien", "cam-ung", "handmade"],
    "Nhà bếp & Ăn uống": ["inox-304", "chong-dinh", "dung-bep-tu", "chiu-nhiet", "binh-giu-nhiet", "bo-dao", "may-xay", "an-toan-thuc-pham"],
    "Chăn ga gối đệm & Phòng tắm": ["cotton", "mat-lanh", "goi-cao-su", "chong-truot", "chong-nuoc", "phong-tam", "1m6", "1m8"],
    "Thiết bị gia dụng lớn": ["inverter", "tiet-kiem-dien", "tu-lanh", "may-giat", "may-lanh", "tivi", "con-bao-hanh", "hang-trung-bay"],
    "Thiết bị gia dụng nhỏ": ["noi-chien-khong-dau", "may-hut-bui", "may-loc-khong-khi", "quat-dien", "am-sieu-toc", "de-ve-sinh", "tiet-kiem-dien", "cho-phong-tro"],
    "Sân vườn & Ngoại thất": ["trong-cay", "chau-nhua", "tuoi-cay", "ngoai-troi", "chiu-nang-mua", "dung-cu-lam-vuon", "nang-luong-mat-troi", "san-thuong"],
    "Tạp hóa & Thực phẩm": ["do-an-van", "gia-vi", "do-uong", "healthy", "an-lien", "date-dai", "combo-tiet-kiem", "dac-san"],
    "Mẹ & Bé": ["ta-bim", "sua-cong-thuc", "an-dam", "do-so-sinh", "an-toan-cho-be", "co-co-cq", "xe-day", "binh-sua"],
    "Đồ dùng cho thú cưng": ["cho-meo", "cho-cho", "hat-cho-meo", "cat-ve-sinh", "chuong-nuoi", "do-choi-thu-cung", "vong-co", "de-ve-sinh"],
    "Thể thao & Dã ngoại": ["cau-long", "gym", "chay-bo", "cam-trai", "yoga", "bong-da", "chong-nuoc", "cho-nguoi-moi"],
    "Đồ chơi & Trò chơi": ["xep-hinh", "giao-duc", "gau-bong", "dieu-khien-tu-xa", "board-game", "rubik", "cho-be-3-tuoi", "qua-tang"],
    "Nhạc cụ": ["guitar", "ukulele", "piano", "trong", "ken", "cho-nguoi-moi", "phu-kien-nhac-cu", "go-tu-nhien"],
    "Nghệ thuật & Thủ công": ["handmade", "mau-ve", "canvas", "dat-set", "tranh-son-theo-so", "diy", "qua-tang", "trang-tri"],
    "Sách & Ấn phẩm": ["sach-tieng-viet", "van-hoc", "ky-nang-song", "kinh-doanh", "truyen-tranh", "sach-thieu-nhi", "sach-cu", "bia-cung"],
    "Văn phòng phẩm & Dụng cụ học tập": ["but-bi", "vo-ghi", "may-tinh-cam-tay", "hop-but", "giay-a4", "cho-hoc-sinh", "van-phong", "combo-tiet-kiem"],
    "Ô tô & Xe máy": ["mu-bao-hiem", "phu-tung-xe-may", "phu-kien-o-to", "dau-nhot", "camera-hanh-trinh", "ao-mua", "cham-soc-xe", "de-lap-dat"],
    "Công cụ & Phần cứng": ["may-khoan", "bo-tua-vit", "dung-cu-cam-tay", "thep-cr-v", "dung-pin", "sua-chua-nha", "do-nghe", "chinh-hang"],
    "Công nghiệp & Thương mại": ["ban-si", "thung-lon", "bao-ho-lao-dong", "vat-tu-dien", "ong-nhua", "inox-304", "tieu-chuan-tcvn", "so-luong-lon"],
}
