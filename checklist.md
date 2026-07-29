# Checklist review database

Việc còn phải xem lại sau đợt review schema (2026-07-28). **Đã review cả 8 module.**
Mục đã đánh `[x]` thì giữ lại làm dấu vết chứ không cần đọc nữa.

Ký hiệu: **[V]** = đã verify bằng chạy migrate thật + insert thử trên DB trắng.
**[R]** = phát hiện bằng đọc code, chưa chạy tới.

Thứ tự đang theo: **schema trước, app sau** — đổi schema khi đã có tiền/đơn thật là việc
đau. Phần schema về cơ bản đã xong; việc lớn nhất còn lại là **10.2**: module `order` phía
Go chưa được viết. Refactor `id.ID[K]` đã xong nên không còn gì chặn nó.

---

## 0. Chặn migrate

**Trống — [V] cả 8 module đã apply sạch** (2026-07-28). 0.1 (`seller_tax_info` rename
dở) và 0.2 (`'verified'` không có trong enum) đã sửa cùng đợt review finance.

`go run ./cmd/migrate` **đã chạy được** (refactor `id.ID[K]` xong): 8/8 module ok trên DB
trắng. Không cần script vòng nữa — xem "Cách verify lại".

## 1. `account`

- [x] **1.1 "Ít nhất một identifier" — đã ép.** [V] `account_has_identifier`
      `CHECK (COALESCE("phone","email","username") IS NOT NULL)`, và comment đầu bảng
      (vốn nói "typically required") đã sửa cho khớp.
      **Hệ quả bắt buộc cho tầng app:** đăng ký OAuth-only phải điền một trong ba —
      sinh `username` khi provider không trả email (Apple "hide my email"). Không ép được
      "có identifier **hoặc** có oauth_identity" ở DB vì đó là ràng buộc liên bảng.
- [ ] **1.2 `profile` thực chất là 0..1, không phải 1-1.** PK = FK nên account tồn tại
      mà không có profile là hợp lệ, trong khi `profile.name NOT NULL` nghĩa là tên
      hiển thị bắt buộc → "account không tên" là trạng thái schema cho phép. Chốt: ép
      tạo profile cùng transaction, hay gộp thẳng vào `account`.
- [x] **1.3 `notification` chưa có partition / retention — đã có.** [V] Cả
      `notification` và `notification_delivery` là hypertable theo `created_at`, chunk 7
      ngày, retention **180 ngày**. Khác `chat.message` (không đặt retention): thông báo
      không phải bằng chứng tranh chấp, "đơn đã giao" của năm ngoái không ai đọc.
- [x] **1.4 Một notification gửi 4 kênh = 4 row trùng — đã tách.** [V] `notification` giữ
      nội dung (category, title, payload, `read_at` thay `is_read`), `notification_delivery`
      giữ mỗi kênh một dòng với `status`/`attempts`/`last_error`/`sent_at`. Đo thật: 1
      notification + 2 kênh = title/payload chỉ tồn tại một bản.
      **Không có FK giữa hai bảng**: retention drop nguyên chunk bên `notification`, FK sẽ
      hoặc chặn drop hoặc để lại dòng mồ côi. Hai retention window đặt bằng nhau để chúng
      đi cùng nhịp. FK về `account` thì vẫn còn (hypertable trỏ ra bảng thường được).
- [x] **1.5 Tên enum quá generic — đã đổi.** [V] `status`→`account_status`,
      `role`→`account_role`, `gender`→`profile_gender`,
      `address_type`→`contact_address_type`. **Chỉ đổi tên type, tên cột giữ nguyên**
      (`"status" "account_status"`), nên không đụng gì tới tầng app. Quét lại toàn bộ 27
      enum của 8 module: không còn tên nào không gắn với chủ thể của nó.
- [x] **1.6 `ON UPDATE CASCADE` ở mọi FK là vô nghĩa — đã bỏ.** [V] 34 FK, bỏ 31, **giữ
      lại 2**: `product_spu_tag` và `tag_embedding` trỏ tới `catalog.tag`, mà `tag.id` là
      `VARCHAR` slug — khoá tự nhiên thì đổi được, nên ở đó cascade mới có nghĩa. Đúng
      ngoại lệ mà luật id đã ghi. `common.option.id` cũng là slug nhưng không FK nào trỏ
      tới nó. `ON DELETE` không bị đụng: vẫn 19 CASCADE / 11 NO ACTION / 3 SET NULL /
      1 RESTRICT như trước.
- [x] **1.7 `profile.country` bị dùng để suy ra currency của ví.** ~~Comment ở
      `finance` ghi `wallet.currency` "matches the account's country".~~ Đã sửa cùng 5.1:
      currency là ISO 4217 tường minh và nằm trong khoá chính của `wallet`.
- [x] **1.8 Không ép cấm opt-out `category='system'` ở DB.** Chốt: thông báo nào là bắt
      buộc là quy tắc sản phẩm, nên domain quyết và bảng chỉ ghi lại. Đã ghi vào comment
      của `notification_preference` để lần sau không ai tưởng là bỏ sót.
- [x] **1.9 KYC giấy tờ tuỳ thân — đã có `account.identity_document`.** [V] Đặt ở
      `account` chứ không `finance`: nó xác lập *người này là ai*, và nối với
      `account.status` (treo tài khoản sau khi bị từ chối) thì cùng schema.
      **Không lưu số giấy tờ, không lưu ảnh scan** — vendor eKYC làm việc kiểm, mình chỉ
      giữ `provider` + `provider_ref` + phán quyết, nên rò bảng này không mạo danh được ai.
      Có `expires_at` vì hộ chiếu/CCCD hết hạn, và partial unique
      `one_verified_per_account` biến "tài khoản này đã xác minh chưa" thành index lookup
      cho cổng payout.

## 2. `catalog`

- [ ] **2.1 Cap `max_length` của job embedding ở 512–1024 token** — việc của tầng app,
      DB đã chặn sẵn nhưng chặn muộn. [V] Đo thật: 1000 non-zero vào được, 1001 bị
      `ERROR: sparsevec cannot have more than 1000 non-zero elements for hnsw index`,
      tức **index ép ngay ở INSERT** chứ không phải lúc build. BGE-M3 sinh một non-zero cho
      mỗi token unique, nên cap 512–1024 là không bao giờ với tới. Không cap mà embed cả
      description dài (BGE-M3 chịu 8192 token) thì cả batch embedding chết ở lệnh ghi.
      Comment trong schema đã nói đúng chỗ nó ép; phần còn lại chờ job được viết.
- [ ] **2.2 `cached_rating` là denormalization xuyên module.** Nguồn là `trust.review`,
      nên catalog phải consume một event từ trust (`bus.Client`) để cập nhật. Chưa có
      event nào; nếu không làm thì `cached_rating` đứng mãi ở 0.
      Đây là **cột denormalized duy nhất còn lại**, và lý do nó tồn tại là cross-module
      (không join được), chứ không phải vì không index được — xem mục "Đã chốt" về giá.
- [x] **2.3 `CREATE EXTENSION vector` thiếu `WITH SCHEMA public` — đã sửa.** [V] Xem
      "Đã chốt" để biết lý do. Lưu ý vận hành: DB nào đã apply `001` rồi thì extension
      vẫn nằm ở `catalog` — sửa file không tự dời nó (xem 7.4).

## 3. `trust`

- [x] **3.1 `trust` không có bảng `audit_log` — đã thêm.** [V] Cùng khuôn với 5 module
      kia (`record_id BIGINT`, unique `(table_name, record_id, version)`). Giờ cả 7 module
      nghiệp vụ đều có; `observability` cố ý không có vì bản thân nó đã là log.
- [x] **3.2 `reputation` và `review` — chốt: KHÔNG gộp.** [V] Thêm
      `review_rating_sum`/`review_rating_count` riêng bên cạnh `rating_sum`/`rating_count`.
      Lý do không cộng chung: một đơn có thể sinh **cả hai** (buyer vừa đánh giá giao dịch
      vừa review sản phẩm), cộng lại là đếm hai lần; và "giao đúng hẹn" không phải cùng
      một khẳng định với "hàng đúng mô tả" — gộp thì mất luôn khả năng phân biệt.
      `reputation_reviews_are_seller_only` ép cột review = 0 với `role='buyer'`, và thêm
      CHECK không âm cho cả 6 counter.
