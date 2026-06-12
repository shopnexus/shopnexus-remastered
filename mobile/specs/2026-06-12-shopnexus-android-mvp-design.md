# ShopNexus Android — MVP mua hàng (Design Spec)

**Date:** 2026-06-12
**Status:** Approved
**Scope:** MVP buyer flow only. Out-of-scope subsystems listed at the end.

## 1. Mục tiêu

Ứng dụng Android (Java thuần) cho sàn ShopNexus, wire trực tiếp với backend đã host
tại `https://shopnexus.hopto.org/api/v1/`. Phục vụ đồ án môn *Phát triển ứng dụng cho
thiết bị di động* (PTIT) — bám giáo trình (Activity/Fragment, Layout, Intent,
SharedPreferences, Service) và bổ sung lớp mạng do giáo trình không dạy networking.

MVP gồm 5 luồng lõi: **Đăng nhập/ký → Trang chủ + tìm kiếm → Chi tiết SP → Giỏ hàng →
Thanh toán + xem đơn**. Đủ chạy end-to-end thật trên API live.

## 2. Tech stack & build

- **Ngôn ngữ:** Java (không Kotlin)
- **Build:** Gradle, AndroidX, Material Components; `minSdk 24`, `targetSdk 34`
- **Networking:** Retrofit 2 + `converter-gson`; OkHttp `Authenticator` (auto-refresh 401) + `Interceptor` (Bearer); `logging-interceptor` (debug)
- **Ảnh:** Glide (URL ảnh nằm trên CDN `down-*.img.susercontent.com`)
- **UI libs:** ViewPager2 (gallery), RecyclerView, SwipeRefreshLayout, BottomNavigationView
- **Kiến trúc:** Activity/Fragment + **Repository mỏng** (callback `onSuccess(T)/onError(ApiException)`). Không MVVM/LiveData (giữ sát giáo trình).

## 3. Backend contract (đã verify trên API live)

- **Base URL:** `https://shopnexus.hopto.org/api/v1/`
- **Auth header:** `Authorization: Bearer <access_token>`
- **Envelope thành công:** `{ "data": <payload> }`
- **Envelope lỗi:** `{ "error": { "http_status": int, "code": string, "message": string } }`

### Endpoints dùng trong MVP

| Nhóm | Method + path | Body / params | Trả về (data) |
|---|---|---|---|
| Auth | `POST account/auth/register/basic` | `{username?,email?,phone?,password,country}` | `{access_token,refresh_token}` |
| Auth | `POST account/auth/login/basic` | `{id,password}` | `{access_token,refresh_token}` |
| Auth | `POST account/auth/refresh` | `{refresh_token}` | `{access_token,refresh_token}` |
| Account | `GET account/me` | — | Account |
| Account | `GET/POST/PATCH/DELETE account/contact` | địa chỉ giao hàng | Contact[] |
| Catalog | `GET catalog/product-card/recommended?limit=` | — | ProductCard[] |
| Catalog | `GET catalog/product-card?search=&category_id=&price_min=&price_max=&...` | filter/paginate | ProductCard[] |
| Catalog | `GET catalog/product-card/:id` | — | ProductCard |
| Catalog | `GET catalog/product-detail?...` | — | ProductDetail |
| Catalog | `GET catalog/category` | — | Category[] |
| Catalog | `GET catalog/comment?...` | đánh giá | Comment[] |
| Cart | `GET order/cart` | — | CartItem[] |
| Cart | `POST order/cart` | `{sku_id, quantity? | delta_quantity?}` | `{message}` |
| Cart | `DELETE order/cart` | — | `{message}` |
| Checkout | `POST order/buyer/quote-transport` | items → options ship (GHTK) | transport options |
| Checkout | `POST order/buyer/checkout` | `{address, payment_option, use_wallet, items:[{sku_id,quantity,transport_option,note}]}` | `{checkout_session_id, payment_url}` |
| Orders | `GET order/buyer/pending-orders` | — | Order[] |
| Orders | `GET order/buyer/completed-orders` | — | Order[] |
| Orders | `GET order/buyer/cancelled-orders` | — | Order[] |
| Orders | `GET order/buyer/orders/:id` | — | Order |

