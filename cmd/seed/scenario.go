package main

import (
	"fmt"
	"math/rand/v2"
	"time"
)

// The three accounts the graduation report signs in as. Every screen the report has to
// photograph is hung off one of these, which is the whole reason this file exists: a random
// spread of orders across a random cast leaves the account you actually log in as empty.
const (
	buyerKey = "khoa_buyer"
	shopKey  = "bob_store"
	adminKey = "admin"
)

// addScenarios lays down the history: the ordinary trade that gives every seller a reputation
// and every listing its reviews, and then the specific situations the report needs a photograph
// of, pinned to the three accounts above.
//
// The specific ones are written last and by hand. They are not "one of the random orders that
// happened to come out in-transit": a demo whose most important screen depends on a dice roll
// is a demo that is empty on the run that matters.
func (p *plan) addScenarios(rng *rand.Rand) error {
	if err := p.addModerationQueue(); err != nil {
		return err
	}
	p.addTradingHistory(rng)
	if err := p.addBuyerJourney(rng); err != nil {
		return err
	}
	if err := p.addNegotiations(); err != nil {
		return err
	}
	if err := p.addSupportQueue(); err != nil {
		return err
	}
	return nil
}

// addModerationQueue leaves the admin area something to moderate. Both halves of the queue are
// represented: a listing waiting for its first publication, and one staff took down.
func (p *plan) addModerationQueue() error {
	pending := []string{"Ván trượt Penny", "Bếp gas mini dã ngoại"}
	for _, fragment := range pending {
		i, err := p.findListing(fragment)
		if err != nil {
			return err
		}
		p.listings[i].status = "pending"
	}
	i, err := p.findListing("Sạc dự phòng Anker")
	if err != nil {
		return err
	}
	p.listings[i].status = "hidden"
	p.listings[i].takedown = "Ảnh sản phẩm không phải ảnh thật của món hàng. Bạn chụp lại ảnh thật rồi đăng lại giúp mình nhé."
	return nil
}

// addTradingHistory is the background: every live listing gets the purchases its reviews are
// made of. It is what stops a seller's reputation reading 0.0 ★ and a product page reading
// "0 đánh giá" — neither of which is a rendering bug, they are what the database actually said.
//
// A negotiable listing is bought through an accepted offer rather than a checkout, so its
// orders carry an offer and no draft. Doing that here rather than only in the hand-written
// scenarios is the point: negotiable listings need reviews too.
func (p *plan) addTradingHistory(rng *rand.Rand) {
	buyers := make([]string, 0, len(seedAccounts))
	for _, a := range seedAccounts {
		if a.Key == adminKey {
			continue // staff do not shop here
		}
		buyers = append(buyers, a.Key)
	}

	// Which sellers have earned a rating yet. A seller with none reads 0.0 ★ everywhere their
	// name appears — on the shop header, on every card, in the seller block of every product
	// page — so the first live listing of a seller who has not sold anything is given a sale
	// whatever the dice said.
	rated := map[string]bool{}

	for i := range p.listings {
		l := &p.listings[i]
		if l.status != "active" {
			continue
		}
		// A featured listing is the one a category page opens on, so it carries the fuller
		// history. The rest get between none and three sales — a marketplace where every
		// single listing has sold is not one either.
		count := rng.IntN(4)
		if l.featuredInDataset() {
			count = 3 + rng.IntN(2)
		}
		if !rated[l.seller] {
			count = max(count, 1)
		}
		if count == 0 {
			continue
		}
		rated[l.seller] = true
		order := rng.Perm(len(buyers))
		used := 0
		for _, at := range order {
			if used == count {
				break
			}
			buyer := buyers[at]
			if buyer == l.seller {
				continue // nobody buys from themselves
			}
			used++
			v := rng.IntN(len(l.variants))
			// Old enough that receipt, escrow window and review all fit before now.
			placedAt := between(rng, l.createdAt, p.now.Add(-12*24*time.Hour))
			rating := ratingFor(rng)
			review := &reviewPlan{
				rating:     rating,
				body:       reviewBody(rng, rating),
				helpful:    rng.IntN(9),
				notHelpful: rng.IntN(2),
				at:         placedAt.Add(time.Duration(6+rng.IntN(6)) * 24 * time.Hour),
			}
			if rating >= 4 && rng.IntN(3) == 0 {
				review.reply = sellerReply(rng)
			}
			o := orderPlan{
				key:       fmt.Sprintf("hist-%d-%d", i, used),
				buyer:     buyer,
				seller:    l.seller,
				listing:   i,
				variant:   v,
				quantity:  1,
				state:     stateCompleted,
				createdAt: placedAt,
				review:    review,
			}
			if l.priceMode == "negotiable" {
				// A negotiable listing has no draft: the accepted offer is what froze its
				// terms, so the order is born of the offer instead.
				o.offerKey = o.key
				p.offers = append(p.offers, offerPlan{
					key:       o.key,
					listing:   i,
					variant:   v,
					buyer:     buyer,
					seller:    l.seller,
					author:    buyer,
					status:    "checked-out",
					quantity:  1,
					total:     haggled(rng, l.variants[v].price),
					reason:    "Mình lấy luôn, bạn để giá này nhé.",
					createdAt: placedAt.Add(-time.Duration(2+rng.IntN(20)) * time.Hour),
					expiresAt: placedAt.Add(30 * time.Minute),
				})
			}
			p.orders = append(p.orders, o)
		}
	}
}