- [x] **3.3 Năm cột `updated_at` còn lại là "lần recompute cuối" — đã ghi chú tại cột.**
      [V] `trust.reputation`, `catalog.product_embedding`, `catalog.category_embedding`,
      `catalog.tag_embedding`, `catalog.account_interest`. Tất cả do cron ghi và mốc đó là
      dữ liệu vận hành (biết khi nào cần embed lại), khác `updated_at` kiểu audit mà
      account/catalog/finance đã bỏ. Giờ mỗi cột có một dòng comment nên đợt dọn sau không
      xoá nhầm.
- [x] **3.4 Đã review kỹ `feedback` / `report`** — kết quả là 3.5–3.11 dưới đây.
- [ ] **3.5 Báo cáo một review thì không xử lý được — y hệt lỗ hổng 8.3 của message.**
      `report_ref_type` đã có `'review'` và `'review-reply'`, nhưng `report_action` **không
      có `'review-removed'`**, và `review`/`review_reply` **không có `deleted_at`**. Tức là
      admin nhận được báo cáo, xử lý xong thì không gỡ được nội dung và cũng không ghi lại
      được là đã gỡ. Đây đúng là lỗ mà 8.3 đã vá cho message (`'message-removed'` +
      `message.deleted_at`), chỉ khác entity. **Ưu tiên cao nhất trong nhóm này.**
- [ ] **3.6 `report` không ép bốn cột kết luận đi cùng `status`.** `action_taken`,
      `resolved_by_id`, `resolved_at`, `resolution_note` set được khi `status='open'`, và
      ngược lại `status='actioned'` không cần cột nào. Đây đúng loại "cột phải đi cùng
      nhau" mà dự án đã ép ở bốn chỗ khác (`account_suspension_requires_suspended`,
      `identity_document_verified_at_matches_status`,
      `notification_delivery_sent_at_matches_status`, và cặp trạng thái của `refund`) —
      chỉ riêng `report` bỏ sót.
- [ ] **3.7 `feedback` cho phép tự đánh giá mình.** `rater_id = ratee_id` vào được, dù
      domain có `ErrSelfFeedback`. Chat đã ép đúng loại này ở DB
      (`CHECK ("account_a_id" < "account_b_id")` chặn luôn tự chat với mình), nên đây là
      lệch chuẩn nội bộ chứ không phải quyết định. Một dòng `CHECK ("rater_id" <> "ratee_id")`.
- [ ] **3.8 Feedback "blind until revealed" nhưng job reveal không có index nào để chạy.**
      [V] `feedback_ratee_id_idx` là partial `WHERE "published_at" IS NOT NULL` — tức nó
      **loại trừ đúng tập hàng mà job cần**. Job "hết X ngày thì công bố, hoặc công bố khi
      cả hai chiều đã nộp" sẽ seq scan cả bảng. Cần partial index ngược lại:
      `("created_at") WHERE "published_at" IS NULL`, cùng khuôn với
      `refund_review_deadline_idx` và `notification_delivery_pending_idx`.
- [ ] **3.9 `report_status_idx` nên là partial trên hàng đợi.** [V] Đo trên 100k report
      với phân bố thật (1.1% open/reviewing): index hiện tại **có** được planner dùng —
      nên lập luận "4 giá trị thì planner bỏ qua" của mục 2 **không áp dụng ở đây**, vì
      phân bố lệch. Nhưng nó lấy cả 1099 hàng rồi mới sort: 837 buffer, 0.94ms. Đổi sang
      `("created_at") WHERE "status" IN ('open','reviewing')`: 36 buffer, 0.073ms, không
      còn node Sort — **nhanh 13×, nhỏ hơn 17×** (40 kB so với 696 kB).
- [ ] **3.10 Index recompute reputation thiếu `direction`.** `feedback_ratee_id_idx` chỉ có
      `("ratee_id")`, trong khi recompute cho `role='seller'` lọc
      `ratee_id = ? AND direction = 'buyer-to-seller'`. Thêm `direction` vào index để mỗi
      role đọc đúng phần của nó. Nhỏ, làm cùng 3.8 cho gọn.
- [ ] **3.11 Mấy thứ nhỏ, chưa chốt.** (a) `review_vote.vote = 0` là một dòng không nói gì
      — cùng loại với `wallet_transaction_moves_something` mà dự án đã chặn; xoá dòng cũng
      diễn đạt được "đã bỏ vote". (b) Không có gì chặn tự vote / tự reply review của mình,
      nhưng đó là liên bảng nên thuộc service. (c) `feedback.comment`, `review.body`,
      `report.detail` đều là `TEXT` không giới hạn, trong khi domain đặt `max=2000`.

## 4. `common`

Mục 4.1–4.4 cũ (về `resource_reference`) đã biến mất cùng bảng đó — xem "Đã chốt".

- [x] **4.1 Listing không gắn được ảnh — đã sửa.** [V] Thêm
      `attachments BIGINT[] NOT NULL DEFAULT '{}'` vào `catalog.product_spu`,
      `catalog.product_sku`, `order.refund`, `order.refund_dispute`. Cùng khuôn với
      `chat.message.attachments` vốn đã có (và đã là `BIGINT[]`, không phải `UUID[]` như
      ghi chú cũ). Tổng cộng 5 cột `attachments` trong schema.
      **Mảng có thứ tự và thứ tự đó là dữ liệu**: `attachments[1]` là ảnh cover của
      listing. SKU rỗng nghĩa là dùng gallery của SPU, nên `NOT NULL DEFAULT '{}'` chứ
      không nullable — "chưa có ảnh" và "không có ảnh riêng" là cùng một trạng thái.
      `account.profile.avatar_resource_id` và `common.option.logo_resource_id` giữ cột đơn
      vì quan hệ 1-1. Không đặt index GIN: truy vấn thường là đọc mảng của một row đã
      biết, còn chiều ngược chỉ có reaper dùng và nó quét một lượt (xem 4.2).
      Chưa có chỗ cho `order-receipt` của enum cũ — chưa có bảng receipt, và bằng chứng
      giao hàng thì nên lấy từ API carrier. Bỏ qua tới khi có nhu cầu thật.
- [ ] **4.2 Không còn gì ràng buộc resource được gắn có tồn tại thật.** Bỏ join table
      là bỏ FK: một id trong `attachments` trỏ tới resource đã xoá thì DB không biết.
      Module sở hữu phải tự kiểm khi ghi. Chiều còn lại là job dọn resource, và giờ nó
      phải gộp **cả 5 mảng** — thiếu một cái là xoá nhầm file đang được dùng:
      ```sql
      WITH referenced AS (
          SELECT unnest(attachments) AS rid FROM catalog.product_spu
          UNION SELECT unnest(attachments) FROM catalog.product_sku
          UNION SELECT unnest(attachments) FROM chat.message
          UNION SELECT unnest(attachments) FROM "order".refund
          UNION SELECT unnest(attachments) FROM "order".refund_dispute
      )
      SELECT id FROM common.resource WHERE id NOT IN (SELECT rid FROM referenced);
      ```
      [V] Đã chạy: nhận đúng resource mồ côi, bỏ qua 4 cái đang được tham chiếu.
      Hai cảnh báo: query này **schema-qualify** nên chỉ chạy khi còn chung một database
      — tách service thì phải đổi thành từng module tự khai resource nó đang giữ. Và nó
      quét toàn bảng, nên khi các mảng lớn lên thì cần GIN index hoặc chạy theo lô.

## 5. `finance`

- [x] **5.1 Đa tiền tệ — đã làm.** [V] `wallet_pkey PRIMARY KEY ("account_id","currency")`.
      Xem "Đã chốt" để biết lý do và các hệ quả kéo theo.
- [x] **5.2 `wallet_transaction` không có cột `currency`.** [V] Đã thêm — row tự mô tả
      được đơn vị của nó, không còn phải suy ra từ ví hay từ `payment_session.fx_snapshot`.