### Model fields cốt lõi (khớp FE types)

- **ProductCard:** `id, slug, seller_id, category_id, name, description, currency, price, original_price, sold, rating{score,total}, is_favorite, resources[{id,url,mime}]`
- **ProductDetail:** `id, name, description, currency, is_favorite, skus[{id, price, original_price, attributes[{name,value}], taken, stock}], resources[], specifications[{name,value}], tags[], comments[]`
- **CartItem:** `{spu_id, sku_id, quantity, currency}` (cập nhật bằng `sku_id` + `quantity`/`delta_quantity`)
- **Order:** `{id, buyer_id, seller_id, transport_id, address, date_created, items[...], total_amount, status}`
- **CheckoutResult:** `{checkout_session_id, payment_url}` — `payment_url` rỗng = thanh toán bằng wallet.

## 4. Package layout

```
com.shopnexus.app
├─ data/
│  ├─ net/
│  │   ├─ ApiService        // Retrofit interface, mọi endpoint MVP
│  │   ├─ ApiClient         // Retrofit/OkHttp singleton, base URL, converters
│  │   ├─ AuthInterceptor   // gắn Bearer token
│  │   ├─ TokenAuthenticator// 401 → refresh (single-use, synchronized) → retry
│  │   ├─ ApiResponse<T>    // { data; error }
│  │   ├─ ApiError          // { http_status, code, message }
│  │   └─ ApiException      // RuntimeException bọc ApiError
│  ├─ model/  AuthCred, Account, Contact, ProductCard, ProductDetail, Sku, Resource,
│  │          Rating, Category, CartItem, CheckoutRequest, CheckoutItem,
│  │          CheckoutResult, Order, OrderItem, Comment
│  └─ repo/   AuthRepo, AccountRepo, CatalogRepo, CartRepo, OrderRepo
├─ ui/
│  ├─ auth/      LoginActivity, RegisterActivity
│  ├─ main/      MainActivity (BottomNav host)
│  │             + HomeFragment, CartFragment, OrdersFragment, ProfileFragment
│  ├─ catalog/   ProductDetailActivity
│  ├─ checkout/  CheckoutActivity, PaymentWebViewActivity
│  └─ adapter/   ProductCardAdapter, CartAdapter, OrderAdapter, CategoryChipAdapter,
│                GalleryAdapter, CommentAdapter
└─ util/  TokenStore (SharedPreferences), Money (format theo currency), Const (BASE_URL)
```

## 5. Lớp mạng (mấu chốt)

- `ApiService` trả `Call<ApiResponse<T>>`. Repo gọi `enqueue` (Retrofit tự chạy off-main-thread, callback về main).
- Repo unwrap: `response.body().data` → `onSuccess`; nếu có `error` hoặc HTTP lỗi → `onError(new ApiException(error))`.
- `AuthInterceptor`: đọc `TokenStore.access()`, nếu có thì thêm header `Authorization: Bearer ...`.
- `TokenAuthenticator` (OkHttp): khi 401, **synchronized** gọi `auth/refresh` với `refresh_token` (single-use như FE custom-fetch), lưu cặp token mới, retry request gốc 1 lần. Refresh fail → `TokenStore.clear()` + broadcast/Intent về `LoginActivity`.
- `TokenStore`: SharedPreferences (`access_token`, `refresh_token`) — bám chương Storage giáo trình.

## 6. Điều hướng & màn hình (9 màn)

Bootstrap (`SplashActivity` hoặc check trong Launcher): có token → `MainActivity`; không → `LoginActivity`.