// addBuyerJourney is the buyer account's own order list, one order per state the report has to
// photograph. Every one of them is against the shop account, so the same pair of logins shows
// both sides of each screen.
func (p *plan) addBuyerJourney(rng *rand.Rand) error {
	pick := func(fragment string) (int, error) { return p.findListing(fragment) }

	// 1. Waiting on the seller to accept. Kept young on purpose: the sweep that chases an
	//    unanswered seller fires at 48h, and an order it has already escalated is a different
	//    screen from one still within the window.
	iPhone, err := pick("iPhone 12 64GB")
	if err != nil {
		return err
	}
	p.orders = append(p.orders, orderPlan{
		key: "journey-awaiting", buyer: buyerKey, seller: shopKey,
		listing: iPhone, variant: 0, quantity: 1,
		state:     stateAwaitingConfirmation,
		createdAt: p.now.Add(-5 * time.Hour),
		note:      "Bạn ơi mình cần gấp, nếu được thì gửi luôn trong hôm nay giúp mình nhé.",
		offerKey:  "offer-iphone-checkout",
	})
	p.offers = append(p.offers, offerPlan{
		key: "offer-iphone-checkout", listing: iPhone, variant: 0,
		buyer: buyerKey, seller: shopKey, author: buyerKey,
		status: "checked-out", quantity: 1, total: 5950000,
		reason:    "Mình lấy ngay, bạn bớt chút nhé.",
		createdAt: p.now.Add(-9 * time.Hour),
		expiresAt: p.now.Add(-4 * time.Hour),
	})

	// 2. Accepted, parcel not yet collected.
	keyboard, err := pick("Bàn phím cơ Keychron")
	if err != nil {
		return err
	}
	p.orders = append(p.orders, orderPlan{
		key: "journey-preparing", buyer: buyerKey, seller: shopKey,
		listing: keyboard, variant: 0, quantity: 1,
		state:     statePreparing,
		createdAt: p.now.Add(-30 * time.Hour),
		note:      "Cho mình xin thêm bộ keycap Windows với nhé.",
	})

	// 3. On the road. The report needs this one and the current data has none.
	monitor, err := pick("Màn hình Dell U2419H")
	if err != nil {
		return err
	}
	p.orders = append(p.orders, orderPlan{
		key: "journey-in-transit", buyer: buyerKey, seller: shopKey,
		listing: monitor, variant: 0, quantity: 1,
		state:     stateInTransit,
		createdAt: p.now.Add(-3 * 24 * time.Hour),
		note:      "Nhờ bạn chèn xốp kỹ giúp mình, hàng dễ vỡ.",
	})

	// 4. Delivered, waiting on the buyer to confirm receipt — the screen with the
	//    "đã nhận hàng" button on it.
	dell, err := pick("Dell Latitude 5420")
	if err != nil {
		return err
	}
	p.orders = append(p.orders, orderPlan{
		key: "journey-delivered", buyer: buyerKey, seller: shopKey,
		listing: dell, variant: 0, quantity: 1,
		state:     stateDelivered,
		createdAt: p.now.Add(-6 * 24 * time.Hour),
	})

	// 5. Finished, reviewed, seller answered. This is the one the product page quotes.
	macbook, err := pick("MacBook Air M1")
	if err != nil {
		return err
	}
	p.orders = append(p.orders, orderPlan{
		key: "journey-completed", buyer: buyerKey, seller: shopKey,
		listing: macbook, variant: 0, quantity: 1,
		state:     stateCompleted,
		createdAt: p.now.Add(-38 * 24 * time.Hour),
		offerKey:  "offer-macbook-checkout",
		review: &reviewPlan{
			rating: 5,
			body: "Máy đúng như mô tả, số lần sạc khớp với ảnh chụp màn hình bạn gửi. " +
				"Vỏ có vết chạm nhỏ ở cạnh như bạn đã nói trước, mình thấy không đáng kể. " +
				"Đóng gói ba lớp xốp, nhận về cắm sạc là chạy luôn. Cảm ơn bạn.",
			reply:      "Cảm ơn bạn đã tin tưởng. Có gì cứ nhắn mình nhé, mình vẫn hỗ trợ cài đặt giúp bạn.",
			helpful:    7,
			notHelpful: 0,
			at:         p.now.Add(-30 * 24 * time.Hour),
		},
	})
	p.offers = append(p.offers, offerPlan{
		key: "offer-macbook-checkout", listing: macbook, variant: 0,
		buyer: buyerKey, seller: shopKey, author: shopKey,
		status: "checked-out", quantity: 1, total: 14000000,
		reason:    "Mình để bạn giá này, tặng kèm túi chống sốc luôn.",
		createdAt: p.now.Add(-39 * 24 * time.Hour),
		expiresAt: p.now.Add(-38*24*time.Hour - 30*time.Minute),
	})

	// 6. A refund the seller still has time to answer. The one state the current data has
	//    none of, and the one that shows the 48h clock running.
	ipad, err := pick("iPad Gen 9")
	if err != nil {
		return err
	}
	p.orders = append(p.orders, orderPlan{
		key: "journey-refund-open", buyer: buyerKey, seller: shopKey,
		listing: ipad, variant: 0, quantity: 1,
		state:     stateRefundRequested,
		createdAt: p.now.Add(-11 * 24 * time.Hour),
		refund: &refundPlan{
			reason: "Máy nhận về bị sọc một vệt dọc bên trái màn hình, lúc nhận hàng mình đã quay video mở hộp. " +
				"Trong tin đăng bạn ghi màn không lỗi nên mình xin hoàn lại. Mình đã đính kèm ảnh chụp vệt sọc.",
			status:    "awaiting-seller-review",
			createdAt: p.now.Add(-9 * time.Hour),
		},
	})

	// 7. A refund that became a dispute and is sitting in the moderator queue.
	samsung, err := pick("Samsung Galaxy S21")
	if err != nil {
		return err
	}
	p.orders = append(p.orders, orderPlan{
		key: "journey-refund-disputed", buyer: buyerKey, seller: shopKey,
		listing: samsung, variant: 1, quantity: 1,
		state:     stateRefundDisputed,
		createdAt: p.now.Add(-24 * 24 * time.Hour),
		refund: &refundPlan{
			reason: "Máy bị ám màn ở góc trên bên phải, nhìn nền trắng là thấy rõ. " +
				"Bạn bán ghi màn không ám nên mình xin hoàn tiền, mình gửi kèm ảnh chụp nền trắng.",
			status:    "disputed",
			escalated: true,
			createdAt: p.now.Add(-6 * 24 * time.Hour),
		},
	})

	// 8. A refund that was granted, so the wallet ledger has a refund pair in it.
	sigma, err := pick("Ống kính Sigma 30mm")
	if err != nil {
		return err
	}
	p.orders = append(p.orders, orderPlan{
		key: "journey-refund-accepted", buyer: buyerKey, seller: "huyen_camera",
		listing: sigma, variant: 0, quantity: 1,
		state:     stateRefundAccepted,
		createdAt: p.now.Add(-52 * 24 * time.Hour),
		refund: &refundPlan{
			reason:    "Ống bị mốc nhẹ ở thấu kính sau, soi đèn mới thấy. Mình xin trả lại.",
			status:    "accepted",
			createdAt: p.now.Add(-45 * 24 * time.Hour),
		},
	})

	// 9. A seller who said no. Its reason is what the order history shows instead of a
	//    bare "đã huỷ".
	anker, err := pick("Sạc dự phòng Anker")
	if err != nil {
		return err
	}
	p.orders = append(p.orders, orderPlan{
		key: "journey-declined", buyer: buyerKey, seller: shopKey,
		listing: anker, variant: 0, quantity: 1,
		state:         stateDeclined,
		createdAt:     p.now.Add(-16 * 24 * time.Hour),
		declineReason: "Mình xin lỗi, món này vừa có người qua lấy trực tiếp sáng nay nên hết hàng rồi.",
	})

	// And a handful of finished purchases so the buyer's own history is not three rows long.
	for n, spec := range []struct {
		fragment string
		seller   string
		days     int
		rating   int
	}{
		{"Lều cắm trại Naturehike", "tuan_sport", 64, 5},
		{"Nhà Giả Kim", "sach_cu_hn", 88, 4},
		{"Nồi chiên không dầu Philips", "linh_home", 103, 5},
		{"Đèn bàn Xiaomi", "nga_vo", 120, 4},
	} {
		i, err := pick(spec.fragment)
		if err != nil {
			return err
		}
		p.orders = append(p.orders, orderPlan{
			key:       fmt.Sprintf("journey-past-%d", n),
			buyer:     buyerKey,
			seller:    spec.seller,
			listing:   i,
			variant:   0,
			quantity:  1,
			state:     stateCompleted,
			createdAt: p.now.Add(-time.Duration(spec.days) * 24 * time.Hour),
			review: &reviewPlan{
				rating:     spec.rating,
				body:       reviewBody(rng, spec.rating),
				helpful:    rng.IntN(6),
				notHelpful: 0,
				at:         p.now.Add(-time.Duration(spec.days-7) * 24 * time.Hour),
			},
		})
	}
	return nil
}