- [ ] **5.3 Cân bằng tiền chưa được ép, chỉ mới đo được.** `group_id` (vừa thêm) cho
      phép viết query đối soát "tổng delta của một movement + tiền qua rail ngoài = 0",
      nhưng không có gì ép. Muốn ép thật thì cần trigger deferred kiểm theo group.
- [ ] **5.4 Hai bất biến của reversal nằm ở tầng service, DB không ép được** (liên row):
      `abs(reversal.amount) <= original.amount`, và currency hai bên phải khớp. Đã ghi
      trong comment cạnh `transaction_reverses_id_unique`; cần test ở service.
- [ ] **5.5 `bank_account.account_number` + `account_holder` lưu plaintext.** Đủ để lừa
      đảo nếu DB rò. Tối thiểu: chắc chắn không bao giờ log. Cân nhắc mã hoá như đã làm
      với secret Vault.
- [x] **5.6 `tax_info.updated_at` — đã bỏ.** [V] Dấu vết thay đổi đã có `finance.audit_log`
      lo, và trạng thái xác minh thì `verified_at` mới là mốc có nghĩa.
- [ ] **5.7 `'topup'` là giá trị enum không có đường vào.** [R] `wallet_txn_kind` có
      `'topup'` nhưng `session_kind` **không có** — mà tiền từ rail ngoài vào thì bắt buộc
      đi qua một session (`transaction` chỉ ghi leg dưới session). Nên hiện không có cách
      nào để user nạp tiền vào ví. Hoặc đây là giá trị chết (cùng loại `'shipping'` ở
      `refund_status`, xem 5.11), hoặc thiếu `session_kind = 'topup'`. Fragment
      `finance` **không có** endpoint nạp tiền vì lý do này — mô hình escrow hiện tại
      không cần: buyer trả trực tiếp qua session `buyer-checkout`.
- [ ] **5.8 Index của "session của tôi" thiếu cột thời gian.** [R]
      `payment_session_from_id_idx` và `_to_id_idx` đều là một cột (`from_id` / `to_id`),
      nên `GET /payment-sessions?role=payer` xếp mới-nhất-trước phải qua node Sort. Đổi
      thành `("from_id", "created_at" DESC)` và tương tự cho `to_id` — đúng khuôn với
      `order_buyer_id_idx` / `item_buyer_id_idx` vốn đã làm vậy.
- [ ] **5.9 Kết luận của admin cho withdrawal nằm trong JSONB.** [R] Theo comment đầu file,
      `withdrawal` không có bảng riêng: đích đến + phán quyết của admin nằm trong
      `payment_session.data`. Hệ quả: "withdrawal admin này đã xử lý" và "còn tồn quá X
      ngày" là truy vấn JSONB chứ không phải index lookup. Cân nhắc đưa
      `resolved_by_id` / `resolved_at` lên thành cột nếu hàng đợi này thành nóng —
      cùng lý lẽ đã dùng cho `order.completed_at` (outcome fact thì nên là cột).
- [x] **5.10 `account_number` không bao giờ ra khỏi API.** Chốt ở tầng contract: DTO
      `BankAccount` chỉ có `account_number_masked`, và `PATCH /bank-accounts/{id}` **chỉ**
      đổi được `is_default` — muốn sửa số thì đăng ký cái mới rồi xoá cái cũ. Lý do sau
      quan trọng hơn lý do bảo mật: một withdrawal đã hoàn tất trỏ tới hàng này như bằng
      chứng tiền đã đi đâu, nên số tài khoản của nó không được phép đổi dưới chân nó.
      Vá được phần "chắc chắn không bao giờ log" của 5.5; phần mã hoá at-rest vẫn mở.

## 6. Chưa review

**Trống — đã review hết 8 module.** `order` ở mục 10, `observability` ở mục 9.

## 7. Doc / vận hành

- [x] **7.1 CLAUDE.md nói module `payment`, code là `finance` — đã sửa.** Kèm
      `internal/provider/payment` → `finance` (thư mục thật tên `finance`).
      **Cái này không chỉ là docs:** `docker-compose.yml` cũng đang cấp `PAYMENT_DB_DSN`
      + `INVENTORY_DB_DSN` mà `config.go` không đọc, và **thiếu hẳn `FINANCE_DB_DSN`** →
      `docker compose --profile app up` chết ngay lúc khởi động (mọi env đều required,
      fail-fast). [V] Đã sửa và so khớp lại: 15 biến compose cấp = đúng 15 biến config
      đọc, không thiếu không thừa.
- [x] **7.2 CLAUDE.md khai `inventory` là module riêng — đã sửa.** `stock` nằm trong
      schema `catalog`; trên đĩa chỉ có 8 module và không có `inventory`.
- [x] **7.3 CLAUDE.md mô tả `trust` thiếu reviews — đã sửa.** Thêm `review`,
      `review_reply`, `review_vote`. Cũng bỏ `resource_reference` khỏi danh sách bảng của
      `common` (bảng đó đã xoá), và ghi thêm hai thứ mới của observability: `instance`
      (env `INSTANCE_ID`) và cách đọc p95 bằng `approx_percentile`.
- [ ] **7.4 Mọi sửa đổi đều là sửa tại chỗ `001_init.sql`, không phải `002_*`.**
      `postgres.Migrate` track version trong `schema_migrations`, nên DB nào đã apply
      `001` rồi thì phải `DROP SCHEMA <module> CASCADE` và migrate lại; sửa file không
      tự động áp dụng.

## 8. `chat`

Đánh số 8 để không phải đổi số các mục cũ; thứ tự section là lịch sử review.

- [ ] **8.1 Read state đang là per-message, chưa per-participant.** `message.status` chạy
      được với thread 1-1, nhưng đếm chưa đọc phải
      `count(*) WHERE conversation_id = ? AND sender_id <> me AND status <> 'read'`.
      Đã đo trên hypertable: query này **fan-out qua mọi chunk** (Bitmap Index Scan trên
      từng `_hyper_1_N_chunk`), và không thể giới hạn theo thời gian vì một tin chưa đọc
      có thể rất cũ. Index `message_unread_idx` có được dùng nhưng không cứu được fan-out.
      Cách gọn: `conversation_participant (conversation_id, account_id, last_read_message_id)`
      → đếm chưa đọc thành O(1), đọc một dòng, không chạm chunk nào. Cũng là chỗ để cho
      admin tham gia thread khi có refund dispute. Chưa chốt.
- [x] **8.2 `message.id BIGINT` bắt hai module khác trả giá — hết hiệu lực.** Tiền đề
      (`report.ref_id` và `chat.audit_log.record_id` phải là `TEXT`) không còn đúng: cả hai
      **đã là `BIGINT`**, và comment cũ "targets mix UUID and bigint keys" cũng đã biến mất
      khỏi file trust. Đợt refactor id giải quyết rồi — `ref_type` cho biết kind nên
      `ParseOpaque` dựng lại được id mờ từ một `BIGINT`. Không phải đổi `message.id` sang
      UUID nữa. Chỗ duy nhất còn `TEXT` là `common.audit_log.record_id`, và đó là cố ý
      (schema `common` trộn khoá BIGINT với khoá slug `option.id`).
- [x] **8.3 Report một message xong chưa xử lý được ở tầng action — đã xong.**
      `trust.report_action` giờ là `('none','listing-removed','message-removed',
      'account-suspended','warning')`, nên việc admin xoá một tin nhắn có chỗ ghi lại.
      `message.deleted_at` lo phần redact.
- [ ] **8.4 Phân trang message phải dùng cursor theo `created_at`, không dùng OFFSET.**
      Hệ quả của việc `message` thành hypertable: chỉ khi query có mốc thời gian thì
      chunk exclusion mới bỏ được chunk cũ. Ràng buộc ở tầng repo, DB không ép được.
