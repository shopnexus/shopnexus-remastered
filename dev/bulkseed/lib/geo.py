"""A point per province, so generated listings have a location.

`listing.location` is a geography(Point,4326) and the browse feed's radius filter reads it
through `listing_location_gist`. The filter treats a NULL point as outside any radius — a
listing cannot claim a distance it has no way to know — so a hundred thousand listings with
no point is a hundred thousand listings "near me" never returns.

areas/vn.json carries province and ward codes and names but no coordinates, so these are
here. One point per province, near its administrative centre, and the generator jitters
around it: ward-level accuracy would need a ward-level dataset, and for a radius filter
being in the right province within a few tens of kilometres is what matters.

Codes are the GSO province codes, the same ones account.contact uses.
"""

PROVINCE_POINTS = {
    "01": (21.0285, 105.8542),   # Hà Nội
    "02": (22.8233, 104.9784),   # Hà Giang
    "04": (22.6657, 106.2570),   # Cao Bằng
    "06": (22.1470, 105.8348),   # Bắc Kạn
    "08": (21.8233, 105.2140),   # Tuyên Quang
    "10": (22.4856, 103.9707),   # Lào Cai
    "11": (21.3860, 103.0166),   # Điện Biên
    "12": (22.3964, 103.4700),   # Lai Châu
    "14": (21.3273, 103.9141),   # Sơn La
    "15": (21.7168, 104.8986),   # Yên Bái
    "17": (20.8133, 105.3383),   # Hoà Bình
    "19": (21.5942, 105.8482),   # Thái Nguyên
    "20": (21.8534, 106.7615),   # Lạng Sơn
    "22": (21.0064, 107.2925),   # Quảng Ninh
    "24": (21.2731, 106.1946),   # Bắc Giang
    "25": (21.4000, 105.2270),   # Phú Thọ
    "26": (21.3089, 105.6049),   # Vĩnh Phúc
    "27": (21.1861, 106.0763),   # Bắc Ninh
    "30": (20.9399, 106.3330),   # Hải Dương
    "31": (20.8449, 106.6881),   # Hải Phòng
    "33": (20.6464, 106.0511),   # Hưng Yên
    "34": (20.4463, 106.3366),   # Thái Bình
    "35": (20.5835, 105.9230),   # Hà Nam
    "36": (20.4388, 106.1621),   # Nam Định
    "37": (20.2506, 105.9745),   # Ninh Bình
    "38": (19.8067, 105.7772),   # Thanh Hóa
    "40": (19.2342, 104.9200),   # Nghệ An
    "42": (18.3428, 105.9057),   # Hà Tĩnh
    "44": (17.4685, 106.6223),   # Quảng Bình
    "45": (16.7487, 107.1900),   # Quảng Trị
    "46": (16.4674, 107.5905),   # Thừa Thiên Huế
    "48": (16.0471, 108.2068),   # Đà Nẵng
    "49": (15.5731, 108.4740),   # Quảng Nam
    "51": (15.1214, 108.8044),   # Quảng Ngãi
    "52": (13.7820, 109.2197),   # Bình Định
    "54": (13.0882, 109.0929),   # Phú Yên
    "56": (12.2388, 109.1967),   # Khánh Hòa
    "58": (11.5645, 108.9899),   # Ninh Thuận
    "60": (11.0904, 108.0721),   # Bình Thuận
    "62": (14.3498, 108.0005),   # Kon Tum
    "64": (13.9833, 108.0000),   # Gia Lai
    "66": (12.7100, 108.2378),   # Đắk Lắk
    "67": (12.0045, 107.6900),   # Đắk Nông
    "68": (11.9404, 108.4583),   # Lâm Đồng
    "70": (11.7512, 106.7235),   # Bình Phước
    "72": (11.3110, 106.0983),   # Tây Ninh
    "74": (11.3254, 106.4770),   # Bình Dương
    "75": (10.9639, 106.8534),   # Đồng Nai
    "77": (10.5417, 107.2431),   # Bà Rịa - Vũng Tàu
    "79": (10.8231, 106.6297),   # Thành phố Hồ Chí Minh
    "80": (10.5355, 106.4130),   # Long An
    "82": (10.4493, 106.3421),   # Tiền Giang
    "83": (10.2415, 106.3752),   # Bến Tre
    "84": (9.9347, 106.3453),    # Trà Vinh
    "86": (10.2538, 105.9722),   # Vĩnh Long
    "87": (10.4938, 105.6882),   # Đồng Tháp
    "89": (10.5216, 105.1259),   # An Giang
    "91": (10.0125, 105.0808),   # Kiên Giang
    "92": (10.0452, 105.7469),   # Cần Thơ
    "93": (9.7845, 105.4701),    # Hậu Giang
    "94": (9.6025, 105.9739),    # Sóc Trăng
    "95": (9.2941, 105.7216),    # Bạc Liêu
    "96": (9.1769, 105.1500),    # Cà Mau
}

# Roughly how far the jitter spreads, in degrees. 0.25° is about 27km north-south, which is
# the scale of a province and small enough that a 10km radius search still separates them.
JITTER = 0.25