| Màn | Loại | Nội dung | API |
|---|---|---|---|
| Login | Activity | id + mật khẩu, link sang Register, Google Sign-In (placeholder vòng sau) | login/basic, refresh |
| Register | Activity | username/email/phone + password + country | register/basic |
| Home | Fragment | search bar, category chips, RecyclerView grid 2 cột + infinite scroll | recommended, product-card, category |
| Product Detail | Activity | ViewPager2 gallery, chọn SKU (attributes), giá/tồn theo SKU, nút Thêm giỏ, danh sách comment | product-card/:id, product-detail, comment, cart POST |
| Cart | Fragment | list item, +/- quantity, swipe xóa, tổng tạm tính, nút Thanh toán | cart GET/POST/DELETE |
| Checkout | Activity | chọn địa chỉ (contact), quote-transport (MVP chọn option đầu/rẻ nhất), payment option, đặt hàng | contact, quote-transport, buyer/checkout |
| Payment WebView | Activity | WebView load `payment_url`, `WebViewClient` bắt return URL (success/fail) → finish | (gateway URL) |
| Orders | Fragment | TabLayout: Chờ / Hoàn thành / Đã hủy; mỗi tab 1 RecyclerView | pending/completed/cancelled-orders, orders/:id |
| Profile | Fragment | GetMe, quản lý địa chỉ (contact CRUD), Đăng xuất | me, contact |

### Luồng checkout chi tiết
1. Từ Cart → `CheckoutActivity` với danh sách item đã chọn.
2. Lấy địa chỉ từ `account/contact`; chưa có → nhắc thêm địa chỉ (form contact POST).
3. `POST buyer/quote-transport` → lấy transport options; MVP auto chọn option đầu tiên / rẻ nhất, gán `transport_option` cho mỗi item.
4. `POST buyer/checkout` `{address, payment_option, use_wallet:false, items}`.
5. `payment_url` có → `PaymentWebViewActivity`; `WebViewClient.shouldOverrideUrlLoading` bắt URL return của gateway → xác định thành công/thất bại → về tab Orders.
6. `payment_url` rỗng (wallet) → thẳng tab Orders.

## 7. Luồng dữ liệu (ví dụ Home)

`HomeFragment.onViewCreated` → `CatalogRepo.recommended(limit, cb)` → `ApiService.getRecommended()` (enqueue) → `ApiResponse<List<ProductCard>>` → `cb.onSuccess(list)` → `ProductCardAdapter.submit(list)` (RecyclerView GridLayoutManager 2 cột). Infinite scroll: `RecyclerView.OnScrollListener` đạt cuối → `CatalogRepo.list(page+1)`. Tap item → `Intent` sang `ProductDetailActivity` kèm `product_id`.

## 8. Xử lý lỗi & UX

- `ApiException(code,message)` → Snackbar/Toast tiếng Việt (map vài `code` phổ biến: `validation`, `account_not_found`, `unauthorized`).
- Mất mạng / timeout → state "Thử lại" trên màn.
- 401 → `TokenAuthenticator` tự refresh; refresh fail → về Login (xóa token).
- Loading: ProgressBar + SwipeRefresh cho list.

## 9. Test & demo

- E2E thủ công trên emulator với API live + tài khoản throwaway (register username/email).
- Smoke checklist: register/login → browse recommended → search → mở detail → add cart → cart update → checkout (WebView) → thấy đơn ở Orders → logout.
- Espresso/unit test: ngoài scope MVP (có thể thêm vòng sau cho Repository + JSON parse).

## 10. Ngoài scope MVP (vòng sau, mỗi cái 1 spec riêng)

Chat WebSocket · Refund/dispute · Seller (xác nhận/từ chối đơn, dashboard) ·
Analytic (MPAndroidChart) · Voucher/khuyến mãi · Push notification (FCM) ·
Google Sign-In thật · Đa ngôn ngữ Vi/En · Light/Dark mode · Wishlist/favorite UI đầy đủ.

## 11. Rủi ro / điểm cần xác nhận khi implement

- **payment_url return scheme:** cần xác định URL/scheme gateway redirect về để `WebViewClient` bắt đúng (kiểm tra runtime với 1 đơn thật, hoặc dùng `date_paid`/poll order status làm fallback xác nhận).
- **quote-transport payload:** xác nhận field response để map `transport_option`.
- **register identifier:** backend yêu cầu ít nhất 1 trong username/email/phone (không chỉ `id`).