- [ ] **8.5 Query hộp thư phải viết dạng `UNION ALL` hai nhánh.** Hệ quả của cặp đối
      xứng: `WHERE account_a_id = me OR account_b_id = me ORDER BY last_message_at DESC`
      sẽ thành sort toàn bộ. Viết thành hai nhánh, mỗi nhánh một ordered index scan, rồi
      merge — mỗi nhánh dùng đúng một trong hai index đã tạo. Ràng buộc ở tầng repo.

## 9. `observability`

- [x] **9.1 `Sink` phải điền `instance` cho cả 4 bảng — đã làm.** `config.InstanceID`
      (env `INSTANCE_ID`, required như mọi biến khác) → `NewSink(bus, log, instance)` →
      cả 4 struct trong `domain/sample.go` → cột `instance` trong 4 lệnh COPY của repo.
      Đóng dấu ở `Sink` chứ không ở từng call site, vì nó là thuộc tính của process.
      Test `TestSink_RecordHTTPPublishesSample` giữ chỗ này khỏi hồi quy.
- [ ] **9.2 `provider_calls.path` phải được template hoá ở `httpx.ObserveOutbound`.**
      Nếu id bị nhúng vào path (`/v1/orders/12345`) thì `GROUP BY path` nổ cardinality.
      DB không ép được, phải làm ở chỗ ghi.
- [ ] **9.3 Chốt danh sách field được mirror vào `business_events.payload`.** Hiện mirror
      cả payload event. Grafana đọc schema này bằng một Postgres datasource, nên ai xem
      được dashboard là xem được mọi thứ ở đây — phạm vi rộng hơn bảng gốc. Chỉ nên
      mirror id + số tiền + trạng thái, không mirror dữ liệu cá nhân.
- [x] **9.4 p95 thật — đã làm.** [V] `timescaledb_toolkit` (1.23.0, có sẵn trong image
      `timescaledb-ha`) + cột `latency percentile_agg("duration_ms")` trong cagg.
      Cách đọc: `approx_percentile(0.95, "latency")`, và `rollup("latency")` trước khi
      gộp nhiều bucket. Đã đo trên 1000 request (90% ~10ms, 10% ~400ms):
      avg 51.5 / p50 13.0 / p95 410.3 — đúng cái mà avg giấu đi.
- [ ] **9.5 Chưa có cagg cho `provider_calls`.** Dashboard latency/error-rate của
      provider vẫn quét raw. Thêm khi nào panel đó thành nóng.
- [ ] **9.6 Telemetry bị mất không tự quan sát được.** `reportDropped` chỉ đếm trong
      process; nếu pipeline chết thì dashboard trông giống "không có traffic". Cân nhắc
      mirror con số đó vào `runtime_metrics`.

## 10. `order`

- [ ] **10.1 `draft_order` sẽ phình vì mỗi row chứa một bản copy của listing.**
      `spu_snapshot` giữ SPU + các SKU, và draft không bao giờ bị xoá (chỉ có
      `cancelled_at`). Draft đã thành order thì không xoá được (`order.draft_id` +
      `item.draft_id` là NO ACTION), nhưng draft bỏ dở / hết hạn mà **không có item** thì
      nên dọn — không thì bảng này dần thành một bản sao của catalog.
      `draft_order_expiring_idx` đã có để job quét.
- [ ] **10.2 Module `order` phía Go không phải "cần sửa cho khớp" — nó chưa được viết.**
      Đã kiểm: 389 dòng, và không hề có `draft_order`, `item`, `transport`, `refund`,
      `offer` ở bất kỳ đâu (`grep` toàn repo chỉ ra `id/kinds.go` và provider payment).
      `domain.Order` là `{ID, BuyerID, Total, Status}` còn repo ghi
      `INSERT INTO orders (buyer_id, total, status)` — sai tên bảng (schema là `order`,
      số ít) và cả ba cột đều không tồn tại trong schema. Nói cách khác 5 "thay đổi hình
      dạng" không có gì để sửa: không có field `address` nào để đổi sang JSONB, không có
      entity `item` hay `draft_order`.
      → Đây là việc **viết mới cả module** theo 9 bảng của schema, không phải một patch.
      Refactor `id.ID[K]` đã xong nên không còn gì chặn: `go build ./...`, `go vet ./...`
      và `go test ./...` (30 gói) đều sạch. Đây giờ là mục lớn nhất còn lại.
- [ ] **10.3 `item.serial_ids` đã bị bỏ** cùng với bảng `catalog.serial` mà bạn xoá. Nếu
      sau này cần theo dõi IMEI/serial thì phải dựng lại cả hai.
- [ ] **10.4 Không lưu số tiền hoàn trên `refund`.** Cố ý: refund là toàn đơn, nên số
      tiền đã có đúng một chỗ ở `finance.transaction` (chân đảo, trỏ tới qua
      `refund_tx_id`). Thêm `amount` vào đây là tạo nguồn sự thật thứ hai cho một con số
      tiền. Nếu đối soát cần nó ở phía order thì đó là lúc thêm — và phải chấp nhận rủi ro
      lệch.
- [ ] **10.5 Không có ràng buộc nào cho các item trong cùng order phải cùng
      `transport_option`.** Một order = một `transport` (`order.transport_id` UNIQUE
      NOT NULL), mà `transport_option` lại chọn ở từng item. Service phải đảm bảo, DB
      không ép được vì nó là quy tắc gom nhóm, không phải fact về một row.
- [ ] **10.6 Bất biến cần test ở service** (không ép ở DB, theo nguyên tắc "DB chỉ ràng
      buộc fact"): máy trạng thái `refund` (`refund_tx_id` chỉ có khi `accepted`,
      `rejection_reason` chỉ khi `rejected`, `return_to_buyer_transport_id` chỉ ở nhánh
      backflow), và `item.cancelled_by_id` chỉ có nghĩa khi `cancelled_at` đã set.

## 11. API contract (OpenAPI)

Đợt viết contract bắt đầu 2026-07-28, **đi từng module một**. Nguồn: schema
(`migrations/`) cho *hình dạng*, `../docs/typst/spec/source-of-truth.typ` (UC-001→013,
BR-001→020) cho *nghiệp vụ*. Code Go ở `api/`, `handler/`, `service.go` là scaffold cũ,
**không phải nguồn tham chiếu**.

Convention dùng chung ghi ở `docs/superpowers/specs/2026-07-28-order-api-design.md` §1 —
đọc nó trước khi viết fragment cho module tiếp theo.

- [x] **11.1 `order` — xong.** 34 endpoint, 8 bảng, kèm surface moderator. Delta schema mà
      nó kéo theo nằm ở §2 của spec doc (5 mục: 3 cột UC-006 trên `order`,
      `payout_session_id`, `refund` sang đồng hồ 48h, `product_spu.shipping_paid_by`,
      3 kind mới trong `id/kinds.go`). **Chưa làm migration nào.**
- [x] **11.2 `finance` — xong.** 25 endpoint. Quyết định định hình cả surface:
      **client không tự mở payment session** — order tạo (checkout / accept offer / phí
      ship) hoặc job tạo (payout); user chỉ *thêm leg* vào session đã có
      (`POST /payment-sessions/{id}/payments`), mỗi rail một leg một gateway URL, và đó
      cũng là cách split-tender hoạt động. Ngoại lệ duy nhất là `withdrawal`.
      Ledger ví phân trang theo `seq` **không theo `created_at`** (timestamp trùng thì
      không replay được), nên `wallet_transaction` **không cần** kind id mới — `seq` vừa
      là danh tính vừa là thứ tự. Findings: 5.7–5.10.
