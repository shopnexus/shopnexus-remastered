# Demo smart search — kịch bản ngắn

`dev/demo-search.sh` chạy đúng bốn câu dưới, theo thứ tự này. Mỗi câu chứng minh **một** thứ; đừng
thêm câu thứ năm ứng khẩu, xem mục "Đừng demo" ở cuối.

Điều kiện: gateway đang chạy, `internal/config/config.dev.yml` có `provider: "litellm"` và `api_key`
thật. Mỗi câu tốn một lần gọi model (~1.500 token vào, ~120 token ra, khoảng 15 đồng) và 1–3 giây.

## Bốn nhịp

**1. `ao thu unilo` — gõ sai, mất dấu.**
Hiểu ra "áo thun thương hiệu Uniqlo". Chỉ vào dòng `tìm:` — `áo thun uniqlo · áo thun · ao thu unilo`.
Câu chữ người dùng gõ *vẫn ở đó*, đứng cuối: nếu model hiểu sai thì tìm kiếm chỉ hẹp lại chứ không bị
thay thế. Không có bảng chính tả nào, không có từ điển đồng nghĩa nào.

**2. `dt cu duoi 5tr` — viết tắt, và ba ràng buộc trong một câu.**
`dt` → điện thoại, `cu` → tình trạng đã dùng, `5tr` → 5.000.000. Một câu bốn từ thành: một probe ngữ
nghĩa, một ràng buộc category, một ngưỡng giá, một tình trạng.

**3. `qua tang sinh nhat re` — mơ hồ, không có tên sản phẩm nào.**
Không từ khoá nào để khớp. Kết quả ra tag sinh nhật, đèn ngủ DIY, hoa sáp, holder card — thứ mà tìm
kiếm theo từ khoá không thể trả về, vì không listing nào chứa chữ "quà tặng sinh nhật".

**4. `op lung iphone` — biết ai là ai.**
Phụ kiện ở đây là *thứ được tìm*. So với nhịp 2, nơi ốp lưng và sạc bị **hạ** ưu tiên vì người ta hỏi
mua điện thoại. Cùng một cặp từ, hai hướng ngược nhau, do câu hỏi quyết định.

## Nhịp thứ năm, nếu bị hỏi sâu: "làm sao biết nó không đoán bừa?"

```
docker logs server-gateway-dev-1 | grep 'search terms' | tail -1
```

Mỗi lượt search ghi hai dòng: model *xin* gì (`asked`) và cái gì thật sự *chạy* (`predicates`).
Nếu model bịa một category không có trong cây, nó nằm ở `asked` và không nằm ở `predicates` — bị bỏ
trong im lặng, và người vận hành thấy được. Không có con số nào trong đó do model đặt: model nói
*thuộc tính nào quan trọng và theo thứ tự nào*, còn trọng số là hằng số trong code.

## Ba câu sẽ bị hỏi, và số liệu để trả lời

**"Model down thì sao?"** Câu chữ người dùng gõ luôn được thêm vào làm một probe, nên answer rỗng hay
lỗi đều rơi về đúng tìm kiếm nền hybrid — không phải nhánh code thứ hai, mà là cùng một đường với danh
sách signal rỗng.

**"Sao không dùng filter cứng?"** Một filter từ suy đoán sai cho trang rỗng, mà trang rỗng là kết cục
tệ nhất của một sàn. Giá vì thế là boost: đo được, ngưỡng giá ở trọng số 0.6 là mức thấp nhất giữ được
khoảng, và một con số đọc sai thì **đẩy không gì cả** thay vì phá trang.

**"Chi phí?"** ~1.500 token vào + 120 ra mỗi lượt ≈ 15 đồng, hay 15.000 đồng cho 1.000 lượt. Chưa có
cache — thêm cache theo query đã normalize sẽ cắt gần hết, và chỗ để bọc đã có sẵn (`understand()`).

## Đừng demo

- **Khoảng giá** (`ao nam 300k-500k`). Kho chỉ có ít áo nam trong khoảng, và khi model tách câu thành
  ba probe thì một áo thun rẻ nằm trong cả ba leg và cộng điểm từ cả ba. Đo được: 2/6 trong khoảng.
- **`máy ảnh Canon`, `nồi cơm điện`.** Catalogue có **0** listing loại này — trang sẽ trông như hỏng.
- **`ip moi`.** `ip` lưỡng nghĩa: có "Camera IP" trong kho, và nó khớp thật.
- **Câu vừa đọc lại lần hai.** Model không tất định tuyệt đối ở temperature 0; tập kết quả có thể đổi
  thứ tự giữa hai lần. Nội dung đúng chủ đề cả hai lần, nhưng đừng hứa trước là "y hệt".