// addNegotiations is the chat, and the price cards in it.
//
// The direction of a live offer is the whole point of the first one: the storefront only shows
// "đồng ý" and "trả giá" to the side that did *not* make the move, so an offer the buyer sent
// gives the buyer nothing but a "rút lại" button. To photograph a buyer accepting a price,
// the seller has to be the one who proposed it.
func (p *plan) addNegotiations() error {
	ipad, err := p.findListing("iPad Gen 9")
	if err != nil {
		return err
	}
	guitar, err := p.findListing("Đàn guitar acoustic Yamaha")
	if err != nil {
		return err
	}
	fuji, err := p.findListing("Fujifilm X-T20")
	if err != nil {
		return err
	}
	bike, err := p.findListing("Xe đạp thể thao Giant")
	if err != nil {
		return err
	}

	// A live offer *from the seller*, waiting on the buyer. Its expiry is in the future,
	// which is what makes the card live rather than a red "đã quá hạn".
	p.offers = append(p.offers, offerPlan{
		key: "offer-live-from-seller", listing: ipad, variant: 0,
		buyer: buyerKey, seller: shopKey, author: shopKey,
		status: "active", quantity: 1, total: 4550000,
		reason:    "Mình bớt cho bạn 250k, bao gồm cả bao da và bút. Bạn thấy được thì bấm đồng ý nhé.",
		createdAt: p.now.Add(-3 * time.Hour),
		expiresAt: p.now.Add(9 * time.Hour),
	})
	// A live offer the other way round, so the seller's own inbox has something waiting on
	// them too.
	p.offers = append(p.offers, offerPlan{
		key: "offer-live-from-buyer", listing: bike, variant: 0,
		buyer: buyerKey, seller: "tuan_sport", author: buyerKey,
		status: "active", quantity: 1, total: 4900000,
		reason:    "Mình trả 4tr9, mình qua lấy tận nơi luôn không cần ship.",
		createdAt: p.now.Add(-20 * time.Hour),
		expiresAt: p.now.Add(4 * time.Hour),
	})
	// One that ran out. The expiry job writes 'cancelled' rather than an 'expired' status —
	// the enum has no such value — so that is what an expired negotiation looks like.
	p.offers = append(p.offers, offerPlan{
		key: "offer-expired", listing: guitar, variant: 0,
		buyer: buyerKey, seller: "sach_cu_hn", author: "sach_cu_hn",
		status: "cancelled", quantity: 1, total: 1350000,
		reason:    "Mình để 1tr35 là hết mức rồi bạn nhé.",
		createdAt: p.now.Add(-4 * 24 * time.Hour),
		expiresAt: p.now.Add(-3*24*time.Hour - 12*time.Hour),
	})

	macbook, err := p.findListing("MacBook Air M1")
	if err != nil {
		return err
	}
	monitor, err := p.findListing("Màn hình Dell U2419H")
	if err != nil {
		return err
	}

	// The buyer's thread with the shop: an ordinary conversation with the negotiation
	// running through it, including the back-and-forth that produced the live card.
	p.threads = append(p.threads, threadPlan{
		a: buyerKey, b: shopKey,
		messages: []messagePlan{
			{from: buyerKey, body: "Chào bạn, con iPad Gen 9 còn không ạ?", listing: ipad + 1, at: p.now.Add(-30 * time.Hour)},
			{from: shopKey, body: "Còn bạn nhé. Máy chỉ WiFi thôi, không lắp sim được, bạn lưu ý giúp mình.", at: p.now.Add(-29 * time.Hour)},
			{from: buyerKey, body: "Mình dùng ở nhà nên WiFi là đủ. Pin còn khoẻ không bạn?", at: p.now.Add(-28 * time.Hour)},
			{from: shopKey, body: "Pin còn tốt bạn ạ, cháu nhà mình học online cả buổi sáng vẫn còn hơn nửa. Bao da với bút mình tặng kèm luôn.", at: p.now.Add(-27 * time.Hour)},
			{from: buyerKey, body: "4tr4 bạn để lại được không?", at: p.now.Add(-5 * time.Hour)},
			{from: shopKey, body: "Hơi thấp bạn ơi, mình gửi bạn mức giá này nhé.", at: p.now.Add(-4 * time.Hour)},
			{offerKey: "offer-live-from-seller", at: p.now.Add(-3 * time.Hour)},
			{from: buyerKey, body: "Để mình xem lại chút rồi trả lời bạn nhé.", at: p.now.Add(-2 * time.Hour)},
		},
	})

	// The same pair, older: the negotiation that became the finished MacBook order, and the
	// after-sales chat that goes with it.
	p.threads[len(p.threads)-1].messages = append([]messagePlan{
		{from: buyerKey, body: "Bạn ơi, MacBook Air M1 còn không? Mình cần một con để làm đồ án.", listing: macbook + 1, at: p.now.Add(-40 * 24 * time.Hour)},
		{from: shopKey, body: "Còn bạn nhé. Máy sạc 142 lần, mình có chụp màn hình System Report gửi bạn xem.", at: p.now.Add(-40 * 24 * time.Hour).Add(20 * time.Minute)},
		{from: buyerKey, body: "Bạn để 14 triệu được không, mình lấy luôn hôm nay.", at: p.now.Add(-39 * 24 * time.Hour)},
		{offerKey: "offer-macbook-checkout", at: p.now.Add(-39*24*time.Hour + time.Hour)},
		{from: buyerKey, body: "Ok bạn, mình đặt luôn nhé.", at: p.now.Add(-38 * 24 * time.Hour)},
		{from: shopKey, body: "Mình gửi rồi nhé, bạn nhận hàng nhớ quay clip mở hộp cho chắc.", at: p.now.Add(-37 * 24 * time.Hour)},
		{from: buyerKey, body: "Nhận được rồi bạn ơi, máy đẹp hơn mình tưởng. Cảm ơn bạn nhiều.", at: p.now.Add(-33 * 24 * time.Hour)},
		{from: buyerKey, body: "Bạn ơi màn hình Dell kia còn không, mình lấy nốt luôn.", listing: monitor + 1, at: p.now.Add(-4 * 24 * time.Hour)},
		{from: shopKey, body: "Còn nhé, mình gửi kèm dây DisplayPort luôn cho bạn.", at: p.now.Add(-4*24*time.Hour + 30*time.Minute)},
	}, p.threads[len(p.threads)-1].messages...)

	p.threads = append(p.threads,
		threadPlan{
			a: buyerKey, b: "tuan_sport",
			messages: []messagePlan{
				{from: buyerKey, body: "Chào bạn, xe Giant Escape 3 mình cao 1m70 đi vừa không ạ?", listing: bike + 1, at: p.now.Add(-26 * time.Hour)},
				{from: "tuan_sport", body: "Vừa đẹp bạn nhé, size M hợp từ 1m65 đến 1m75.", at: p.now.Add(-25 * time.Hour)},
				{from: buyerKey, body: "Mình qua Bình Thạnh xem trực tiếp được không?", at: p.now.Add(-22 * time.Hour)},
				{from: "tuan_sport", body: "Được bạn, cuối tuần mình ở nhà cả ngày.", at: p.now.Add(-21 * time.Hour)},
				{offerKey: "offer-live-from-buyer", at: p.now.Add(-20 * time.Hour)},
			},
		},
		threadPlan{
			a: buyerKey, b: "huyen_camera",
			messages: []messagePlan{
				{from: buyerKey, body: "Chị ơi ống Sigma 30 F1.4 em soi thấy hình như có mốc ở thấu kính sau ạ.", at: p.now.Add(-46 * 24 * time.Hour)},
				{from: "huyen_camera", body: "Em chụp giúp chị ảnh soi đèn với. Nếu đúng có mốc thì chị nhận lại và hoàn tiền cho em nhé.", at: p.now.Add(-46*24*time.Hour + 2*time.Hour)},
				{from: buyerKey, body: "Em gửi ảnh trong yêu cầu hoàn tiền rồi ạ.", at: p.now.Add(-45 * 24 * time.Hour)},
				{from: "huyen_camera", body: "Chị xem rồi, đúng là có. Chị đồng ý hoàn, em gửi lại theo địa chỉ cũ giúp chị.", at: p.now.Add(-44 * 24 * time.Hour)},
			},
		},
		threadPlan{
			a: shopKey, b: "an_nguyen",
			messages: []messagePlan{
				{from: "an_nguyen", body: "Anh ơi con Fujifilm X-T20 bên anh còn không, em đang tìm máy đầu tay.", listing: fuji + 1, at: p.now.Add(-9 * 24 * time.Hour)},
				{from: shopKey, body: "Máy đó của shop Huyền Camera em ạ, không phải của anh. Bên anh chủ yếu điện thoại với laptop thôi.", at: p.now.Add(-9*24*time.Hour + time.Hour)},
				{from: "an_nguyen", body: "À em nhầm, cảm ơn anh.", at: p.now.Add(-9*24*time.Hour + 90*time.Minute)},
			},
		},
	)
	return nil
}