- [x] **11.3 Cả 7 module xong** (2026-07-28). 166 endpoint, 200 schema:

      | module | endpoint | ghi chú định hình |
      |---|---|---|
      | `account` | 44 | 11 bảng: auth + profile + contact + device + notification + favorite/follow + KYC + admin |
      | `order` | 34 | pay-first, transition là POST lên sub-resource danh từ |
      | `catalog` | 25 | browse/search/feed + SKU + stock + category/tag + moderation |
      | `finance` | 25 | client không tự mở session; withdrawal cần admin duyệt |
      | `trust` | 19 | feedback (blind) tách khỏi review; report queue |
      | `common` | 10 | upload presigned, server đặt `object_key` |
      | `chat` | 9 | 1 thread / 1 cặp; đọc đều theo cursor thời gian |

      Bốn quyết định lặp lại ở nhiều module, đáng ghi vì đều xuất phát từ schema:
      – **Bảng hypertable thì "mark read" nhận mốc thời gian, không nhận danh sách id.**
      `notification` và `chat.message` đều chunk theo `created_at`; update theo id lẻ
      fan-out qua mọi chunk. Kèm hệ quả tốt: `notification` **không cần** id mờ nào cả.
      – **Ngược lại `chat` GET unread-count vẫn không tránh được fan-out** vì read state
      là per-message (mục 8.1 chưa chốt). Đã ghi thẳng vào description của endpoint đó
      thay vì giấu đi.
      – **Ledger `wallet_transaction` phơi `seq`, không phơi id** — `seq` vừa là danh tính
      vừa là thứ tự, nên tiết được một prefix vĩnh viễn.
      – **Số tài khoản / push token / `vault_secret_path` không bao giờ ra khỏi API.**
      `BankAccount.account_number_masked`, `Device.push_token_suffix`, và `Option` cố ý
      không có `vault_secret_path`.

- [x] **11.5 Id schema dồn hết về `openapi.base.yaml`.** Ban đầu mỗi fragment tự khai id
      của module khác (`OrderAccountID`, `FinanceAccountID`, sắp có `CommonAccountID`…) vì
      namespace schema là phẳng nên phải prefix. Sai chỗ: id là primitive của
      `shared/id`, không của module, và hơn nửa số kind bị 3–4 fragment tham chiếu. Giờ
      base khai đúng một schema cho mỗi kind trong `kinds.go` (25 cái) + `CurrencyCode`,
      fragment chỉ `$ref`. Lý tưởng nhất là `cmd/specgen` sinh chúng từ `kinds.go` để chỉ
      còn một nguồn — chưa làm, `kinds.go` không có registry để liệt kê.
- [x] **11.6 5 kind mới — đã thêm.** `CartItem`→`crt`, `DraftOrder`→`drf`,
      `Transport`→`trp`, `Device`→**`dvc`**, `IdentityDocument`→`idd`. Chốt 2026-07-28,
      lúc chưa phát id nào. 25 kind, khớp 1-1 với 25 id schema ở `openapi.base.yaml`.
      **`RefsResolve` không bắt được lệch giữa hai chỗ này** — nó chỉ kiểm YAML, nên thêm
      entity mà quên `kinds.go` thì lỗi chỉ hiện lúc marshal đầu tiên.
- [x] **11.8 Gateway đã scaffold — 166/166 route đăng ký, mọi handler trả 501.**
      `errx.ErrNotImplemented` (501, code `not_implemented`). **501 chứ không 404**: hai
      cái nói hai chuyện khác nhau, 404 là client sai URL còn 501 là URL đúng mà chưa có
      tính năng — nối route documented vào 404 là bắt client debug sai đầu.
      Handler struct **vẫn giữ `svc`/`v`/`log`** dù chưa dùng, để graph fx còn thật (pool
      của module vẫn mở, config vẫn validate lúc khởi động) và viết một method là sửa tại
      chỗ chứ không phải đi nối dây lại. `handler.Payment` → `handler.Finance` cho khớp tên
      module.
      **Route viết tay trong `router.go`, không sinh từ spec lúc chạy** — router dựng từ
      document sẽ pass `AllPathsRouted` một cách tự động và mất luôn khả năng bắt endpoint
      documented mà chưa ai nối. Bảng tên handler thì sinh một lần bằng script có assert
      hai chiều với spec (thiếu tên → fail, tên không có route → fail).
- [x] **11.9 `AllPathsRouted` đã thành hard fail** (đảo lại 11.4). Đã verify guard sống:
      bỏ một dòng route ra khỏi `router.go` thì test fail và chỉ đúng
      `POST /orders/{id}/receipt`. Chiều nguy hiểm hơn (route sống không có contract) vẫn
      không kiểm được vì `http.ServeMux` không expose pattern đã đăng ký.
- [ ] **11.10 Hai test đã bị xoá cùng handler cũ, cần viết lại khi implement.**
      `handler/catalog_test.go` (đã xoá) assert hai hành vi vẫn còn giá trị:
      handler lấy `userID` từ `gwctx` chứ không từ body, và request sai validate tag thì
      trả 400 trước khi gọi service. `router_test.go` cũng mất
      `PublicGetListing_RejectsRawID` — id thô trong path phải là 400, không được xuống
      service dưới dạng zero value. Cả ba phải quay lại cùng handler thật.
- [ ] **11.7 Delta schema mà spec 7 module kéo theo.** Ngoài 4 mục ở §2 của spec doc
      order, thêm: `catalog.product_spu` cần `shipping_paid_by` (đã ghi), và
      `finance.payment_session` cần index `("from_id","created_at" DESC)` (mục 5.8).
      **Chưa apply migration nào.**
- [ ] **11.4 `openapi_contract_test.go` đã đổi ý nghĩa.** Trước: mọi path trong spec phải
      resolve trên router (hard fail) — sai hướng cho contract-first, vì viết fragment
      trước handler thành ra test đỏ. Giờ là hai test: `RefsResolve` (hard fail — mọi
      `$ref` phải trỏ tới component có thật, đây là guard thật vì merge làm phẳng schema
      của cả 7 module vào một namespace nên order ref được `PaymentSession` của finance),
      và `AllPathsRouted` chỉ **báo cáo** (56/72 chưa có route). 405 cũng tính là chưa có
      route: ServeMux trả 405 khi path có mà method khác, nên chỉ nhìn 404 sẽ coi
      `GET /orders` là đã có nhờ route `POST /orders`. **Đổi lại thành hard fail khi
      gateway được viết xong.**

---

## Cách verify lại

Cách chuẩn giờ đã dùng được — `cmd/migrate` compile và chạy. Trỏ mọi DSN vào một
**database trắng** (DB dev đã có `schema_migrations` đánh dấu `001` nên migrate sẽ skip
và không nói lên gì). Container không publish port, nên lấy IP trên bridge của compose:

```bash
DSN="postgres://app:app@172.18.0.2:5432/shopnexus_verify?sslmode=disable"   # ip: xem docker inspect
GATEWAY_ADDR=localhost:8080 INSTANCE_ID=verify LOG_LEVEL=info \
ID_CIPHER_KEY=0123456789abcdef0123456789abcdef JWT_SECRET=0123456789012345678901234567890123 \
NATS_URL=nats://172.18.0.4:4222 REDIS_ADDR=172.18.0.3:6379 REDIS_PASSWORD=app \
ACCOUNT_DB_DSN=$DSN CATALOG_DB_DSN=$DSN ORDER_DB_DSN=$DSN CHAT_DB_DSN=$DSN \
COMMON_DB_DSN=$DSN TRUST_DB_DSN=$DSN FINANCE_DB_DSN=$DSN OBSERVABILITY_DB_DSN=$DSN \
go run ./cmd/migrate
```

Muốn assert trên schema thì vẫn dùng `psql` **bên trong container db** (không cần port
trên host):

```bash
C=shopneuxsnew-db-1; DB=shopnexus_verify
docker exec -i $C psql -U app -d postgres -c "DROP DATABASE IF EXISTS $DB WITH (FORCE)" -c "CREATE DATABASE $DB"
for m in account catalog chat common order finance observability trust; do
  docker exec -i $C psql -U app -d $DB -c "CREATE SCHEMA IF NOT EXISTS \"$m\""
  { printf 'SET search_path TO "%s", public;\n' "$m"; cat internal/module/$m/migrations/*.sql; } \
    | docker exec -i $C psql -U app -d $DB -v ON_ERROR_STOP=1 --single-transaction -f -
done
```

`--single-transaction` là để giống `postgres.Migrate`, vốn đưa cả file qua một
`pool.Exec` (Postgres bọc trong implicit transaction). Không có cross-schema FK nên thứ
tự module không quan trọng. Hai cái bẫy đã dính: (1) đừng coi mọi stdout là lỗi —
`create_hypertable`/`add_retention_policy` trả về result row, chỉ grep `ERROR|FATAL`;
(2) `SAVEPOINT` không dùng được vì psql chạy autocommit, muốn test một INSERT phải hỏng
thì chạy riêng một lệnh.

