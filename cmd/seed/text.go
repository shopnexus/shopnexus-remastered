package main

import (
	"math/rand/v2"
	"strings"

	catalogdomain "shopnexus/internal/module/catalog/domain"
)

// slugify is catalog's own rule, not a copy of it: `listing.slug` and `tag.id` are CHECKed
// against `^[a-z0-9]+(-[a-z0-9]+)*$`, and the module that owns those columns owns what satisfies
// them. Vietnamese folds to ASCII through it — "Áo Thun Nữ" becomes "ao-thun-nu" — which is
// what makes a Vietnamese title a usable URL.
func slugify(s string) string { return catalogdomain.SlugifyName(s) }

// clipSlug cuts a slug to n bytes on a dash boundary, so the result is still a slug and not a
// word sawn in half. A cut that already lands on one keeps the last word.
func clipSlug(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut, rest := s[:n], s[n]
	if i := strings.LastIndexByte(cut, '-'); i > 0 && rest != '-' {
		cut = cut[:i]
	}
	return strings.Trim(cut, "-")
}

// The review copy. Written as a Vietnamese buyer of second-hand goods actually writes: about
// the state of the thing, the packing and whether the seller said the truth — not about a brand.
// Short sentences, no marketing voice, the occasional typo-free but plainly spoken complaint.
var (
	reviewsGood = []string{
		"Hàng đúng như mô tả, người bán nói chuyện dễ chịu. Mình nhận được sớm hơn hẹn một ngày.",
		"Đóng gói kỹ, bọc hai lớp xốp. Món đồ còn mới hơn mình tưởng, dùng ngon lành.",
		"Bạn bán mô tả rất thật, chỗ nào xước cũng nói trước. Mình đánh giá cao chuyện đó.",
		"Giao nhanh, kiểm tra tại chỗ luôn. Mọi thứ hoạt động bình thường, cảm ơn bạn nhé.",
		"Mình qua tận nơi xem rồi mới lấy, đúng như trong ảnh. Người bán vui vẻ, cho thử thoải mái.",
		"Dùng được một tuần rồi, chưa thấy vấn đề gì. Giá này là hợp lý so với ngoài tiệm.",
		"Món này mình tìm mãi mới có, may là gặp bạn. Hàng chuẩn, không phải lo.",
		"Bạn bán còn nhắn hỏi lại xem mình dùng có ổn không. Rất có tâm.",
	}
	reviewsFine = []string{
		"Hàng ổn so với giá. Có vài vết xước nhỏ nhưng bạn bán đã nói trước nên mình không bất ngờ.",
		"Dùng được, không có gì để chê nhiều. Giao hơi chậm một chút.",
		"Đúng mô tả. Đóng gói hơi sơ sài nhưng may là hàng không sao.",
		"Tạm ổn. Món đồ cũ hơn mình hình dung qua ảnh, nhưng vẫn chạy tốt.",
		"Được việc. Nếu bạn chụp thêm ảnh cận cảnh nữa thì người mua đỡ phải hỏi.",
		"Hàng nhận đúng hẹn, chất lượng vừa phải, đúng tầm tiền.",
	}
	reviewsPoor = []string{
		"Món đồ có vết xước sâu ở mặt sau mà trong ảnh không thấy. Bạn bán có xin lỗi và bớt lại một ít.",
		"Giao chậm gần một tuần so với hẹn. Hàng thì vẫn dùng được.",
		"Hộp bị móp lúc nhận, chắc do vận chuyển. Bên trong may vẫn nguyên.",
		"Không đúng như mình hình dung, phụ kiện kèm theo thiếu một món so với mô tả.",
		"Mình phải nhắn mấy lần bạn mới trả lời. Hàng thì tạm được.",
	}
	reviewsBad = []string{
		"Máy không lên nguồn, mình đã báo và đang chờ bạn bán xử lý.",
		"Khác hẳn mô tả. Mình đã yêu cầu hoàn tiền.",
		"Nhận hàng thì thiếu mất phụ kiện chính, nhắn tin thì bạn bán không trả lời.",
	}
	sellerReplies = []string{
		"Cảm ơn bạn đã mua hàng và dành thời gian đánh giá. Có gì cần hỗ trợ bạn cứ nhắn mình nhé.",
		"Cảm ơn bạn nhiều. Mình luôn cố gắng mô tả đúng nhất có thể để hai bên đỡ mất công.",
		"Mình vui vì bạn hài lòng. Lần sau ghé mình bớt thêm cho bạn.",
		"Cảm ơn bạn đã thông cảm phần đóng gói, lần sau mình sẽ bọc kỹ hơn.",
		"Mình xin lỗi vì phần giao hàng chậm, bên vận chuyển đợt này quá tải. Cảm ơn bạn đã kiên nhẫn.",
	}
	feedbackBuyerSide = []string{
		"Người bán uy tín, giao đúng hẹn.",
		"Trao đổi nhanh, dễ chịu, đóng gói cẩn thận.",
		"Mô tả đúng tình trạng món hàng, không giấu lỗi.",
		"Giao dịch suôn sẻ, sẽ ủng hộ lần sau.",
		"Bạn bán nhiệt tình, trả lời tin nhắn nhanh.",
		"Giao hơi chậm nhưng có báo trước.",
	}
	feedbackSellerSide = []string{
		"Người mua thanh toán nhanh, nhận hàng đúng hẹn.",
		"Bạn mua dễ tính, trao đổi rõ ràng.",
		"Giao dịch vui vẻ, cảm ơn bạn.",
		"Bạn nhận hàng và xác nhận ngay, rất thuận tiện.",
	}
)

func reviewBody(rng *rand.Rand, rating int) string {
	var pool []string
	switch {
	case rating >= 5:
		pool = reviewsGood
	case rating == 4:
		pool = reviewsFine
	case rating == 3:
		pool = reviewsPoor
	default:
		pool = reviewsBad
	}
	return pool[rng.IntN(len(pool))]
}

func sellerReply(rng *rand.Rand) string { return sellerReplies[rng.IntN(len(sellerReplies))] }
func feedbackFromBuyer(rng *rand.Rand) string {
	return feedbackBuyerSide[rng.IntN(len(feedbackBuyerSide))]
}
func feedbackFromSeller(rng *rand.Rand) string {
	return feedbackSellerSide[rng.IntN(len(feedbackSellerSide))]
}