// addSupportQueue fills the moderator queue. A queue with nothing in it photographs as an
// empty state, which is not the screen a report about a moderation workflow wants.
func (p *plan) addSupportQueue() error {
	anker, err := p.findListing("Sạc dự phòng Anker")
	if err != nil {
		return err
	}

	// The dispute raised by the escalated refund. Its ref is the *order*, not the refund —
	// that is what the trust module names when a refund is handed to staff.
	p.tickets = append(p.tickets, ticketPlan{
		key:       "ticket-refund-dispute",
		requester: buyerKey,
		kind:      "refund-dispute",
		subject:   "Người bán không đồng ý hoàn tiền cho máy bị ám màn",
		refType:   "order",
		refOrder:  "journey-refund-disputed",
		status:    "open",
		createdAt: p.now.Add(-6 * 24 * time.Hour),
		messages: []messagePlan{
			{from: buyerKey, body: "Em gửi ảnh chụp nền trắng, góc trên bên phải bị ám vàng rõ. " +
				"Người bán nói là do em chỉnh màu màn hình nhưng em chưa động vào cài đặt nào cả. " +
				"Nhờ bên mình xem giúp em ạ.", at: p.now.Add(-6 * 24 * time.Hour)},
			{from: adminKey, body: "Chào bạn, bộ phận hỗ trợ đã nhận yêu cầu. " +
				"Bên mình đang đề nghị người bán cung cấp ảnh chụp màn hình trước khi gửi hàng. " +
				"Bạn vui lòng giữ nguyên máy và hộp cho đến khi có kết luận nhé.", at: p.now.Add(-5 * 24 * time.Hour)},
		},
	})

	// A report somebody already answered, so the queue has a resolved row beside the open one.
	p.tickets = append(p.tickets, ticketPlan{
		key:        "ticket-report-listing",
		requester:  "thao_le",
		kind:       "report-listing",
		subject:    "Tin đăng dùng ảnh lấy trên mạng",
		refType:    "listing",
		refListing: anker + 1,
		reason:     "other",
		status:     "resolved",
		assignee:   adminKey,
		action:     "listing-removed",
		resolvedBy: adminKey,
		note:       "Đã xác minh ảnh trùng với ảnh quảng cáo của hãng. Đã ẩn tin và nhắc người bán chụp lại ảnh thật.",
		createdAt:  p.now.Add(-13 * 24 * time.Hour),
		messages: []messagePlan{
			{from: "thao_le", body: "Ảnh trong tin này giống hệt ảnh trên trang chủ của hãng, mình nghĩ không phải ảnh thật của món hàng.", at: p.now.Add(-13 * 24 * time.Hour)},
			{from: adminKey, body: "Cảm ơn bạn đã báo. Bên mình đã kiểm tra và tạm ẩn tin đăng này.", at: p.now.Add(-12 * 24 * time.Hour)},
		},
	})

	// And one a moderator has claimed but not answered, so the "đang xử lý" column is not
	// empty either.
	p.tickets = append(p.tickets, ticketPlan{
		key:       "ticket-order-issue",
		requester: buyerKey,
		kind:      "order-issue",
		subject:   "Đơn hàng báo đã giao nhưng mình chưa nhận được",
		refType:   "order",
		refOrder:  "journey-delivered",
		status:    "reviewing",
		assignee:  adminKey,
		createdAt: p.now.Add(-2 * 24 * time.Hour),
		messages: []messagePlan{
			{from: buyerKey, body: "Bên vận chuyển báo đã giao từ hôm qua nhưng mình không nhận được gói nào, bảo vệ toà nhà cũng nói không có. Nhờ bên mình kiểm tra giúp ạ.", at: p.now.Add(-2 * 24 * time.Hour)},
			{from: adminKey, body: "Bên mình đã liên hệ đơn vị vận chuyển để tra soát, có kết quả sẽ báo lại bạn trong 48 giờ. Bạn chưa bấm xác nhận đã nhận hàng thì tiền vẫn đang được giữ lại nhé.", at: p.now.Add(-40 * time.Hour)},
		},
	})
	return nil
}

// featuredInDataset is set on the handful of listings the dataset marks as the shop window.
// It is read back off the plan rather than carried through, because the only thing it changes
// is how much history the listing gets.
func (l listingPlan) featuredInDataset() bool { return l.featured }

func between(rng *rand.Rand, from, to time.Time) time.Time {
	span := to.Sub(from)
	if span <= 0 {
		return from
	}
	return from.Add(time.Duration(rng.Int64N(int64(span))))
}

// ratingFor is weighted the way a C2C marketplace's ratings actually are: mostly five, some
// four, and the occasional bad one. A flat distribution reads as generated at a glance.
func ratingFor(rng *rand.Rand) int {
	switch n := rng.IntN(100); {
	case n < 62:
		return 5
	case n < 85:
		return 4
	case n < 94:
		return 3
	case n < 98:
		return 2
	default:
		return 1
	}
}

// haggled is what a negotiation settles at: a few percent under the asking price, rounded to
// something a person would actually type.
func haggled(rng *rand.Rand, price int64) int64 {
	cut := price * int64(3+rng.IntN(9)) / 100
	settled := price - cut
	if settled > 100000 {
		settled = settled / 50000 * 50000
	} else {
		settled = settled / 5000 * 5000
	}
	return max(settled, 1)
}