Nếu `docker` báo `permission denied` dù đã ở trong group `docker`: shell hiện tại lấy
credential từ trước lúc thêm group. `sg docker -c '<lệnh>'` chạy được ngay, hoặc đăng
nhập lại.

Trạng thái lần chạy cuối (2026-07-28): **cả 8 module apply sạch**, kèm assert cho
`draft_order.spu_snapshot`, ví đa tiền tệ (5.1) và p95 qua cagg (9.4).
PostGIS, TimescaleDB 2.28.3 và `timescaledb_toolkit` 1.23.0 đều có sẵn trên
`timescale/timescaledb-ha:pg18`.

---

## Đã chốt, đừng review lại

Quyết định đã cân nhắc và kết luận, kèm lý do (lý do mới là phần dễ mất):

- **Không cần bảng seller/shop.** Marketplace C2C thuần: `catalog.product_spu.account_id`
  là seller, `common.option.owner_id` chứa cấu hình payment/transport riêng của người
  bán, `trust.reputation` giữ rating, `finance.bank_account`/`tax_info` giữ payout.
  `account.profile` chính là trang shop. "Vacation mode" cũng không cần cột mới:
  `listing_status='hidden'` là đủ.
- **Session/refresh token lưu ở Redis, không phải Postgres.** `cache.Client` có `Set`
  với TTL + `Delete` nên revoke rẻ hơn DB. Hai điều cần nhớ ở tầng app: khi set
  `status='suspended'` phải chủ động `Delete` key session (DB không chặn được token
  đang lưu hành), và flush Redis = logout toàn hệ thống.
- **Review sản phẩm thuộc `trust`, không thuộc `catalog`.** Trust đã sở hữu feedback
  hai chiều theo order; một chỗ authoritative là điều giữ rating người bán khỏi lệch
  giữa hai module. Khi chuyển đã bỏ: polymorphic `ref_type`/`ref_id` (thành `spu_id`
  tường minh), `ref_type='account'` (trùng `feedback`), `updated_at`, và thang điểm
  0..100 (đổi sang `rating` 1..5 cho khớp `feedback`).
- **`review.order_id` NOT NULL** — không mua thì không review.
- **Reply không giới hạn số lần trên mỗi thread.** Constraint cũ của catalog
  (`UNIQUE (ref_type, ref_id, account_id)`) chặn mỗi user một reply/thread.
- **KHÔNG cache giá ở `product_spu`.** Sort feed theo giá là join sang `product_sku`
  (cùng schema) và planner **không cần sort node**: ordered index scan trên
  `product_sku_price_idx` làm bảng dẫn, nested loop tra `product_spu` qua unique index
  `product_spu_featured_sku_id_key`, nested loop giữ nguyên thứ tự outer nên `LIMIT`
  dừng sớm. Đã đo trên 50k listing: quét 24 index row để ra 20 kết quả, 98 buffer hit,
  0.37ms, không có Sort. Một *index* không span 2 bảng, nhưng *plan* thì tránh được
  sort — đừng suy từ cái thứ nhất ra cái thứ hai.
  Lưu ý duy nhất: bảng dẫn là `product_sku`, nên nếu tỉ lệ listing `active` xuống thấp
  thì scan phải đi qua nhiều entry hơn trước khi gom đủ `LIMIT`.
- **Index browse đều partial `WHERE status='active' AND deleted_at IS NULL`**
  (created_at / cached_rating), thay cho index trên `status` — 4 giá trị thì planner
  không dùng. Riêng `'pending'` có partial index cho hàng đợi kiểm duyệt. Giá không
  nằm trong nhóm này, nó đi qua `product_sku_price_idx`.
- **Soft delete thống nhất bằng `deleted_at`** ở cả `product_spu` và `product_sku`;
  `status` chỉ còn lo lifecycle + moderation. Hai thứ không chồng nhau: `'hidden'` là
  người bán tạm hạ một listing đang sống, `deleted_at` là mất hẳn. Soft chứ không hard
  vì `order.item` giữ `spu_id`/`sku_id` mà không có FK, nên lịch sử đơn phải còn tra
  được sau khi người bán xoá listing.
- **Xoá category không cascade sang dữ liệu.** `category_parent_id_fkey` là
  `ON DELETE SET NULL` (xoá cha thì con thành root, không mất nhánh),
  `product_spu_category_id_fkey` là `ON DELETE RESTRICT` (phải dọn hết listing mới xoá
  được category; `category_id` NOT NULL nên SET NULL không dùng được).
- **`featured_sku_id` nullable, không dùng `DEFERRABLE`.** Nullable đã đủ phá vòng lặp:
  INSERT SPU (NULL) → INSERT SKU → UPDATE SPU.
- **`stock.sku_id` có FK thật** — sau khi bỏ `ref_type` polymorphic thì FK khai được,
  và hai bảng cùng schema nên không tốn gì.
- **`audit_log` thay cho `updated_at`** ở `account` và `catalog`.
- **Mọi extension khai `WITH SCHEMA public`.** Extension là object cấp *database* và chỉ
  nằm ở **một** schema, nên `CREATE EXTENSION` dưới `search_path = <module>, public` sẽ
  cài vào schema của module. Đã đo hai hệ quả khi `vector` nằm ở `catalog`: schema khác
  báo `type "vector" does not exist`, và `DROP SCHEMA catalog CASCADE` — đúng quy trình
  re-migrate ở 7.4 — kéo theo `drop cascades to extension vector`. Cái bẫy thật nằm ở chỗ
  khác: module thứ hai cần vector sẽ khai `CREATE EXTENSION IF NOT EXISTS vector` và gặp
  **no-op im lặng** (đã tồn tại ở cấp DB), extension vẫn ở `catalog`, rồi CREATE TABLE của
  nó fail với lỗi trỏ sai chỗ — migration của chính nó không tự chữa được.
  Nếu sau này mỗi module một database thì chuyện này gần như vô hại (mỗi DB một extension
  riêng); nó chỉ cắn ở mô hình chung DB, tức là mô hình hiện tại.
- **Vector index cho BGE-M3: HNSW cho cả dense và sparse.** `dense vector(1024)` +
  `vector_cosine_ops`, `sparse sparsevec(250048)` + `sparsevec_ip_ops`.
  – HNSW chứ không IVFFlat: IVFFlat tuy không có giới hạn 1000 nnz nhưng cần k-means
  training trên dữ liệu có sẵn, mà các bảng embedding khởi tạo rỗng rồi fill dần bằng
  cron → recall tệ cho tới khi rebuild. Giới hạn nnz thì xử lý được bằng cap
  `max_length` (mục 2.1), rẻ hơn nhiều so với đổi loại index.
  – `sparsevec_ip_ops` vì `compute_lexical_matching_score` của BGE-M3 là **dot product**
  các weight của token chung, không phải L2 hay cosine.
  – Dense giữ cosine: FlagEmbedding trả về vector đã normalize nên cosine và IP xếp hạng
  y nhau, nhưng cosine vẫn đúng nếu norm có lúc lệch khỏi 1.
  – `sparsevec(250048)` chỉ cần lớn hơn token id lớn nhất (vocab XLM-R ~250k); chiều
  khai dư không tốn storage vì sparsevec chỉ lưu phần non-zero. Nhưng insert vector khai
  chiều khác là pgvector báo lỗi → fix cứng con số này ở tầng job.
  – Khi viết query: `<#>` trả về **inner product âm**, nên `ORDER BY ... <#> :q` tăng dần
  mới là giống nhất, và điểm phải đổi dấu khi trả ra ngoài.
- **Khoá chính là `BIGINT` identity, không phải UUID** (trừ khoá dạng slug:
  `catalog.tag.id`, `common.option.id` — cả hai đã CHECK kebab-case). Ra ngoài API thì
  `internal/shared/id` mã hoá thành chuỗi `prefix_base32` bằng Feistel 4 vòng khoá AES,
  tweak riêng theo từng kind. Hệ quả cho schema: cột `ref_id` polymorphic **không cần**
  là TEXT nữa — `BIGINT` + `ref_type` là đủ, vì `FormatOpaque`/`ParseOpaque` lấy kind từ
  `ref_type`. Hai điều phải nhớ ở tầng vận hành: khoá AES gần như vĩnh viễn (rotate là
  vô hiệu mọi id đã phát ra) nên phải backup như credential DB; và id mờ **không phải**
  authorization — mọi handler vẫn cần check ownership.
- **Tên kind và prefix bám theo tên bảng, không theo từ vựng sản phẩm.**
  `Listing` → **`ProductSPU`** và prefix `lst` → **`spu`** (bảng là `catalog.product_spu`);
  `SKU` → **`ProductSKU`** (prefix `sku` vốn đã khớp). Xoá kind **`Comment`**: entity đó đã
  thành `trust.review`, không còn bảng nào, nên `cmt` là prefix chết.
  Đổi prefix là **cửa một chiều** — nó là tweak của cipher, nên mọi id đã phát ra sẽ vô
  hiệu. Làm được vì lúc này chưa có dữ liệu và chưa phát id nào. **Sau khi có user thật
  thì không đổi nữa.** Đã kiểm bằng golden vector: `acc`/`ord`/`msg` giữ nguyên giá trị,
  chỉ nhánh `spu` đổi — đúng như kỳ vọng khi chỉ tweak của một kind thay đổi.
  18 kind còn lại đã khớp tên bảng. Vẫn **chưa có kind** cho `draft_order`, `transport`,
  `cart_item`, `review_vote` — thiếu chứ không lệch, thêm khi module `order` được viết.
- **Đường đi của id, sau khi refactor `id.ID[K]` xong.** `domain`/`port`/`adapter` là
  `int64` thuần; chỉ DTO trong `api/` dùng `ID[K]`; service đổi ở đúng biên
  (`req.X.Int64()` vào, `id.Of[K](n)` ra). Bốn quyết định kèm theo:
  – **Subject của JWT là id mờ**, không phải khoá thô: token nằm trong tay client và
  decode được, để khoá tuần tự ở đó là vô hiệu hoá chính `shared/id`.
  – **`gwctx` mang `id.ID[id.Account]`, không mang string.** Middleware parse một lần, nên
  handler thả thẳng vào DTO và không handler nào phải tự quyết định subject hỏng nghĩa là
  gì (nó thành 401 tại middleware).
  – **`id.SetCipher` được gọi trong `cmd/gateway` qua `fx.Invoke`** với `ID_CIPHER_KEY`
  (env, required). Trước đó không ai gọi — lần marshal id đầu tiên sẽ **panic**. Độ dài
  khoá do `SetCipher` kiểm (16/24/32), không lặp lại ở validate tag để hai nơi khỏi lệch.
  – **Event trên bus giữ khoá thô** (`OrderPlaced.OrderID int64`): nó không phải payload
  công khai, và module nhận cần khoá để tra DB.
- **`common.resource_reference` bị bỏ.** Mỗi bảng tự quản lý `attachments BIGINT[]`, vì
  ghi message/listing và reference của nó qua hai schema sẽ không atomic được khi tách
  service. Đánh đổi: mất FK, xem 4.2.
- **DB chỉ ràng buộc *fact*, không ràng buộc *nghiệp vụ*.** Số dư không bao giờ âm là
  fact → `CHECK` trong schema. Các bước chuyển trạng thái hợp lệ của `refund` là quy tắc
  nghiệp vụ → thuộc domain layer, dù hoàn toàn viết được thành CHECK. Lý do: quy tắc
  nghiệp vụ đổi theo sản phẩm, ép ở DB thì mỗi lần đổi là một migration, và cùng một quy
  tắc ở hai nơi sẽ lệch. Vẫn ép ở DB: dấu âm, cột phải đi cùng nhau, uniqueness,
  one-default-per-owner, idempotency key.
- **Refund là toàn đơn, không hoàn lẻ.** Hoàn lẻ kéo theo cả một luồng thương lượng
  (seller mở thương lượng → buyer không đồng ý → quá X ngày tự hoàn full → seller không
  đồng ý thì mở dispute), nên không làm. Vì vậy không có bảng `refund_item`, và
  `refund.amount` cũng không lưu (xem 10.4).
- **`draft_order` là phiên mua của buyer**, tồn tại để đóng băng điều khoản: listing hiện
  100k thì lúc chốt đơn không được tính giá vừa update. `item.draft_id` và
  `order.draft_id` (UNIQUE) nối chuỗi lại: một phiên mua sinh tối đa một order, và item
  tồn tại được trước khi order có.
- **Đóng băng bằng `spu_snapshot JSONB`, không phải con trỏ.** Snapshot phải chứa **cả các
  SKU**, vì giá nằm ở `product_sku` — snapshot riêng SPU thì chẳng đóng băng được gì; kèm
  `package_details` vì báo giá vận chuyển tính từ đó.
  Không dùng id của `audit_log` để trỏ, dù nó cũng lưu snapshot, vì: giá nằm ở bảng khác
  nên một id chỉ trỏ được một dòng trong khi cần SPU + N SKU; `product_spu` thuộc
  `catalog` nên đó là con trỏ xuyên schema và sẽ là xuyên database; và `audit_log` là log
  sẽ bị prune, prune là mất điều khoản đã chốt của đơn.
- **`item.order_id` là `ON DELETE NO ACTION`, không phải CASCADE.** `item` giữ
  `payment_session_id` + `total_amount`, tức nó *là* bản ghi tiền buyer đã trả; CASCADE
  cho phép một câu `DELETE FROM "order"` xoá sạch dấu vết tiền.
- **Địa chỉ trong order là snapshot JSONB của contact, không phải một dòng text.** Mã hành
  chính là thứ carrier API cần, nên thiếu nó thì không tạo lại được vận đơn từ dữ liệu đơn
  (`account.contact` có thể đã đổi). `order.pickup_address` cũng snapshot, vì carrier cần
  cả hai đầu.
- **`order` không có cột status, chỉ có `completed_at`/`cancelled_at`.** Vòng đời là việc
  của service; "kết thúc khi nào" là fact, và nó biến "đơn đang mở của tôi" thành index
  lookup thay vì join qua items + transport + refunds.
- **Telemetry có vòng đời dữ liệu, khác `chat.message`.** Cả 4 hypertable của
  `observability` đều columnstore sau 7 ngày và bị drop theo retention (http_requests 30
  ngày, provider_calls 30, runtime_metrics 90, business_events 180). Lý do khác `chat`: ở
  đây dữ liệu sinh ra để vẽ dashboard và bỏ được, còn tin nhắn là bằng chứng trong tranh
  chấp. Không đặt retention thì cái để giám sát hệ thống sẽ là cái làm sập hệ thống.
- **Mọi bảng telemetry có `instance`.** Không có nó thì từ pod thứ hai trở đi
  `runtime_metrics` vô nghĩa (heap/goroutine của 3 pod đan vào một series) và không tách
  được "một pod chậm" khỏi "cả hệ thống chậm". Giá trị đến từ env `INSTANCE_ID`
  (**required**, không default về hostname: một giá trị sai mà trông hợp lý còn tệ hơn là
  chết lúc khởi động), và được `Sink` đóng dấu một lần thay vì truyền qua từng call site.
- **Dùng từ vựng columnstore mới của TimescaleDB 2.18+**: `enable_columnstore` /
  `segmentby` / `orderby` + `CALL add_columnstore_policy`, chứ không phải
  `timescaledb.compress` + `add_compression_policy` (đã deprecated, sẽ bỏ ở major kế).
  Cả hai gọi cùng một hàm C, và `timescaledb_information.jobs` vẫn hiện
  `policy_compression`. Đã verify trên 2.28.3.
- **Cagg `http_requests_1m` bật `materialized_only = false`.** Để default (true) thì
  panel mất 1–2 phút gần nhất, vì job refresh luôn chạy sau `end_offset`. Và
  `start_offset` là 1 ngày chứ không phải 1 giờ: refresh chỉ tính lại bucket bị
  invalidate nên cửa sổ rộng gần như miễn phí, nhưng cứu được trường hợp job fail một lúc
  — cửa sổ 1 giờ sẽ để lại lỗ vĩnh viễn.
- **p95 lưu bằng sketch, không lưu bằng số.** Cagg giữ
  `percentile_agg("duration_ms") AS latency` (UddSketch của `timescaledb_toolkit`), đọc ra
  bằng `approx_percentile(0.95, "latency")` và `rollup("latency")` khi gộp nhiều bucket.
  Không lưu sẵn một con số p95 mỗi phút vì **percentile không cộng được**: trung bình của
  các p95 theo phút không phải p95 của cả giờ. Sketch thì rollup được nên nó đúng ở mọi
  khoảng thời gian. `percentile_cont` không dùng được ở đây — nó cần toàn bộ row, tức
  không partial-aggregate được, tức không nằm trong cagg được.
  Đã đo (1000 request, 90% ~10ms + 10% ~400ms): avg 51.5 / p50 13.0 / p95 410.3.
- **Cagg tạo được bên trong migration.** `postgres.Migrate` đưa cả file qua một
  `pool.Exec`, tức Postgres bọc trong implicit transaction, mà
  `CREATE MATERIALIZED VIEW ... WITH (timescaledb.continuous)` vẫn chạy — đã verify trên
  2.28.3, không cần tách file riêng. Dùng `WITH NO DATA` để migration không phải
  materialize lịch sử.
- **Idempotency là ràng buộc ở DB, không phải quy ước ở code.** `transaction` có
  `provider_ref` + `UNIQUE ("payment_option","provider_ref")`, nên webhook được gateway
  gửi lại (at-least-once, chúng retry tới khi nhận 200) sẽ vi phạm unique thay vì ghi có
  lần hai. `wallet_transaction` có `idempotency_key` unique cho cùng mục đích ở phía ví.
- **Ví đa tiền tệ: `wallet_pkey PRIMARY KEY ("account_id","currency")`.** Mở một số dư
  mới thành một INSERT, thay vì một migration đổi PK trên bảng đang giữ tiền thật — đó
  là thứ đắt, nên trả trước khi bảng còn rỗng. Bốn thứ đi kèm, tất cả là hệ quả bắt buộc
  chứ không phải tuỳ chọn:
  – `wallet_transaction.currency` (đóng luôn 5.2): một row ledger phải tự nói được đơn vị
  của nó. Trước đây phải suy từ ví, mà `topup`/`adjustment` không có session nên
  `payment_session.fx_snapshot` cũng không cứu được.
  – `seq` là per **ví**, không per account: `UNIQUE ("account_id","currency","seq")`. Mỗi
  số dư là một ledger riêng và đánh số lại từ 1.
  – FK thật `("account_id","currency") → wallet`: cùng schema nên khai được, và nó chặn
  việc ghi tiền vào một ví chưa từng mở.
  – Không có index `("account_id","currency","seq" DESC)` riêng: unique constraint đã xếp
  đúng thứ tự cột đó và btree quét ngược rẻ y như quét xuôi. Index DESC riêng chỉ đáng
  khi thứ tự **trộn chiều** (a ASC, b DESC).
  Đổi lại: `wallet` không còn tra được bằng `account_id` đơn lẻ, và chuyển tiền giữa hai
  ví của cùng một người là **hai movement + một tỉ giá**, không phải một UPDATE.
- **Ledger ví có thứ tự toàn phần bằng `seq` per account**, không dựa vào `created_at`
  (hai row trùng timestamp là chuỗi không replay được và thiếu row không phát hiện
  được). `seq` được cấp dưới cùng row lock với việc cập nhật số dư
  (`SELECT ... FOR UPDATE` trên `wallet`) — đó cũng là thứ chặn hai movement đồng thời
  tính `*_after` từ cùng một số dư cũ.
- **Ba vòng đời, ba enum riêng ở finance**: `session_status` (5 trạng thái),
  `transaction_status` (chỉ `pending/success/failed` theo đúng state machine của leg), và
  `verification_status` cho KYC. Trước đó cả ba dùng chung một enum `status`, nên mỗi cột
  đều giữ được trạng thái nó không bao giờ hợp lệ — và KYC thì thiếu hẳn `'verified'`.
- **Secret của provider không nằm trong DB.** `common.option.data` chỉ chứa config không
  bí mật; credential nằm ở Vault, `option.vault_secret_path` giữ đường dẫn. Provider
  client resolve lúc runtime, nên không có key material trong DB, backup, replica, hay
  snapshot của `audit_log`.
- **`common.resource` soft delete.** `provider` + `object_key` là handle duy nhất tới
  object trên storage, nên row phải sống lâu hơn lệnh xoá cho tới khi reaper dọn xong
  object. Xoá row trước là leak file vĩnh viễn.
- **Chat: một thread cho mỗi cặp account, không scope theo listing.** Cặp được lưu có
  thứ tự (`account_a_id` < `account_b_id`) nên không thể lách unique bằng cách đổi bên,
  và cùng CHECK đó chặn luôn tự chat với mình. `buyer_id`/`seller_id` cũ bị bỏ vì với
  một thread duy nhất thì vai trò không còn nghĩa. Muốn nhắc tới một sản phẩm cụ thể
  (gửi offer) thì đi qua `message.metadata`.
- **Chat: `attachments UUID[]` inline, cố ý không dùng `common.resource_reference`.**
  Message và reference của nó ở hai schema, ghi cả hai atomic sẽ không còn khả thi khi
  tách module ra service riêng.
- **Chat: `message_type ('user','system')`, `sender_id` NULL trên row 'system'.** Có
  CHECK ép hai thứ đi cùng nhau. text và ảnh không phải type riêng — ảnh là `attachments`.
- **Chat: `message` là hypertable theo `created_at`, KHÔNG bật retention policy.** Chunk
  hoá để index maintenance và vacuum không phình theo bảng, nhưng tin nhắn là bằng chứng
  trong refund dispute và user mong đợi lịch sử, nên việc xoá dữ liệu cũ là quyết định
  sản phẩm/pháp lý, không phải default. Cách bật ghi trong comment của file.
- **`account_interest` không cần vector index.** Nó được đọc bằng PK để dựng feed, còn
  ANN chạy trên `product_embedding`; mỗi account chỉ có vài slot nên quét là miễn phí.
  Index chỉ có nghĩa cho chiều ngược ("listing này thì ai muốn") và sẽ phải chịu chi phí
  các vector này bị ghi lại liên tục.
- **`date_of_birth` chỉ CHECK lower bound `> 1900-01-01`.** Không dùng
  `CHECK (dob < now())` vì `now()` không IMMUTABLE, để trong CHECK là footgun khi
  dump/restore. Chặn ngày tương lai + tuổi tối thiểu thuộc `domain`.
- **`contact.district_code` nullable** — VN bỏ cấp huyện từ 2025-07-01 (còn tỉnh–xã).
  Carrier vẫn giữ territory id riêng → đó là lý do có `provider_codes JSONB`.
- **`contact.location` nullable** — geocode fail vẫn phải lưu được địa chỉ; mã hành
  chính mới là routing source of truth, pin chỉ advisory.
- **`profile.locale` / `timezone` NOT NULL không DEFAULT** — tránh hardcode giả định
  `vi-VN` vào schema.
- **`notification_category` là enum, không phải free text** — preference key theo
  category, typo sẽ đọc thành "không có preference" → fallback sang gửi, tức opt-out bị
  bỏ qua âm thầm.
- **Default của notification preference nằm ở `domain`, không trong DB** — nó khác nhau
  theo category và đổi mà không nên phải migrate. Bảng chỉ lưu chỗ user lệch khỏi default.
- **`device.push_token` UNIQUE toàn cục** (không per-account) — FCM/APNs cấp lại đúng
  token đó cho máy cũ khi user khác đăng nhập; upsert theo token để dời `account_id`,
  không thì chủ cũ vẫn nhận push của máy đó.
