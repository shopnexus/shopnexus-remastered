# ShopNexus Android MVP — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Java Android app (buyer MVP) wired to the live ShopNexus backend: login → browse/search → product detail → cart → checkout (WebView payment) → view orders.

**Architecture:** Activity/Fragment UI + a thin Repository layer over Retrofit. One `BottomNavigationView` host (`MainActivity`) with 4 fragments (Home, Cart, Orders, Profile); separate Activities for Login, Register, ProductDetail, Checkout, PaymentWebView. JSON over Retrofit+Gson; OkHttp `Authenticator` auto-refreshes JWT on 401. Tokens in SharedPreferences.

**Tech Stack:** Java 17, AGP 8.5.x / Gradle 8.9, AndroidX, Material 1.12, Retrofit 2.11 + converter-gson, OkHttp logging 4.12, Glide 4.16, ViewPager2, RecyclerView, SwipeRefreshLayout. JUnit 4 for pure-JVM unit tests.

**Spec:** `mobile/specs/2026-06-12-shopnexus-android-mvp-design.md`

---

## Prerequisites (execution environment)

The current dev box has **no JDK / Gradle / Android SDK**. Build, run, and smoke-test
steps MUST run where the Android SDK is installed (Android Studio on the developer
machine, or a CI image with `cmdline-tools` + an emulator/device). Code-writing tasks
can proceed anywhere; the `./gradlew` / emulator steps are gated on that toolchain.

- JDK 17, Android Studio (or `sdkmanager` + `platform-tools`), an AVD or USB device.
- Project root for the app: `mobile/app-android/` (new Android project; spec/plan stay in `mobile/specs/`).
- `INTERNET` permission + cleartext NOT needed (backend is HTTPS).

**Testing convention used below:**
- **Unit (TDD):** pure-JVM logic (Gson model parsing, envelope unwrap, Money formatter) → `src/test/java/...`, run with `./gradlew test`.
- **UI (manual smoke):** Activities/Fragments verified by `./gradlew installDebug` + a scripted click-through. Espresso is out of scope per spec.

---

## Phase 0 — Project scaffold

### Task 0.1: Create Android project skeleton

**Files:**
- Create: `mobile/app-android/settings.gradle.kts`
- Create: `mobile/app-android/build.gradle.kts` (root)
- Create: `mobile/app-android/gradle.properties`
- Create: `mobile/app-android/app/build.gradle.kts`
- Create: `mobile/app-android/app/src/main/AndroidManifest.xml`
- Create: `mobile/app-android/app/src/main/res/values/strings.xml`

- [ ] **Step 1: Create the project via Android Studio** (New Project → Empty Views Activity, Language = Java, package `com.shopnexus.app`, minSdk 24). Then overwrite the generated Gradle files with the contents below so dependencies/versions match this plan. (If scaffolding by hand, create the files directly.)

- [ ] **Step 2: Root `settings.gradle.kts`**

```kotlin
pluginManagement {
    repositories { google(); mavenCentral(); gradlePluginPortal() }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories { google(); mavenCentral() }
}
rootProject.name = "ShopNexus"
include(":app")
```

- [ ] **Step 3: Root `build.gradle.kts`**

```kotlin
plugins {
    id("com.android.application") version "8.5.2" apply false
}
```

- [ ] **Step 4: `gradle.properties`**

```properties
org.gradle.jvmargs=-Xmx2048m
android.useAndroidX=true
android.nonTransitiveRClass=true
```

- [ ] **Step 5: `app/build.gradle.kts`**

```kotlin
plugins { id("com.android.application") }

android {
    namespace = "com.shopnexus.app"
    compileSdk = 34
    defaultConfig {
        applicationId = "com.shopnexus.app"
        minSdk = 24
        targetSdk = 34
        versionCode = 1
        versionName = "0.1"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }
    buildTypes {
        release { isMinifyEnabled = false }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures { viewBinding = true }
}

dependencies {
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.android.material:material:1.12.0")
    implementation("androidx.constraintlayout:constraintlayout:2.1.4")
    implementation("androidx.recyclerview:recyclerview:1.3.2")
    implementation("androidx.viewpager2:viewpager2:1.1.0")
    implementation("androidx.swiperefreshlayout:swiperefreshlayout:1.1.0")
    implementation("androidx.fragment:fragment:1.8.2")

    implementation("com.squareup.retrofit2:retrofit:2.11.0")
    implementation("com.squareup.retrofit2:converter-gson:2.11.0")
    implementation("com.squareup.okhttp3:logging-interceptor:4.12.0")
    implementation("com.github.bumptech.glide:glide:4.16.0")

    testImplementation("junit:junit:4.13.2")
    testImplementation("com.google.code.gson:gson:2.11.0")
}
```

- [ ] **Step 6: `AndroidManifest.xml`** (Activities added in later tasks; start minimal)

```xml
<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <uses-permission android:name="android.permission.INTERNET" />
    <application
        android:allowBackup="true"
        android:label="ShopNexus"
        android:supportsRtl="true"
        android:theme="@style/Theme.Material3.DayNight.NoActionBar">
        <activity android:name=".ui.auth.LoginActivity" android:exported="true">
            <intent-filter>
                <action android:name="android.intent.action.MAIN" />
                <category android:name="android.intent.category.LAUNCHER" />
            </intent-filter>
        </activity>
    </application>
</manifest>
```

- [ ] **Step 7: Verify build** (toolchain machine)

Run: `cd mobile/app-android && ./gradlew assembleDebug`
Expected: `BUILD SUCCESSFUL` (LoginActivity created in Phase 2; for now create an empty `LoginActivity` stub extending `AppCompatActivity` so the manifest resolves, or run after Task 2.1).

- [ ] **Step 8: Commit**

```bash
git add mobile/app-android
git commit -m "scaffold android project"
```

---

## Phase 1 — Networking, models, token store (TDD on pure-JVM parts)

### Task 1.1: Constants + Money formatter (TDD)

**Files:**
- Create: `app/src/main/java/com/shopnexus/app/util/Const.java`
- Create: `app/src/main/java/com/shopnexus/app/util/Money.java`
- Test: `app/src/test/java/com/shopnexus/app/util/MoneyTest.java`

- [ ] **Step 1: Write the failing test**

```java
package com.shopnexus.app.util;
import static org.junit.Assert.assertEquals;
import org.junit.Test;
public class MoneyTest {
    @Test public void formatsVndWithGrouping() {
        assertEquals("199.000 ₫", Money.format(199000, "VND"));
    }
    @Test public void formatsForeignCurrencyWithCode() {
        assertEquals("96 THB", Money.format(96, "THB"));
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./gradlew test --tests "*MoneyTest"`
Expected: FAIL — `Money` not found.

- [ ] **Step 3: Implement `Const.java` and `Money.java`**

```java
// Const.java
package com.shopnexus.app.util;
public final class Const {
    public static final String BASE_URL = "https://shopnexus.hopto.org/api/v1/";
    private Const() {}
}
```

```java
// Money.java
package com.shopnexus.app.util;
import java.text.DecimalFormat;
import java.text.DecimalFormatSymbols;
import java.util.Locale;
public final class Money {
    private Money() {}
    public static String format(long amount, String currency) {
        DecimalFormatSymbols sym = new DecimalFormatSymbols(Locale.US);
        sym.setGroupingSeparator('.');
        String grouped = new DecimalFormat("#,###", sym).format(amount);
        if ("VND".equalsIgnoreCase(currency)) return grouped + " ₫";
        return grouped + " " + currency;
    }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `./gradlew test --tests "*MoneyTest"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add app/src/main/java/com/shopnexus/app/util app/src/test
git commit -m "add Const and Money formatter with tests"
```

### Task 1.2: API envelope + ApiException (TDD)

**Files:**
- Create: `app/src/main/java/com/shopnexus/app/data/net/ApiResponse.java`
- Create: `app/src/main/java/com/shopnexus/app/data/net/ApiError.java`
- Create: `app/src/main/java/com/shopnexus/app/data/net/ApiException.java`
- Test: `app/src/test/java/com/shopnexus/app/data/net/ApiResponseTest.java`

- [ ] **Step 1: Write the failing test** (Gson parses both envelopes — pure JVM)

```java
package com.shopnexus.app.data.net;
import static org.junit.Assert.*;
import com.google.gson.Gson;
import org.junit.Test;
public class ApiResponseTest {
    static class Tok { String access_token; String refresh_token; }
    @Test public void parsesDataEnvelope() {
        String json = "{\"data\":{\"access_token\":\"a\",\"refresh_token\":\"r\"}}";
        ApiResponse<Tok> r = new Gson().fromJson(json,
            com.google.gson.reflect.TypeToken.getParameterized(ApiResponse.class, Tok.class).getType());
        assertNull(r.error);
        assertEquals("a", r.data.access_token);
    }
    @Test public void parsesErrorEnvelope() {
        String json = "{\"error\":{\"http_status\":401,\"code\":\"account_not_found\",\"message\":\"Account not found\"}}";
        ApiResponse<Tok> r = new Gson().fromJson(json,
            com.google.gson.reflect.TypeToken.getParameterized(ApiResponse.class, Tok.class).getType());
        assertNull(r.data);
        assertEquals("account_not_found", r.error.code);
        assertEquals(401, r.error.http_status);
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./gradlew test --tests "*ApiResponseTest"`
Expected: FAIL — classes not found.

- [ ] **Step 3: Implement the three classes**

```java
// ApiError.java
package com.shopnexus.app.data.net;
public class ApiError {
    public int http_status;
    public String code;
    public String message;
}
```

```java
// ApiResponse.java
package com.shopnexus.app.data.net;
public class ApiResponse<T> {
    public T data;
    public ApiError error;
}
```

```java
// ApiException.java
package com.shopnexus.app.data.net;
public class ApiException extends RuntimeException {
    public final String code;
    public final int httpStatus;
    public ApiException(ApiError e) {
        super(e != null && e.message != null ? e.message : "Đã có lỗi xảy ra");
        this.code = e != null ? e.code : "unknown";
        this.httpStatus = e != null ? e.http_status : 0;
    }
    public ApiException(String message) { super(message); this.code = "network"; this.httpStatus = 0; }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `./gradlew test --tests "*ApiResponseTest"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add app/src/main/java/com/shopnexus/app/data/net app/src/test
git commit -m "add API envelope, error, exception with tests"
```

### Task 1.3: Model POJOs (TDD on parsing a real product card)

**Files:**
- Create: `app/src/main/java/com/shopnexus/app/data/model/Resource.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/Rating.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/ProductCard.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/ProductDetail.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/Sku.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/Attribute.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/Category.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/CartItem.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/AuthCred.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/Account.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/Contact.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/CheckoutRequest.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/CheckoutItem.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/CheckoutResult.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/Order.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/OrderItem.java`
- Create: `app/src/main/java/com/shopnexus/app/data/model/Comment.java`
- Test: `app/src/test/java/com/shopnexus/app/data/model/ProductCardParseTest.java`

- [ ] **Step 1: Write the failing test** (uses the real recommended-card shape)

```java
package com.shopnexus.app.data.model;
import static org.junit.Assert.*;
import com.google.gson.Gson;
import org.junit.Test;
public class ProductCardParseTest {
    @Test public void parsesRealCard() {
        String json = "{\"id\":\"x\",\"name\":\"Áo\",\"currency\":\"THB\",\"price\":96,"
            + "\"original_price\":120,\"sold\":5,\"rating\":{\"score\":0.9,\"total\":5},"
            + "\"resources\":[{\"id\":\"r1\",\"url\":\"http://img/1\",\"mime\":\"image/jpeg\"}]}";
        ProductCard c = new Gson().fromJson(json, ProductCard.class);
        assertEquals("Áo", c.name);
        assertEquals(96, c.price);
        assertEquals("http://img/1", c.resources.get(0).url);
        assertEquals(5, c.rating.total);
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `./gradlew test --tests "*ProductCardParseTest"`
Expected: FAIL — model classes not found.

- [ ] **Step 3: Implement the POJOs** (public fields matching JSON keys; `long` for money, `int` for counts)

```java
// Resource.java
package com.shopnexus.app.data.model;
public class Resource { public String id; public String url; public String mime; }
```
```java
// Rating.java
package com.shopnexus.app.data.model;
public class Rating { public double score; public int total; }
```
```java
// Attribute.java
package com.shopnexus.app.data.model;
public class Attribute { public String name; public String value; }
```
```java
// ProductCard.java
package com.shopnexus.app.data.model;
import java.util.List;
public class ProductCard {
    public String id, slug, seller_id, category_id, name, description, currency;
    public long price, original_price; public int sold;
    public Rating rating; public boolean is_favorite;
    public List<Resource> resources;
}
```
```java
// Sku.java
package com.shopnexus.app.data.model;
import java.util.List;
public class Sku {
    public String id; public long price, original_price;
    public List<Attribute> attributes; public int taken, stock;
}
```
```java
// ProductDetail.java
package com.shopnexus.app.data.model;
import java.util.List;
public class ProductDetail {
    public String id, slug, seller_id, name, description, currency;
    public boolean is_favorite;
    public List<Sku> skus; public List<Resource> resources;
    public List<Attribute> specifications; public List<String> tags;
}
```
```java
// Category.java
package com.shopnexus.app.data.model;
public class Category { public String id; public String name; }
```
```java
// CartItem.java
package com.shopnexus.app.data.model;
public class CartItem { public String spu_id; public String sku_id; public int quantity; public String currency; }
```
```java
// AuthCred.java
package com.shopnexus.app.data.model;
public class AuthCred { public String access_token; public String refresh_token; }
```
```java
// Account.java
package com.shopnexus.app.data.model;
public class Account { public String id; public String username; public String email; public String phone; public String country; }
```
```java
// Contact.java
package com.shopnexus.app.data.model;
public class Contact { public String id; public String full_name; public String phone; public String address; }
```
```java
// CheckoutItem.java
package com.shopnexus.app.data.model;
public class CheckoutItem {
    public String sku_id; public long quantity; public String transport_option; public String note;
    public CheckoutItem(String sku_id, long quantity, String transport_option, String note) {
        this.sku_id = sku_id; this.quantity = quantity; this.transport_option = transport_option; this.note = note;
    }
}
```
```java
// CheckoutRequest.java
package com.shopnexus.app.data.model;
import java.util.List;
public class CheckoutRequest {
    public boolean buy_now; public String address; public String payment_option;
    public boolean use_wallet; public List<CheckoutItem> items;
}
```
```java
// CheckoutResult.java
package com.shopnexus.app.data.model;
public class CheckoutResult { public String checkout_session_id; public String payment_url; }
```
```java
// OrderItem.java
package com.shopnexus.app.data.model;
public class OrderItem {
    public String sku_id, spu_id, sku_name, slug, image_url, note, transport_option;
    public int quantity; public long subtotal_amount, total_amount;
}
```
```java
// Order.java
package com.shopnexus.app.data.model;
import java.util.List;
public class Order {
    public String id, buyer_id, seller_id, address, status, date_created, currency;
    public long total_amount; public List<OrderItem> items;
}
```
```java
// Comment.java
package com.shopnexus.app.data.model;
public class Comment { public String id, account_id, body; public int score; public String date_created; }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `./gradlew test --tests "*ProductCardParseTest"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add app/src/main/java/com/shopnexus/app/data/model app/src/test
git commit -m "add model POJOs with card parse test"
```

### Task 1.4: TokenStore (SharedPreferences)

**Files:**
- Create: `app/src/main/java/com/shopnexus/app/util/TokenStore.java`

- [ ] **Step 1: Implement** (no unit test — depends on Android `Context`; verified at runtime in Phase 2)

```java
package com.shopnexus.app.util;
import android.content.Context;
import android.content.SharedPreferences;
public class TokenStore {
    private static final String PREF = "shopnexus_auth";
    private static final String K_ACCESS = "access_token";
    private static final String K_REFRESH = "refresh_token";
    private final SharedPreferences sp;
    public TokenStore(Context ctx) {
        sp = ctx.getApplicationContext().getSharedPreferences(PREF, Context.MODE_PRIVATE);
    }
    public void save(String access, String refresh) {
        sp.edit().putString(K_ACCESS, access).putString(K_REFRESH, refresh).apply();
    }
    public String access()  { return sp.getString(K_ACCESS, null); }
    public String refresh() { return sp.getString(K_REFRESH, null); }
    public boolean isLoggedIn() { return access() != null; }
    public void clear() { sp.edit().clear().apply(); }
}
```

- [ ] **Step 2: Commit**

```bash
git add app/src/main/java/com/shopnexus/app/util/TokenStore.java
git commit -m "add TokenStore over SharedPreferences"
```

### Task 1.5: ApiService + ApiClient + interceptors

**Files:**
- Create: `app/src/main/java/com/shopnexus/app/data/net/ApiService.java`
- Create: `app/src/main/java/com/shopnexus/app/data/net/AuthInterceptor.java`
- Create: `app/src/main/java/com/shopnexus/app/data/net/TokenAuthenticator.java`
- Create: `app/src/main/java/com/shopnexus/app/data/net/ApiClient.java`

- [ ] **Step 1: `ApiService.java`** (all MVP endpoints; bodies as `Map`/typed POJOs)

```java
package com.shopnexus.app.data.net;
import com.shopnexus.app.data.model.*;
import java.util.List;
import java.util.Map;
import retrofit2.Call;
import retrofit2.http.*;

public interface ApiService {
    // Auth
    @POST("account/auth/login/basic")
    Call<ApiResponse<AuthCred>> login(@Body Map<String, Object> body);
    @POST("account/auth/register/basic")
    Call<ApiResponse<AuthCred>> register(@Body Map<String, Object> body);
    @POST("account/auth/refresh")
    Call<ApiResponse<AuthCred>> refresh(@Body Map<String, Object> body);

    // Account
    @GET("account/me") Call<ApiResponse<Account>> me();
    @GET("account/contact") Call<ApiResponse<List<Contact>>> contacts();
    @POST("account/contact") Call<ApiResponse<Contact>> addContact(@Body Map<String, Object> body);

    // Catalog
    @GET("catalog/product-card/recommended")
    Call<ApiResponse<List<ProductCard>>> recommended(@Query("limit") int limit);
    @GET("catalog/product-card")
    Call<ApiResponse<List<ProductCard>>> products(@QueryMap Map<String, String> q);
    @GET("catalog/product-card/{id}")
    Call<ApiResponse<ProductCard>> productCard(@Path("id") String id);
    @GET("catalog/product-detail")
    Call<ApiResponse<ProductDetail>> productDetail(@Query("id") String id);
    @GET("catalog/category") Call<ApiResponse<List<Category>>> categories();
    @GET("catalog/comment") Call<ApiResponse<List<Comment>>> comments(@Query("product_id") String productId);

    // Cart
    @GET("order/cart") Call<ApiResponse<List<CartItem>>> cart();
    @POST("order/cart") Call<ApiResponse<Map<String, Object>>> updateCart(@Body Map<String, Object> body);
    @DELETE("order/cart") Call<ApiResponse<Map<String, Object>>> clearCart();

    // Checkout
    @POST("order/buyer/quote-transport")
    Call<ApiResponse<Object>> quoteTransport(@Body Map<String, Object> body);
    @POST("order/buyer/checkout")
    Call<ApiResponse<CheckoutResult>> checkout(@Body CheckoutRequest body);

    // Orders
    @GET("order/buyer/pending-orders")   Call<ApiResponse<List<Order>>> pendingOrders();
    @GET("order/buyer/completed-orders") Call<ApiResponse<List<Order>>> completedOrders();
    @GET("order/buyer/cancelled-orders") Call<ApiResponse<List<Order>>> cancelledOrders();
    @GET("order/buyer/orders/{id}")      Call<ApiResponse<Order>> order(@Path("id") String id);
}
```

- [ ] **Step 2: `AuthInterceptor.java`**

```java
package com.shopnexus.app.data.net;
import androidx.annotation.NonNull;
import com.shopnexus.app.util.TokenStore;
import java.io.IOException;
import okhttp3.Interceptor;
import okhttp3.Request;
import okhttp3.Response;
public class AuthInterceptor implements Interceptor {
    private final TokenStore tokens;
    public AuthInterceptor(TokenStore tokens) { this.tokens = tokens; }
    @NonNull @Override public Response intercept(@NonNull Chain chain) throws IOException {
        Request req = chain.request();
        String access = tokens.access();
        if (access != null) {
            req = req.newBuilder().header("Authorization", "Bearer " + access).build();
        }
        return chain.proceed(req);
    }
}
```

- [ ] **Step 3: `TokenAuthenticator.java`** (single-use refresh, synchronized, one retry)

```java
package com.shopnexus.app.data.net;
import androidx.annotation.Nullable;
import com.google.gson.Gson;
import com.shopnexus.app.data.model.AuthCred;
import com.shopnexus.app.util.Const;
import com.shopnexus.app.util.TokenStore;
import java.io.IOException;
import java.util.Collections;
import okhttp3.*;
public class TokenAuthenticator implements Authenticator {
    private final TokenStore tokens;
    private final Gson gson = new Gson();
    public TokenAuthenticator(TokenStore tokens) { this.tokens = tokens; }

    @Nullable @Override public synchronized Request authenticate(@Nullable Route route, Response response) throws IOException {
        if (responseCount(response) >= 2) return null;        // already retried
        String refresh = tokens.refresh();
        if (refresh == null) { tokens.clear(); return null; }

        // Bare OkHttp call to the refresh endpoint (no AuthInterceptor recursion).
        OkHttpClient bare = new OkHttpClient();
        RequestBody body = RequestBody.create(
            gson.toJson(Collections.singletonMap("refresh_token", refresh)),
            MediaType.get("application/json"));
        Request req = new Request.Builder().url(Const.BASE_URL + "account/auth/refresh").post(body).build();
        try (Response resp = bare.newCall(req).execute()) {
            if (!resp.isSuccessful() || resp.body() == null) { tokens.clear(); return null; }
            ApiResponse<AuthCred> parsed = gson.fromJson(resp.body().string(),
                com.google.gson.reflect.TypeToken.getParameterized(ApiResponse.class, AuthCred.class).getType());
            if (parsed.data == null) { tokens.clear(); return null; }
            tokens.save(parsed.data.access_token, parsed.data.refresh_token);
            return response.request().newBuilder()
                .header("Authorization", "Bearer " + parsed.data.access_token).build();
        }
    }
    private int responseCount(Response r) {
        int c = 1; while ((r = r.priorResponse()) != null) c++; return c;
    }
}
```

- [ ] **Step 4: `ApiClient.java`** (singleton)

```java
package com.shopnexus.app.data.net;
import android.content.Context;
import com.shopnexus.app.util.Const;
import com.shopnexus.app.util.TokenStore;
import okhttp3.OkHttpClient;
import okhttp3.logging.HttpLoggingInterceptor;
import retrofit2.Retrofit;
import retrofit2.converter.gson.GsonConverterFactory;
public final class ApiClient {
    private static ApiService service;
    public static synchronized ApiService get(Context ctx) {
        if (service == null) {
            TokenStore tokens = new TokenStore(ctx);
            HttpLoggingInterceptor log = new HttpLoggingInterceptor();
            log.setLevel(HttpLoggingInterceptor.Level.BASIC);
            OkHttpClient client = new OkHttpClient.Builder()
                .addInterceptor(new AuthInterceptor(tokens))
                .addInterceptor(log)
                .authenticator(new TokenAuthenticator(tokens))
                .build();
            service = new Retrofit.Builder()
                .baseUrl(Const.BASE_URL)
                .client(client)
                .addConverterFactory(GsonConverterFactory.create())
                .build()
                .create(ApiService.class);
        }
        return service;
    }
    private ApiClient() {}
}
```

- [ ] **Step 5: Commit**

```bash
git add app/src/main/java/com/shopnexus/app/data/net
git commit -m "add ApiService, ApiClient, auth interceptor + authenticator"
```

### Task 1.6: Repository base + RepoCallback + the 5 repos

**Files:**
- Create: `app/src/main/java/com/shopnexus/app/data/repo/RepoCallback.java`
- Create: `app/src/main/java/com/shopnexus/app/data/repo/BaseRepo.java`
- Create: `app/src/main/java/com/shopnexus/app/data/repo/AuthRepo.java`
- Create: `app/src/main/java/com/shopnexus/app/data/repo/CatalogRepo.java`
- Create: `app/src/main/java/com/shopnexus/app/data/repo/CartRepo.java`
- Create: `app/src/main/java/com/shopnexus/app/data/repo/OrderRepo.java`
- Create: `app/src/main/java/com/shopnexus/app/data/repo/AccountRepo.java`

- [ ] **Step 1: `RepoCallback.java` + `BaseRepo.java`** (unwrap envelope, route to main thread via Retrofit's callback which is already main-thread on Android)

```java
// RepoCallback.java
package com.shopnexus.app.data.repo;
import com.shopnexus.app.data.net.ApiException;
public interface RepoCallback<T> {
    void onSuccess(T data);
    void onError(ApiException e);
}
```

```java
// BaseRepo.java
package com.shopnexus.app.data.repo;
import com.shopnexus.app.data.net.*;
import retrofit2.Call;
import retrofit2.Callback;
import retrofit2.Response;
abstract class BaseRepo {
    protected <T> void enqueue(Call<ApiResponse<T>> call, RepoCallback<T> cb) {
        call.enqueue(new Callback<ApiResponse<T>>() {
            @Override public void onResponse(Call<ApiResponse<T>> c, Response<ApiResponse<T>> resp) {
                ApiResponse<T> body = resp.body();
                if (body == null) {
                    ApiError err = new ApiError(); err.http_status = resp.code();
                    err.code = "http_" + resp.code(); err.message = "Lỗi máy chủ (" + resp.code() + ")";
                    cb.onError(new ApiException(err)); return;
                }
                if (body.error != null) { cb.onError(new ApiException(body.error)); return; }
                cb.onSuccess(body.data);
            }
            @Override public void onFailure(Call<ApiResponse<T>> c, Throwable t) {
                cb.onError(new ApiException("Mất kết nối mạng. Vui lòng thử lại."));
            }
        });
    }
}
```

- [ ] **Step 2: `AuthRepo.java`**

```java
package com.shopnexus.app.data.repo;
import android.content.Context;
import com.shopnexus.app.data.model.AuthCred;
import com.shopnexus.app.data.net.ApiClient;
import com.shopnexus.app.data.net.ApiService;
import com.shopnexus.app.util.TokenStore;
import java.util.HashMap;
import java.util.Map;
public class AuthRepo extends BaseRepo {
    private final ApiService api; private final TokenStore tokens;
    public AuthRepo(Context ctx) { api = ApiClient.get(ctx); tokens = new TokenStore(ctx); }
    public void login(String id, String password, RepoCallback<AuthCred> cb) {
        Map<String,Object> b = new HashMap<>(); b.put("id", id); b.put("password", password);
        enqueue(api.login(b), wrapSave(cb));
    }
    public void register(String username, String email, String phone, String password, String country, RepoCallback<AuthCred> cb) {
        Map<String,Object> b = new HashMap<>();
        if (username != null && !username.isEmpty()) b.put("username", username);
        if (email != null && !email.isEmpty()) b.put("email", email);
        if (phone != null && !phone.isEmpty()) b.put("phone", phone);
        b.put("password", password); b.put("country", country);
        enqueue(api.register(b), wrapSave(cb));
    }
    private RepoCallback<AuthCred> wrapSave(RepoCallback<AuthCred> cb) {
        return new RepoCallback<AuthCred>() {
            @Override public void onSuccess(AuthCred c) { tokens.save(c.access_token, c.refresh_token); cb.onSuccess(c); }
            @Override public void onError(com.shopnexus.app.data.net.ApiException e) { cb.onError(e); }
        };
    }
}
```

- [ ] **Step 3: `CatalogRepo.java`**

```java
package com.shopnexus.app.data.repo;
import android.content.Context;
import com.shopnexus.app.data.model.*;
import com.shopnexus.app.data.net.ApiClient;
import com.shopnexus.app.data.net.ApiService;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
public class CatalogRepo extends BaseRepo {
    private final ApiService api;
    public CatalogRepo(Context ctx) { api = ApiClient.get(ctx); }
    public void recommended(int limit, RepoCallback<List<ProductCard>> cb) { enqueue(api.recommended(limit), cb); }
    public void search(String query, int page, int limit, RepoCallback<List<ProductCard>> cb) {
        Map<String,String> q = new HashMap<>();
        if (query != null && !query.isEmpty()) q.put("search", query);
        q.put("page", String.valueOf(page)); q.put("limit", String.valueOf(limit));
        enqueue(api.products(q), cb);
    }
    public void detail(String id, RepoCallback<ProductDetail> cb) { enqueue(api.productDetail(id), cb); }
    public void card(String id, RepoCallback<ProductCard> cb) { enqueue(api.productCard(id), cb); }
    public void categories(RepoCallback<List<Category>> cb) { enqueue(api.categories(), cb); }
    public void comments(String productId, RepoCallback<List<Comment>> cb) { enqueue(api.comments(productId), cb); }
}
```

- [ ] **Step 4: `CartRepo.java`**

```java
package com.shopnexus.app.data.repo;
import android.content.Context;
import com.shopnexus.app.data.model.CartItem;
import com.shopnexus.app.data.net.ApiClient;
import com.shopnexus.app.data.net.ApiService;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
public class CartRepo extends BaseRepo {
    private final ApiService api;
    public CartRepo(Context ctx) { api = ApiClient.get(ctx); }
    public void list(RepoCallback<List<CartItem>> cb) { enqueue(api.cart(), cb); }
    public void setQuantity(String skuId, int quantity, RepoCallback<Map<String,Object>> cb) {
        Map<String,Object> b = new HashMap<>(); b.put("sku_id", skuId); b.put("quantity", quantity);
        enqueue(api.updateCart(b), cb);
    }
    public void changeBy(String skuId, int delta, RepoCallback<Map<String,Object>> cb) {
        Map<String,Object> b = new HashMap<>(); b.put("sku_id", skuId); b.put("delta_quantity", delta);
        enqueue(api.updateCart(b), cb);
    }
}
```

- [ ] **Step 5: `OrderRepo.java`**

```java
package com.shopnexus.app.data.repo;
import android.content.Context;
import com.shopnexus.app.data.model.*;
import com.shopnexus.app.data.net.ApiClient;
import com.shopnexus.app.data.net.ApiService;
import java.util.List;
public class OrderRepo extends BaseRepo {
    private final ApiService api;
    public OrderRepo(Context ctx) { api = ApiClient.get(ctx); }
    public void checkout(CheckoutRequest req, RepoCallback<CheckoutResult> cb) { enqueue(api.checkout(req), cb); }
    public void pending(RepoCallback<List<Order>> cb)   { enqueue(api.pendingOrders(), cb); }
    public void completed(RepoCallback<List<Order>> cb) { enqueue(api.completedOrders(), cb); }
    public void cancelled(RepoCallback<List<Order>> cb) { enqueue(api.cancelledOrders(), cb); }
}
```

- [ ] **Step 6: `AccountRepo.java`**

```java
package com.shopnexus.app.data.repo;
import android.content.Context;
import com.shopnexus.app.data.model.Account;
import com.shopnexus.app.data.model.Contact;
import com.shopnexus.app.data.net.ApiClient;
import com.shopnexus.app.data.net.ApiService;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
public class AccountRepo extends BaseRepo {
    private final ApiService api;
    public AccountRepo(Context ctx) { api = ApiClient.get(ctx); }
    public void me(RepoCallback<Account> cb) { enqueue(api.me(), cb); }
    public void contacts(RepoCallback<List<Contact>> cb) { enqueue(api.contacts(), cb); }
    public void addContact(String fullName, String phone, String address, RepoCallback<Contact> cb) {
        Map<String,Object> b = new HashMap<>();
        b.put("full_name", fullName); b.put("phone", phone); b.put("address", address);
        enqueue(api.addContact(b), cb);
    }
}
```

- [ ] **Step 7: Commit**

```bash
git add app/src/main/java/com/shopnexus/app/data/repo
git commit -m "add repository layer over ApiService"
```

---

## Phase 2 — Auth + app bootstrap

### Task 2.1: LoginActivity

**Files:**
- Create: `app/src/main/res/layout/activity_login.xml`
- Create: `app/src/main/java/com/shopnexus/app/ui/auth/LoginActivity.java`
- Modify: `AndroidManifest.xml` (register MainActivity + RegisterActivity once they exist)

- [ ] **Step 1: `activity_login.xml`** (id field, password field, login button, link to register)

```xml
<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:orientation="vertical" android:padding="24dp"
    android:layout_width="match_parent" android:layout_height="match_parent"
    android:gravity="center_vertical">
    <TextView android:text="ShopNexus" android:textSize="28sp" android:textStyle="bold"
        android:gravity="center" android:layout_marginBottom="32dp"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <EditText android:id="@+id/etId" android:hint="Email / Username / SĐT"
        android:inputType="text" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <EditText android:id="@+id/etPassword" android:hint="Mật khẩu"
        android:inputType="textPassword" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <Button android:id="@+id/btnLogin" android:text="Đăng nhập"
        android:layout_marginTop="16dp" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <TextView android:id="@+id/tvToRegister" android:text="Chưa có tài khoản? Đăng ký"
        android:gravity="center" android:layout_marginTop="16dp" android:padding="8dp"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <ProgressBar android:id="@+id/progress" android:visibility="gone"
        style="?android:attr/progressBarStyle"
        android:layout_gravity="center" android:layout_width="wrap_content" android:layout_height="wrap_content"/>
</LinearLayout>
```

- [ ] **Step 2: `LoginActivity.java`** (bootstrap: if already logged in, skip to MainActivity)

```java
package com.shopnexus.app.ui.auth;
import android.content.Intent;
import android.os.Bundle;
import android.view.View;
import android.widget.*;
import androidx.appcompat.app.AppCompatActivity;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.AuthCred;
import com.shopnexus.app.data.net.ApiException;
import com.shopnexus.app.data.repo.AuthRepo;
import com.shopnexus.app.data.repo.RepoCallback;
import com.shopnexus.app.ui.main.MainActivity;
import com.shopnexus.app.util.TokenStore;
public class LoginActivity extends AppCompatActivity {
    private AuthRepo auth;
    @Override protected void onCreate(Bundle s) {
        super.onCreate(s);
        if (new TokenStore(this).isLoggedIn()) { goMain(); return; }
        setContentView(R.layout.activity_login);
        auth = new AuthRepo(this);
        EditText etId = findViewById(R.id.etId), etPw = findViewById(R.id.etPassword);
        Button btn = findViewById(R.id.btnLogin);
        ProgressBar progress = findViewById(R.id.progress);
        findViewById(R.id.tvToRegister).setOnClickListener(v ->
            startActivity(new Intent(this, RegisterActivity.class)));
        btn.setOnClickListener(v -> {
            String id = etId.getText().toString().trim(), pw = etPw.getText().toString();
            if (id.isEmpty() || pw.isEmpty()) { toast("Nhập đủ thông tin"); return; }
            progress.setVisibility(View.VISIBLE); btn.setEnabled(false);
            auth.login(id, pw, new RepoCallback<AuthCred>() {
                @Override public void onSuccess(AuthCred c) { goMain(); }
                @Override public void onError(ApiException e) {
                    progress.setVisibility(View.GONE); btn.setEnabled(true); toast(e.getMessage());
                }
            });
        });
    }
    private void goMain() {
        startActivity(new Intent(this, MainActivity.class)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TASK));
        finish();
    }
    private void toast(String m) { Toast.makeText(this, m, Toast.LENGTH_SHORT).show(); }
}
```

- [ ] **Step 3: Build + manual smoke** (after Task 2.2 + 3.1 exist, or temporarily stub MainActivity)

Run: `./gradlew installDebug` then open app.
Expected: login screen shows; wrong creds → toast "Account not found"; valid creds → navigates to MainActivity.

- [ ] **Step 4: Commit**

```bash
git add app/src/main/res/layout/activity_login.xml app/src/main/java/com/shopnexus/app/ui/auth/LoginActivity.java
git commit -m "add login screen"
```

### Task 2.2: RegisterActivity

**Files:**
- Create: `app/src/main/res/layout/activity_register.xml`
- Create: `app/src/main/java/com/shopnexus/app/ui/auth/RegisterActivity.java`
- Modify: `AndroidManifest.xml` (add `<activity android:name=".ui.auth.RegisterActivity"/>`)

- [ ] **Step 1: `activity_register.xml`** (username, email, phone, password, country default "VN", register button)

```xml
<?xml version="1.0" encoding="utf-8"?>
<ScrollView xmlns:android="http://schemas.android.com/apk/res/android"
    android:layout_width="match_parent" android:layout_height="match_parent">
<LinearLayout android:orientation="vertical" android:padding="24dp"
    android:layout_width="match_parent" android:layout_height="wrap_content">
    <TextView android:text="Đăng ký" android:textSize="24sp" android:textStyle="bold"
        android:layout_marginBottom="16dp" android:layout_width="wrap_content" android:layout_height="wrap_content"/>
    <EditText android:id="@+id/etUsername" android:hint="Username (tùy chọn)" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <EditText android:id="@+id/etEmail" android:hint="Email (tùy chọn)" android:inputType="textEmailAddress" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <EditText android:id="@+id/etPhone" android:hint="Số điện thoại (tùy chọn)" android:inputType="phone" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <EditText android:id="@+id/etPassword" android:hint="Mật khẩu" android:inputType="textPassword" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <EditText android:id="@+id/etCountry" android:hint="Quốc gia" android:text="VN" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <Button android:id="@+id/btnRegister" android:text="Tạo tài khoản" android:layout_marginTop="16dp" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <ProgressBar android:id="@+id/progress" android:visibility="gone" style="?android:attr/progressBarStyle" android:layout_gravity="center" android:layout_width="wrap_content" android:layout_height="wrap_content"/>
</LinearLayout>
</ScrollView>
```

- [ ] **Step 2: `RegisterActivity.java`** (require ≥1 of username/email/phone)

```java
package com.shopnexus.app.ui.auth;
import android.content.Intent;
import android.os.Bundle;
import android.view.View;
import android.widget.*;
import androidx.appcompat.app.AppCompatActivity;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.AuthCred;
import com.shopnexus.app.data.net.ApiException;
import com.shopnexus.app.data.repo.AuthRepo;
import com.shopnexus.app.data.repo.RepoCallback;
import com.shopnexus.app.ui.main.MainActivity;
public class RegisterActivity extends AppCompatActivity {
    @Override protected void onCreate(Bundle s) {
        super.onCreate(s);
        setContentView(R.layout.activity_register);
        AuthRepo auth = new AuthRepo(this);
        EditText u = findViewById(R.id.etUsername), e = findViewById(R.id.etEmail),
                 p = findViewById(R.id.etPhone), pw = findViewById(R.id.etPassword), c = findViewById(R.id.etCountry);
        Button btn = findViewById(R.id.btnRegister); ProgressBar pr = findViewById(R.id.progress);
        btn.setOnClickListener(v -> {
            String un = u.getText().toString().trim(), em = e.getText().toString().trim(),
                   ph = p.getText().toString().trim(), pass = pw.getText().toString(), co = c.getText().toString().trim();
            if (un.isEmpty() && em.isEmpty() && ph.isEmpty()) { toast("Cần ít nhất 1: username / email / SĐT"); return; }
            if (pass.isEmpty() || co.isEmpty()) { toast("Nhập mật khẩu và quốc gia"); return; }
            pr.setVisibility(View.VISIBLE); btn.setEnabled(false);
            auth.register(un, em, ph, pass, co, new RepoCallback<AuthCred>() {
                @Override public void onSuccess(AuthCred cred) {
                    startActivity(new Intent(RegisterActivity.this, MainActivity.class)
                        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TASK));
                    finish();
                }
                @Override public void onError(ApiException ex) {
                    pr.setVisibility(View.GONE); btn.setEnabled(true); toast(ex.getMessage());
                }
            });
        });
    }
    private void toast(String m){ Toast.makeText(this, m, Toast.LENGTH_SHORT).show(); }
}
```

- [ ] **Step 3: Build + manual smoke**

Run: `./gradlew installDebug`
Expected: register with a unique email succeeds and lands on MainActivity; empty identifiers → toast.

- [ ] **Step 4: Commit**

```bash
git add app/src/main/res/layout/activity_register.xml app/src/main/java/com/shopnexus/app/ui/auth/RegisterActivity.java app/src/main/AndroidManifest.xml
git commit -m "add register screen"
```

---

## Phase 3 — MainActivity host + Home (catalog browse/search)

### Task 3.1: MainActivity + BottomNavigation + 4 fragment stubs

**Files:**
- Create: `app/src/main/res/menu/bottom_nav.xml`
- Create: `app/src/main/res/layout/activity_main.xml`
- Create: `app/src/main/java/com/shopnexus/app/ui/main/MainActivity.java`
- Create stubs: `HomeFragment.java`, `CartFragment.java`, `OrdersFragment.java`, `ProfileFragment.java` (each inflates a placeholder TextView; filled in later tasks)
- Modify: `AndroidManifest.xml` (add MainActivity)

- [ ] **Step 1: `res/menu/bottom_nav.xml`**

```xml
<menu xmlns:android="http://schemas.android.com/apk/res/android">
    <item android:id="@+id/nav_home" android:title="Trang chủ" android:icon="@android:drawable/ic_menu_view"/>
    <item android:id="@+id/nav_cart" android:title="Giỏ hàng" android:icon="@android:drawable/ic_menu_agenda"/>
    <item android:id="@+id/nav_orders" android:title="Đơn hàng" android:icon="@android:drawable/ic_menu_recent_history"/>
    <item android:id="@+id/nav_profile" android:title="Tài khoản" android:icon="@android:drawable/ic_menu_myplaces"/>
</menu>
```

- [ ] **Step 2: `activity_main.xml`** (FrameLayout container + BottomNavigationView)

```xml
<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:orientation="vertical" android:layout_width="match_parent" android:layout_height="match_parent">
    <FrameLayout android:id="@+id/container" android:layout_width="match_parent"
        android:layout_height="0dp" android:layout_weight="1"/>
    <com.google.android.material.bottomnavigation.BottomNavigationView
        android:id="@+id/bottomNav" app:menu="@menu/bottom_nav"
        xmlns:app="http://schemas.android.com/apk/res-auto"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
</LinearLayout>
```

- [ ] **Step 3: `MainActivity.java`** (swap fragments)

```java
package com.shopnexus.app.ui.main;
import android.os.Bundle;
import androidx.appcompat.app.AppCompatActivity;
import androidx.fragment.app.Fragment;
import com.google.android.material.bottomnavigation.BottomNavigationView;
import com.shopnexus.app.R;
public class MainActivity extends AppCompatActivity {
    @Override protected void onCreate(Bundle s) {
        super.onCreate(s);
        setContentView(R.layout.activity_main);
        BottomNavigationView nav = findViewById(R.id.bottomNav);
        nav.setOnItemSelectedListener(item -> { show(fragmentFor(item.getItemId())); return true; });
        if (s == null) show(new HomeFragment());
    }
    private Fragment fragmentFor(int id) {
        if (id == R.id.nav_cart) return new CartFragment();
        if (id == R.id.nav_orders) return new OrdersFragment();
        if (id == R.id.nav_profile) return new ProfileFragment();
        return new HomeFragment();
    }
    private void show(Fragment f) {
        getSupportFragmentManager().beginTransaction().replace(R.id.container, f).commit();
    }
}
```

- [ ] **Step 4: Four stub fragments** (placeholder; replaced in later tasks). Example:

```java
package com.shopnexus.app.ui.main;
import android.os.Bundle; import android.view.*; import android.widget.TextView;
import androidx.annotation.*; import androidx.fragment.app.Fragment;
public class CartFragment extends Fragment {
    @Nullable @Override public View onCreateView(@NonNull LayoutInflater i, @Nullable ViewGroup c, @Nullable Bundle s) {
        TextView t = new TextView(getContext()); t.setText("Giỏ hàng"); return t;
    }
}
```
(Create `OrdersFragment` and `ProfileFragment` identically with their own labels. `HomeFragment` is built in Task 3.2.)

- [ ] **Step 5: Build + manual smoke**

Run: `./gradlew installDebug`
Expected: after login, bottom nav with 4 tabs; tapping swaps placeholder screens.

- [ ] **Step 6: Commit**

```bash
git add app/src/main/res/menu app/src/main/res/layout/activity_main.xml app/src/main/java/com/shopnexus/app/ui/main app/src/main/AndroidManifest.xml
git commit -m "add main activity with bottom navigation"
```

### Task 3.2: HomeFragment — recommended grid + search + infinite scroll

**Files:**
- Create: `app/src/main/res/layout/fragment_home.xml`
- Create: `app/src/main/res/layout/item_product_card.xml`
- Create: `app/src/main/java/com/shopnexus/app/ui/adapter/ProductCardAdapter.java`
- Modify: `app/src/main/java/com/shopnexus/app/ui/main/HomeFragment.java`

- [ ] **Step 1: `item_product_card.xml`** (image, name, price)

```xml
<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:orientation="vertical" android:padding="6dp"
    android:layout_width="match_parent" android:layout_height="wrap_content">
    <ImageView android:id="@+id/img" android:scaleType="centerCrop"
        android:layout_width="match_parent" android:layout_height="160dp"
        android:background="#EEEEEE"/>
    <TextView android:id="@+id/name" android:maxLines="2" android:ellipsize="end"
        android:textSize="13sp" android:layout_marginTop="4dp"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <TextView android:id="@+id/price" android:textColor="#E53935" android:textStyle="bold"
        android:textSize="14sp" android:layout_width="match_parent" android:layout_height="wrap_content"/>
</LinearLayout>
```

- [ ] **Step 2: `ProductCardAdapter.java`** (Glide image, click callback)

```java
package com.shopnexus.app.ui.adapter;
import android.view.*; import android.widget.*;
import androidx.annotation.*; import androidx.recyclerview.widget.RecyclerView;
import com.bumptech.glide.Glide;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.ProductCard;
import com.shopnexus.app.util.Money;
import java.util.*;
public class ProductCardAdapter extends RecyclerView.Adapter<ProductCardAdapter.VH> {
    public interface OnClick { void onClick(ProductCard p); }
    private final List<ProductCard> items = new ArrayList<>();
    private final OnClick onClick;
    public ProductCardAdapter(OnClick onClick) { this.onClick = onClick; }
    public void submit(List<ProductCard> data) { items.clear(); items.addAll(data); notifyDataSetChanged(); }
    public void addAll(List<ProductCard> data) { int s = items.size(); items.addAll(data); notifyItemRangeInserted(s, data.size()); }
    public int size() { return items.size(); }
    @NonNull @Override public VH onCreateViewHolder(@NonNull ViewGroup p, int t) {
        return new VH(LayoutInflater.from(p.getContext()).inflate(R.layout.item_product_card, p, false));
    }
    @Override public void onBindViewHolder(@NonNull VH h, int pos) {
        ProductCard c = items.get(pos);
        h.name.setText(c.name);
        h.price.setText(Money.format(c.price, c.currency));
        String url = (c.resources != null && !c.resources.isEmpty()) ? c.resources.get(0).url : null;
        Glide.with(h.img).load(url).into(h.img);
        h.itemView.setOnClickListener(v -> onClick.onClick(c));
    }
    @Override public int getItemCount() { return items.size(); }
    static class VH extends RecyclerView.ViewHolder {
        ImageView img; TextView name, price;
        VH(View v){ super(v); img=v.findViewById(R.id.img); name=v.findViewById(R.id.name); price=v.findViewById(R.id.price); }
    }
}
```

- [ ] **Step 3: `fragment_home.xml`** (search EditText + SwipeRefresh + RecyclerView)

```xml
<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:orientation="vertical" android:layout_width="match_parent" android:layout_height="match_parent">
    <EditText android:id="@+id/etSearch" android:hint="Tìm kiếm sản phẩm..."
        android:imeOptions="actionSearch" android:inputType="text" android:padding="12dp"
        android:layout_margin="8dp" android:background="#F0F0F0"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <androidx.swiperefreshlayout.widget.SwipeRefreshLayout
        android:id="@+id/swipe" android:layout_width="match_parent" android:layout_height="match_parent">
        <androidx.recyclerview.widget.RecyclerView android:id="@+id/recycler"
            android:layout_width="match_parent" android:layout_height="match_parent"/>
    </androidx.swiperefreshlayout.widget.SwipeRefreshLayout>
</LinearLayout>
```

- [ ] **Step 4: `HomeFragment.java`** (recommended on load; search on IME action; paginate on scroll)

```java
package com.shopnexus.app.ui.main;
import android.content.Intent; import android.os.Bundle; import android.view.*;
import android.view.inputmethod.EditorInfo; import android.widget.*;
import androidx.annotation.*; import androidx.fragment.app.Fragment;
import androidx.recyclerview.widget.*;
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.ProductCard;
import com.shopnexus.app.data.net.ApiException;
import com.shopnexus.app.data.repo.CatalogRepo;
import com.shopnexus.app.data.repo.RepoCallback;
import com.shopnexus.app.ui.adapter.ProductCardAdapter;
import com.shopnexus.app.ui.catalog.ProductDetailActivity;
import java.util.List;
public class HomeFragment extends Fragment {
    private CatalogRepo repo; private ProductCardAdapter adapter; private SwipeRefreshLayout swipe;
    private String query = ""; private int page = 1; private boolean loading = false, end = false;
    @Nullable @Override public View onCreateView(@NonNull LayoutInflater inf, @Nullable ViewGroup c, @Nullable Bundle s) {
        return inf.inflate(R.layout.fragment_home, c, false);
    }
    @Override public void onViewCreated(@NonNull View v, @Nullable Bundle s) {
        repo = new CatalogRepo(requireContext());
        adapter = new ProductCardAdapter(this::openDetail);
        RecyclerView rv = v.findViewById(R.id.recycler);
        GridLayoutManager glm = new GridLayoutManager(getContext(), 2);
        rv.setLayoutManager(glm); rv.setAdapter(adapter);
        swipe = v.findViewById(R.id.swipe);
        swipe.setOnRefreshListener(this::reload);
        EditText etSearch = v.findViewById(R.id.etSearch);
        etSearch.setOnEditorActionListener((tv, actionId, e) -> {
            if (actionId == EditorInfo.IME_ACTION_SEARCH) { query = tv.getText().toString().trim(); reload(); return true; }
            return false;
        });
        rv.addOnScrollListener(new RecyclerView.OnScrollListener() {
            @Override public void onScrolled(@NonNull RecyclerView r, int dx, int dy) {
                if (loading || end || dy <= 0) return;
                int last = glm.findLastVisibleItemPosition();
                if (last >= adapter.size() - 4) loadPage();
            }
        });
        reload();
    }
    private void reload() { page = 1; end = false; adapter.submit(java.util.Collections.emptyList()); loadFirst(); }
    private void loadFirst() {
        loading = true; swipe.setRefreshing(true);
        RepoCallback<List<ProductCard>> cb = new RepoCallback<List<ProductCard>>() {
            @Override public void onSuccess(List<ProductCard> data) {
                loading = false; swipe.setRefreshing(false);
                adapter.submit(data); if (data.isEmpty()) end = true;
            }
            @Override public void onError(ApiException e) {
                loading = false; swipe.setRefreshing(false); toast(e.getMessage());
            }
        };
        if (query.isEmpty()) repo.recommended(24, cb); else repo.search(query, page, 24, cb);
    }
    private void loadPage() {
        if (query.isEmpty()) return; // recommended is single-shot
        loading = true; page++;
        repo.search(query, page, 24, new RepoCallback<List<ProductCard>>() {
            @Override public void onSuccess(List<ProductCard> data) {
                loading = false; if (data.isEmpty()) end = true; else adapter.addAll(data);
            }
            @Override public void onError(ApiException e) { loading = false; page--; toast(e.getMessage()); }
        });
    }
    private void openDetail(ProductCard p) {
        startActivity(new Intent(getContext(), ProductDetailActivity.class).putExtra("product_id", p.id));
    }
    private void toast(String m){ if (getContext()!=null) Toast.makeText(getContext(), m, Toast.LENGTH_SHORT).show(); }
}
```

- [ ] **Step 5: Build + manual smoke**

Run: `./gradlew installDebug`
Expected: Home shows a 2-column grid of recommended products with images + prices; typing a search term + Enter reloads; scrolling search results loads more.

- [ ] **Step 6: Commit**

```bash
git add app/src/main/res/layout/fragment_home.xml app/src/main/res/layout/item_product_card.xml app/src/main/java/com/shopnexus/app/ui/adapter/ProductCardAdapter.java app/src/main/java/com/shopnexus/app/ui/main/HomeFragment.java
git commit -m "add home feed with search and infinite scroll"
```

---

## Phase 4 — Product detail

### Task 4.1: ProductDetailActivity (gallery + SKU select + add to cart)

**Files:**
- Create: `app/src/main/res/layout/activity_product_detail.xml`
- Create: `app/src/main/res/layout/item_gallery.xml`
- Create: `app/src/main/java/com/shopnexus/app/ui/adapter/GalleryAdapter.java`
- Create: `app/src/main/java/com/shopnexus/app/ui/catalog/ProductDetailActivity.java`
- Modify: `AndroidManifest.xml` (add ProductDetailActivity)

- [ ] **Step 1: `item_gallery.xml`** (full-width ImageView)

```xml
<?xml version="1.0" encoding="utf-8"?>
<ImageView xmlns:android="http://schemas.android.com/apk/res/android"
    android:id="@+id/img" android:scaleType="fitCenter"
    android:layout_width="match_parent" android:layout_height="match_parent"/>
```

- [ ] **Step 2: `GalleryAdapter.java`** (ViewPager2 adapter over resource URLs)

```java
package com.shopnexus.app.ui.adapter;
import android.view.*; import android.widget.ImageView;
import androidx.annotation.*; import androidx.recyclerview.widget.RecyclerView;
import com.bumptech.glide.Glide;
import com.shopnexus.app.R;
import java.util.*;
public class GalleryAdapter extends RecyclerView.Adapter<GalleryAdapter.VH> {
    private final List<String> urls = new ArrayList<>();
    public void submit(List<String> u){ urls.clear(); urls.addAll(u); notifyDataSetChanged(); }
    @NonNull @Override public VH onCreateViewHolder(@NonNull ViewGroup p, int t){
        return new VH(LayoutInflater.from(p.getContext()).inflate(R.layout.item_gallery, p, false));
    }
    @Override public void onBindViewHolder(@NonNull VH h, int pos){ Glide.with(h.img).load(urls.get(pos)).into(h.img); }
    @Override public int getItemCount(){ return urls.size(); }
    static class VH extends RecyclerView.ViewHolder { ImageView img; VH(View v){ super(v); img=v.findViewById(R.id.img);} }
}
```

- [ ] **Step 3: `activity_product_detail.xml`** (ViewPager2 gallery, name, price, SKU radio group container, add-to-cart button)

```xml
<?xml version="1.0" encoding="utf-8"?>
<ScrollView xmlns:android="http://schemas.android.com/apk/res/android"
    android:layout_width="match_parent" android:layout_height="match_parent">
<LinearLayout android:orientation="vertical" android:layout_width="match_parent" android:layout_height="wrap_content">
    <androidx.viewpager2.widget.ViewPager2 android:id="@+id/pager"
        android:layout_width="match_parent" android:layout_height="300dp"/>
    <TextView android:id="@+id/name" android:textSize="18sp" android:textStyle="bold"
        android:padding="12dp" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <TextView android:id="@+id/price" android:textColor="#E53935" android:textSize="20sp" android:textStyle="bold"
        android:paddingHorizontal="12dp" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <TextView android:text="Phân loại" android:textStyle="bold" android:padding="12dp"
        android:layout_width="wrap_content" android:layout_height="wrap_content"/>
    <RadioGroup android:id="@+id/skuGroup" android:paddingHorizontal="12dp"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <TextView android:id="@+id/desc" android:padding="12dp" android:textColor="#555555"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <Button android:id="@+id/btnAddCart" android:text="Thêm vào giỏ" android:layout_margin="12dp"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
</LinearLayout>
</ScrollView>
```

- [ ] **Step 4: `ProductDetailActivity.java`** (load detail, render SKUs as radios, add selected SKU to cart)

```java
package com.shopnexus.app.ui.catalog;
import android.os.Bundle; import android.view.View; import android.widget.*;
import androidx.appcompat.app.AppCompatActivity;
import androidx.viewpager2.widget.ViewPager2;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.*;
import com.shopnexus.app.data.net.ApiException;
import com.shopnexus.app.data.repo.*;
import com.shopnexus.app.ui.adapter.GalleryAdapter;
import com.shopnexus.app.util.Money;
import java.util.*;
public class ProductDetailActivity extends AppCompatActivity {
    private ProductDetail detail; private Sku selected;
    @Override protected void onCreate(Bundle s) {
        super.onCreate(s);
        setContentView(R.layout.activity_product_detail);
        String id = getIntent().getStringExtra("product_id");
        CatalogRepo repo = new CatalogRepo(this);
        ViewPager2 pager = findViewById(R.id.pager);
        GalleryAdapter gallery = new GalleryAdapter(); pager.setAdapter(gallery);
        TextView name = findViewById(R.id.name), price = findViewById(R.id.price), desc = findViewById(R.id.desc);
        RadioGroup group = findViewById(R.id.skuGroup);
        Button add = findViewById(R.id.btnAddCart);
        repo.detail(id, new RepoCallback<ProductDetail>() {
            @Override public void onSuccess(ProductDetail d) {
                detail = d; name.setText(d.name); desc.setText(d.description);
                List<String> urls = new ArrayList<>();
                if (d.resources != null) for (Resource r : d.resources) urls.add(r.url);
                gallery.submit(urls);
                if (d.skus != null) {
                    for (int i = 0; i < d.skus.size(); i++) {
                        Sku sku = d.skus.get(i);
                        RadioButton rb = new RadioButton(ProductDetailActivity.this);
                        rb.setId(i); rb.setText(skuLabel(sku));
                        group.addView(rb);
                    }
                    group.setOnCheckedChangeListener((g, checked) -> {
                        selected = d.skus.get(checked);
                        price.setText(Money.format(selected.price, d.currency));
                    });
                    if (!d.skus.isEmpty()) group.check(0);
                }
            }
            @Override public void onError(ApiException e) { toast(e.getMessage()); finish(); }
        });
        CartRepo cart = new CartRepo(this);
        add.setOnClickListener(v -> {
            if (selected == null) { toast("Chọn phân loại"); return; }
            add.setEnabled(false);
            cart.setQuantity(selected.id, 1, new RepoCallback<Map<String,Object>>() {
                @Override public void onSuccess(Map<String,Object> r){ add.setEnabled(true); toast("Đã thêm vào giỏ"); }
                @Override public void onError(ApiException e){ add.setEnabled(true); toast(e.getMessage()); }
            });
        });
    }
    private String skuLabel(Sku sku) {
        StringBuilder sb = new StringBuilder();
        if (sku.attributes != null) for (Attribute a : sku.attributes) sb.append(a.value).append(" ");
        sb.append("· kho ").append(sku.stock);
        return sb.toString().trim();
    }
    private void toast(String m){ Toast.makeText(this, m, Toast.LENGTH_SHORT).show(); }
}
```

- [ ] **Step 5: Build + manual smoke**

Run: `./gradlew installDebug`
Expected: tap a product → detail with swipeable gallery, SKU radios, price updates on SKU change, "Thêm vào giỏ" toasts success (requires logged-in token).

- [ ] **Step 6: Commit**

```bash
git add app/src/main/res/layout/activity_product_detail.xml app/src/main/res/layout/item_gallery.xml app/src/main/java/com/shopnexus/app/ui/adapter/GalleryAdapter.java app/src/main/java/com/shopnexus/app/ui/catalog/ProductDetailActivity.java app/src/main/AndroidManifest.xml
git commit -m "add product detail with gallery and add-to-cart"
```

---

## Phase 5 — Cart

### Task 5.1: CartFragment + CartAdapter

**Files:**
- Create: `app/src/main/res/layout/fragment_cart.xml`
- Create: `app/src/main/res/layout/item_cart.xml`
- Create: `app/src/main/java/com/shopnexus/app/ui/adapter/CartAdapter.java`
- Replace stub: `app/src/main/java/com/shopnexus/app/ui/main/CartFragment.java`

- [ ] **Step 1: `item_cart.xml`** (name, qty −/+ , line price)

```xml
<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:orientation="horizontal" android:padding="12dp" android:gravity="center_vertical"
    android:layout_width="match_parent" android:layout_height="wrap_content">
    <LinearLayout android:orientation="vertical" android:layout_weight="1"
        android:layout_width="0dp" android:layout_height="wrap_content">
        <TextView android:id="@+id/name" android:maxLines="2" android:ellipsize="end"
            android:layout_width="match_parent" android:layout_height="wrap_content"/>
        <TextView android:id="@+id/price" android:textColor="#E53935"
            android:layout_width="match_parent" android:layout_height="wrap_content"/>
    </LinearLayout>
    <Button android:id="@+id/btnMinus" android:text="−" android:minWidth="40dp"
        android:layout_width="wrap_content" android:layout_height="wrap_content"/>
    <TextView android:id="@+id/qty" android:paddingHorizontal="12dp"
        android:layout_width="wrap_content" android:layout_height="wrap_content"/>
    <Button android:id="@+id/btnPlus" android:text="+" android:minWidth="40dp"
        android:layout_width="wrap_content" android:layout_height="wrap_content"/>
</LinearLayout>
```

- [ ] **Step 2: `CartAdapter.java`** (qty change callback)

```java
package com.shopnexus.app.ui.adapter;
import android.view.*; import android.widget.*;
import androidx.annotation.*; import androidx.recyclerview.widget.RecyclerView;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.CartItem;
import com.shopnexus.app.util.Money;
import java.util.*;
public class CartAdapter extends RecyclerView.Adapter<CartAdapter.VH> {
    public interface OnQty { void change(CartItem item, int delta); }
    private final List<CartItem> items = new ArrayList<>();
    private final OnQty onQty;
    public CartAdapter(OnQty onQty){ this.onQty = onQty; }
    public void submit(List<CartItem> d){ items.clear(); items.addAll(d); notifyDataSetChanged(); }
    public List<CartItem> items(){ return items; }
    @NonNull @Override public VH onCreateViewHolder(@NonNull ViewGroup p, int t){
        return new VH(LayoutInflater.from(p.getContext()).inflate(R.layout.item_cart, p, false));
    }
    @Override public void onBindViewHolder(@NonNull VH h, int pos){
        CartItem c = items.get(pos);
        h.name.setText(c.spu_id);   // name not in cart payload; show id (detail fetch is out of MVP scope)
        h.qty.setText(String.valueOf(c.quantity));
        h.price.setText(c.currency != null ? c.currency : "");
        h.minus.setOnClickListener(v -> { if (c.quantity > 1) onQty.change(c, -1); });
        h.plus.setOnClickListener(v -> onQty.change(c, +1));
    }
    @Override public int getItemCount(){ return items.size(); }
    static class VH extends RecyclerView.ViewHolder {
        TextView name, qty, price; Button minus, plus;
        VH(View v){ super(v); name=v.findViewById(R.id.name); qty=v.findViewById(R.id.qty);
            price=v.findViewById(R.id.price); minus=v.findViewById(R.id.btnMinus); plus=v.findViewById(R.id.btnPlus); }
    }
}
```

- [ ] **Step 3: `fragment_cart.xml`** (RecyclerView + total bar + checkout button)

```xml
<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:orientation="vertical" android:layout_width="match_parent" android:layout_height="match_parent">
    <androidx.recyclerview.widget.RecyclerView android:id="@+id/recycler"
        android:layout_width="match_parent" android:layout_height="0dp" android:layout_weight="1"/>
    <Button android:id="@+id/btnCheckout" android:text="Thanh toán" android:layout_margin="12dp"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
</LinearLayout>
```

- [ ] **Step 4: `CartFragment.java`** (load cart, qty change via `changeBy`, refresh, go to checkout)

```java
package com.shopnexus.app.ui.main;
import android.content.Intent; import android.os.Bundle; import android.view.*;
import android.widget.Toast;
import androidx.annotation.*; import androidx.fragment.app.Fragment;
import androidx.recyclerview.widget.*;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.CartItem;
import com.shopnexus.app.data.net.ApiException;
import com.shopnexus.app.data.repo.*;
import com.shopnexus.app.ui.adapter.CartAdapter;
import com.shopnexus.app.ui.checkout.CheckoutActivity;
import java.util.*;
public class CartFragment extends Fragment {
    private CartRepo repo; private CartAdapter adapter;
    @Nullable @Override public View onCreateView(@NonNull LayoutInflater i, @Nullable ViewGroup c, @Nullable Bundle s){
        return i.inflate(R.layout.fragment_cart, c, false);
    }
    @Override public void onViewCreated(@NonNull View v, @Nullable Bundle s){
        repo = new CartRepo(requireContext());
        adapter = new CartAdapter((item, delta) -> repo.changeBy(item.sku_id, delta, new RepoCallback<Map<String,Object>>() {
            @Override public void onSuccess(Map<String,Object> r){ load(); }
            @Override public void onError(ApiException e){ toast(e.getMessage()); }
        }));
        RecyclerView rv = v.findViewById(R.id.recycler);
        rv.setLayoutManager(new LinearLayoutManager(getContext())); rv.setAdapter(adapter);
        v.findViewById(R.id.btnCheckout).setOnClickListener(x -> {
            if (adapter.items().isEmpty()) { toast("Giỏ hàng trống"); return; }
            startActivity(new Intent(getContext(), CheckoutActivity.class));
        });
        load();
    }
    @Override public void onResume(){ super.onResume(); if (repo != null) load(); }
    private void load(){
        repo.list(new RepoCallback<List<CartItem>>() {
            @Override public void onSuccess(List<CartItem> data){ adapter.submit(data); }
            @Override public void onError(ApiException e){ toast(e.getMessage()); }
        });
    }
    private void toast(String m){ if (getContext()!=null) Toast.makeText(getContext(), m, Toast.LENGTH_SHORT).show(); }
}
```

- [ ] **Step 5: Build + manual smoke**

Run: `./gradlew installDebug`
Expected: Cart tab lists items added earlier; +/− changes quantity and reloads; "Thanh toán" opens CheckoutActivity.

- [ ] **Step 6: Commit**

```bash
git add app/src/main/res/layout/fragment_cart.xml app/src/main/res/layout/item_cart.xml app/src/main/java/com/shopnexus/app/ui/adapter/CartAdapter.java app/src/main/java/com/shopnexus/app/ui/main/CartFragment.java
git commit -m "add cart screen with quantity controls"
```

---

## Phase 6 — Checkout + payment WebView

### Task 6.1: CheckoutActivity

**Files:**
- Create: `app/src/main/res/layout/activity_checkout.xml`
- Create: `app/src/main/java/com/shopnexus/app/ui/checkout/CheckoutActivity.java`
- Modify: `AndroidManifest.xml` (add CheckoutActivity)

- [ ] **Step 1: `activity_checkout.xml`** (address field, payment option spinner, place-order button)

```xml
<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:orientation="vertical" android:padding="16dp"
    android:layout_width="match_parent" android:layout_height="match_parent">
    <TextView android:text="Địa chỉ giao hàng" android:textStyle="bold"
        android:layout_width="wrap_content" android:layout_height="wrap_content"/>
    <EditText android:id="@+id/etAddress" android:hint="Nhập địa chỉ giao hàng"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <TextView android:text="Phương thức thanh toán" android:textStyle="bold" android:layout_marginTop="16dp"
        android:layout_width="wrap_content" android:layout_height="wrap_content"/>
    <Spinner android:id="@+id/spPayment" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <View android:layout_width="match_parent" android:layout_height="0dp" android:layout_weight="1"/>
    <Button android:id="@+id/btnPlace" android:text="Đặt hàng"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <ProgressBar android:id="@+id/progress" android:visibility="gone" style="?android:attr/progressBarStyle"
        android:layout_gravity="center" android:layout_width="wrap_content" android:layout_height="wrap_content"/>
</LinearLayout>
```

- [ ] **Step 2: `CheckoutActivity.java`** (prefill address from first contact; build CheckoutRequest from current cart with a default transport option; route to payment)

```java
package com.shopnexus.app.ui.checkout;
import android.content.Intent; import android.os.Bundle; import android.view.View; import android.widget.*;
import androidx.appcompat.app.AppCompatActivity;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.*;
import com.shopnexus.app.data.net.ApiException;
import com.shopnexus.app.data.repo.*;
import com.shopnexus.app.ui.main.MainActivity;
import java.util.*;
public class CheckoutActivity extends AppCompatActivity {
    // MVP: backend requires a transport_option per item. "ghtk" is the active provider
    // (see backend transport/webhook/ghtk). Confirm the exact option string at runtime
    // via order/buyer/quote-transport; fall back to this constant.
    private static final String DEFAULT_TRANSPORT = "ghtk";
    private static final String DEFAULT_PAYMENT = "vnpay";

    @Override protected void onCreate(Bundle s) {
        super.onCreate(s);
        setContentView(R.layout.activity_checkout);
        EditText etAddress = findViewById(R.id.etAddress);
        Spinner sp = findViewById(R.id.spPayment);
        Button place = findViewById(R.id.btnPlace);
        ProgressBar progress = findViewById(R.id.progress);
        sp.setAdapter(new ArrayAdapter<>(this, android.R.layout.simple_spinner_dropdown_item,
            new String[]{ "VNPay", "SePay" }));

        AccountRepo account = new AccountRepo(this);
        account.contacts(new RepoCallback<List<Contact>>() {
            @Override public void onSuccess(List<Contact> data){ if (!data.isEmpty() && data.get(0).address != null) etAddress.setText(data.get(0).address); }
            @Override public void onError(ApiException e){ /* address can be typed manually */ }
        });

        CartRepo cartRepo = new CartRepo(this);
        OrderRepo orderRepo = new OrderRepo(this);
        place.setOnClickListener(v -> {
            String address = etAddress.getText().toString().trim();
            if (address.isEmpty()) { toast("Nhập địa chỉ giao hàng"); return; }
            String payment = sp.getSelectedItemPosition() == 1 ? "sepay" : DEFAULT_PAYMENT;
            progress.setVisibility(View.VISIBLE); place.setEnabled(false);
            cartRepo.list(new RepoCallback<List<CartItem>>() {
                @Override public void onSuccess(List<CartItem> cart) {
                    if (cart.isEmpty()) { fail("Giỏ hàng trống"); return; }
                    CheckoutRequest req = new CheckoutRequest();
                    req.address = address; req.payment_option = payment; req.use_wallet = false;
                    req.items = new ArrayList<>();
                    for (CartItem ci : cart) req.items.add(new CheckoutItem(ci.sku_id, ci.quantity, DEFAULT_TRANSPORT, ""));
                    orderRepo.checkout(req, new RepoCallback<CheckoutResult>() {
                        @Override public void onSuccess(CheckoutResult res) {
                            if (res.payment_url != null && !res.payment_url.isEmpty()) {
                                startActivity(new Intent(CheckoutActivity.this, PaymentWebViewActivity.class)
                                    .putExtra("url", res.payment_url));
                                finish();
                            } else { goOrders(); }
                        }
                        @Override public void onError(ApiException e){ fail(e.getMessage()); }
                    });
                }
                @Override public void onError(ApiException e){ fail(e.getMessage()); }
            });
        });
    }
    private void fail(String m){ findViewById(R.id.progress).setVisibility(View.GONE); findViewById(R.id.btnPlace).setEnabled(true); toast(m); }
    private void goOrders(){
        startActivity(new Intent(this, MainActivity.class)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TASK)
            .putExtra("open_tab", "orders"));
        finish();
    }
    private void toast(String m){ Toast.makeText(this, m, Toast.LENGTH_SHORT).show(); }
}
```

- [ ] **Step 3: Build + manual smoke**

Run: `./gradlew installDebug`
Expected: checkout shows address (prefilled if a contact exists) + payment spinner; "Đặt hàng" calls backend. If it returns `validation` about `transport_option`, capture the valid option from `quote-transport` and update `DEFAULT_TRANSPORT` (see risk note in spec §11).

- [ ] **Step 4: Commit**

```bash
git add app/src/main/res/layout/activity_checkout.xml app/src/main/java/com/shopnexus/app/ui/checkout/CheckoutActivity.java app/src/main/AndroidManifest.xml
git commit -m "add checkout screen"
```

### Task 6.2: PaymentWebViewActivity

**Files:**
- Create: `app/src/main/res/layout/activity_payment.xml`
- Create: `app/src/main/java/com/shopnexus/app/ui/checkout/PaymentWebViewActivity.java`
- Modify: `AndroidManifest.xml` (add PaymentWebViewActivity)

- [ ] **Step 1: `activity_payment.xml`** (full-screen WebView + progress)

```xml
<?xml version="1.0" encoding="utf-8"?>
<FrameLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:layout_width="match_parent" android:layout_height="match_parent">
    <WebView android:id="@+id/web" android:layout_width="match_parent" android:layout_height="match_parent"/>
    <ProgressBar android:id="@+id/progress" style="?android:attr/progressBarStyleHorizontal"
        android:layout_width="match_parent" android:layout_height="wrap_content" android:indeterminate="true"/>
</FrameLayout>
```

- [ ] **Step 2: `PaymentWebViewActivity.java`** (load url; detect a return/result URL fragment → finish to Orders)

```java
package com.shopnexus.app.ui.checkout;
import android.annotation.SuppressLint; import android.content.Intent; import android.graphics.Bitmap;
import android.os.Bundle; import android.view.View; import android.webkit.*; import android.widget.ProgressBar;
import androidx.appcompat.app.AppCompatActivity;
import com.shopnexus.app.R;
import com.shopnexus.app.ui.main.MainActivity;
public class PaymentWebViewActivity extends AppCompatActivity {
    @SuppressLint("SetJavaScriptEnabled")
    @Override protected void onCreate(Bundle s) {
        super.onCreate(s);
        setContentView(R.layout.activity_payment);
        String url = getIntent().getStringExtra("url");
        WebView web = findViewById(R.id.web);
        ProgressBar progress = findViewById(R.id.progress);
        web.getSettings().setJavaScriptEnabled(true);
        web.setWebViewClient(new WebViewClient() {
            @Override public void onPageStarted(WebView v, String u, Bitmap f) {
                progress.setVisibility(View.VISIBLE);
                // Gateways redirect to a return URL containing the result. Heuristic:
                // any URL hitting our own backend host after payment is the return leg.
                if (u != null && (u.contains("vnpay_ResponseCode") || u.contains("/payment/")
                        || u.contains("shopnexus.hopto.org"))) {
                    goOrders();
                }
            }
            @Override public void onPageFinished(WebView v, String u) { progress.setVisibility(View.GONE); }
        });
        if (url != null) web.loadUrl(url); else goOrders();
    }
    private void goOrders() {
        startActivity(new Intent(this, MainActivity.class)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TASK)
            .putExtra("open_tab", "orders"));
        finish();
    }
}
```

- [ ] **Step 3: Build + manual smoke**

Run: `./gradlew installDebug`
Expected: after "Đặt hàng" with a gateway, the VNPay/SePay page loads in-app; completing/cancelling returns to the Orders tab. (Confirm the return-URL heuristic against a real transaction; adjust the `contains(...)` checks per §11.)

- [ ] **Step 4: Commit**

```bash
git add app/src/main/res/layout/activity_payment.xml app/src/main/java/com/shopnexus/app/ui/checkout/PaymentWebViewActivity.java app/src/main/AndroidManifest.xml
git commit -m "add payment webview"
```

---

## Phase 7 — Orders

### Task 7.1: OrdersFragment with 3 status tabs + OrderAdapter

**Files:**
- Create: `app/src/main/res/layout/fragment_orders.xml`
- Create: `app/src/main/res/layout/item_order.xml`
- Create: `app/src/main/java/com/shopnexus/app/ui/adapter/OrderAdapter.java`
- Replace stub: `app/src/main/java/com/shopnexus/app/ui/main/OrdersFragment.java`
- Modify: `app/src/main/java/com/shopnexus/app/ui/main/MainActivity.java` (honor `open_tab` extra)

- [ ] **Step 1: `item_order.xml`** (order id, status, total)

```xml
<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:orientation="vertical" android:padding="12dp"
    android:layout_width="match_parent" android:layout_height="wrap_content">
    <TextView android:id="@+id/orderId" android:textStyle="bold"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <TextView android:id="@+id/status" android:textColor="#1976D2"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <TextView android:id="@+id/total" android:textColor="#E53935"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <View android:layout_marginTop="8dp" android:background="#EEEEEE"
        android:layout_width="match_parent" android:layout_height="1dp"/>
</LinearLayout>
```

- [ ] **Step 2: `OrderAdapter.java`**

```java
package com.shopnexus.app.ui.adapter;
import android.view.*; import android.widget.TextView;
import androidx.annotation.*; import androidx.recyclerview.widget.RecyclerView;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.Order;
import com.shopnexus.app.util.Money;
import java.util.*;
public class OrderAdapter extends RecyclerView.Adapter<OrderAdapter.VH> {
    private final List<Order> items = new ArrayList<>();
    public void submit(List<Order> d){ items.clear(); items.addAll(d); notifyDataSetChanged(); }
    @NonNull @Override public VH onCreateViewHolder(@NonNull ViewGroup p, int t){
        return new VH(LayoutInflater.from(p.getContext()).inflate(R.layout.item_order, p, false));
    }
    @Override public void onBindViewHolder(@NonNull VH h, int pos){
        Order o = items.get(pos);
        h.orderId.setText("Đơn #" + o.id);
        h.status.setText(o.status != null ? o.status : "");
        h.total.setText(Money.format(o.total_amount, o.currency != null ? o.currency : "VND"));
    }
    @Override public int getItemCount(){ return items.size(); }
    static class VH extends RecyclerView.ViewHolder {
        TextView orderId, status, total;
        VH(View v){ super(v); orderId=v.findViewById(R.id.orderId); status=v.findViewById(R.id.status); total=v.findViewById(R.id.total); }
    }
}
```

- [ ] **Step 3: `fragment_orders.xml`** (TabLayout + RecyclerView)

```xml
<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    xmlns:app="http://schemas.android.com/apk/res-auto"
    android:orientation="vertical" android:layout_width="match_parent" android:layout_height="match_parent">
    <com.google.android.material.tabs.TabLayout android:id="@+id/tabs"
        app:tabMode="fixed" android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <androidx.recyclerview.widget.RecyclerView android:id="@+id/recycler"
        android:layout_width="match_parent" android:layout_height="match_parent"/>
</LinearLayout>
```

- [ ] **Step 4: `OrdersFragment.java`** (tab 0 pending, 1 completed, 2 cancelled)

```java
package com.shopnexus.app.ui.main;
import android.os.Bundle; import android.view.*; import android.widget.Toast;
import androidx.annotation.*; import androidx.fragment.app.Fragment;
import androidx.recyclerview.widget.*;
import com.google.android.material.tabs.TabLayout;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.Order;
import com.shopnexus.app.data.net.ApiException;
import com.shopnexus.app.data.repo.OrderRepo;
import com.shopnexus.app.data.repo.RepoCallback;
import com.shopnexus.app.ui.adapter.OrderAdapter;
import java.util.List;
public class OrdersFragment extends Fragment {
    private OrderRepo repo; private OrderAdapter adapter; private int tab = 0;
    @Nullable @Override public View onCreateView(@NonNull LayoutInflater i, @Nullable ViewGroup c, @Nullable Bundle s){
        return i.inflate(R.layout.fragment_orders, c, false);
    }
    @Override public void onViewCreated(@NonNull View v, @Nullable Bundle s){
        repo = new OrderRepo(requireContext()); adapter = new OrderAdapter();
        RecyclerView rv = v.findViewById(R.id.recycler);
        rv.setLayoutManager(new LinearLayoutManager(getContext())); rv.setAdapter(adapter);
        TabLayout tabs = v.findViewById(R.id.tabs);
        tabs.addTab(tabs.newTab().setText("Chờ xử lý"));
        tabs.addTab(tabs.newTab().setText("Hoàn thành"));
        tabs.addTab(tabs.newTab().setText("Đã hủy"));
        tabs.addOnTabSelectedListener(new TabLayout.OnTabSelectedListener() {
            @Override public void onTabSelected(TabLayout.Tab t){ tab = t.getPosition(); load(); }
            @Override public void onTabUnselected(TabLayout.Tab t){}
            @Override public void onTabReselected(TabLayout.Tab t){}
        });
        load();
    }
    private void load(){
        RepoCallback<List<Order>> cb = new RepoCallback<List<Order>>() {
            @Override public void onSuccess(List<Order> d){ adapter.submit(d); }
            @Override public void onError(ApiException e){ toast(e.getMessage()); }
        };
        if (tab == 1) repo.completed(cb); else if (tab == 2) repo.cancelled(cb); else repo.pending(cb);
    }
    private void toast(String m){ if (getContext()!=null) Toast.makeText(getContext(), m, Toast.LENGTH_SHORT).show(); }
}
```

- [ ] **Step 5: `MainActivity` honors `open_tab`** — add to `onCreate` after `show(new HomeFragment())`:

```java
        String openTab = getIntent().getStringExtra("open_tab");
        if ("orders".equals(openTab)) nav.setSelectedItemId(R.id.nav_orders);
```

- [ ] **Step 6: Build + manual smoke**

Run: `./gradlew installDebug`
Expected: Orders tab shows 3 sub-tabs; after a successful checkout the new order appears under "Chờ xử lý".

- [ ] **Step 7: Commit**

```bash
git add app/src/main/res/layout/fragment_orders.xml app/src/main/res/layout/item_order.xml app/src/main/java/com/shopnexus/app/ui/adapter/OrderAdapter.java app/src/main/java/com/shopnexus/app/ui/main/OrdersFragment.java app/src/main/java/com/shopnexus/app/ui/main/MainActivity.java
git commit -m "add orders screen with status tabs"
```

---

## Phase 8 — Profile + logout

### Task 8.1: ProfileFragment

**Files:**
- Create: `app/src/main/res/layout/fragment_profile.xml`
- Replace stub: `app/src/main/java/com/shopnexus/app/ui/main/ProfileFragment.java`

- [ ] **Step 1: `fragment_profile.xml`** (name/email, address, logout button)

```xml
<?xml version="1.0" encoding="utf-8"?>
<LinearLayout xmlns:android="http://schemas.android.com/apk/res/android"
    android:orientation="vertical" android:padding="16dp"
    android:layout_width="match_parent" android:layout_height="match_parent">
    <TextView android:id="@+id/tvName" android:textSize="18sp" android:textStyle="bold"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <TextView android:id="@+id/tvEmail" android:textColor="#777777" android:layout_marginTop="4dp"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <TextView android:id="@+id/tvAddress" android:layout_marginTop="12dp"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
    <View android:layout_width="match_parent" android:layout_height="0dp" android:layout_weight="1"/>
    <Button android:id="@+id/btnLogout" android:text="Đăng xuất"
        android:layout_width="match_parent" android:layout_height="wrap_content"/>
</LinearLayout>
```

- [ ] **Step 2: `ProfileFragment.java`** (load `me` + first contact; logout clears token → LoginActivity)

```java
package com.shopnexus.app.ui.main;
import android.content.Intent; import android.os.Bundle; import android.view.*; import android.widget.*;
import androidx.annotation.*; import androidx.fragment.app.Fragment;
import com.shopnexus.app.R;
import com.shopnexus.app.data.model.Account;
import com.shopnexus.app.data.model.Contact;
import com.shopnexus.app.data.net.ApiException;
import com.shopnexus.app.data.repo.AccountRepo;
import com.shopnexus.app.data.repo.RepoCallback;
import com.shopnexus.app.ui.auth.LoginActivity;
import com.shopnexus.app.util.TokenStore;
import java.util.List;
public class ProfileFragment extends Fragment {
    @Nullable @Override public View onCreateView(@NonNull LayoutInflater i, @Nullable ViewGroup c, @Nullable Bundle s){
        return i.inflate(R.layout.fragment_profile, c, false);
    }
    @Override public void onViewCreated(@NonNull View v, @Nullable Bundle s){
        TextView name = v.findViewById(R.id.tvName), email = v.findViewById(R.id.tvEmail), addr = v.findViewById(R.id.tvAddress);
        AccountRepo repo = new AccountRepo(requireContext());
        repo.me(new RepoCallback<Account>() {
            @Override public void onSuccess(Account a){
                name.setText(a.username != null ? a.username : (a.email != null ? a.email : a.id));
                email.setText(a.email != null ? a.email : (a.phone != null ? a.phone : ""));
            }
            @Override public void onError(ApiException e){ name.setText("Tài khoản"); }
        });
        repo.contacts(new RepoCallback<List<Contact>>() {
            @Override public void onSuccess(List<Contact> d){ if (!d.isEmpty()) addr.setText("Địa chỉ: " + d.get(0).address); }
            @Override public void onError(ApiException e){}
        });
        v.findViewById(R.id.btnLogout).setOnClickListener(x -> {
            new TokenStore(requireContext()).clear();
            startActivity(new Intent(getContext(), LoginActivity.class)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TASK));
            requireActivity().finish();
        });
    }
}
```

- [ ] **Step 3: Build + manual smoke**

Run: `./gradlew installDebug`
Expected: Profile shows username/email + address; logout returns to Login and clears the session (relaunch goes to Login, not Main).

- [ ] **Step 4: Commit**

```bash
git add app/src/main/res/layout/fragment_profile.xml app/src/main/java/com/shopnexus/app/ui/main/ProfileFragment.java
git commit -m "add profile screen with logout"
```

---

## Phase 9 — End-to-end smoke + unit test run

### Task 9.1: Full e2e smoke + test suite

- [ ] **Step 1: Run unit tests**

Run: `./gradlew test`
Expected: `MoneyTest`, `ApiResponseTest`, `ProductCardParseTest` all PASS.

- [ ] **Step 2: E2E smoke checklist** (on emulator/device against live API)

1. Launch → Login screen (no session).
2. Register a unique email/password → lands on Home.
3. Home shows recommended grid with images.
4. Search a term → results reload; scroll → more load.
5. Open a product → gallery + SKUs; add to cart → toast success.
6. Cart tab → item present; +/− adjusts quantity.
7. Checkout → enter address → Đặt hàng → VNPay WebView (or order created).
8. Complete/cancel payment → Orders tab → order under "Chờ xử lý".
9. Profile → username/email shown → Đăng xuất → Login. Relaunch → Login (session cleared).

- [ ] **Step 3: Commit any fixes found during smoke**

```bash
git add -A && git commit -m "fix issues from e2e smoke"
```

---

## Open risks to resolve during execution (from spec §11)

1. **Transport option string** — `CheckoutItem.transport_option` is hardcoded `"ghtk"`. Hit `order/buyer/quote-transport` with the cart once and confirm the accepted option value; wire the real value (and, if returned, a per-item selection) into `CheckoutActivity`.
2. **Payment return URL** — `PaymentWebViewActivity` uses a `contains(...)` heuristic to detect the return leg. Validate against one real VNPay/SePay transaction and tighten the match; fallback is polling `order/buyer/orders/:id` for `date_paid`/status.
3. **Cart `sku_id` availability** — qty change (`CartRepo.changeBy`) and checkout (`CheckoutItem.sku_id`) both key on `sku_id`, but the FE `CartItem` type only declares `spu_id`/`quantity`/`currency`. Verify the real `GET order/cart` payload includes `sku_id`. If it only returns `spu_id`, resolve the sku via `product-detail` (or change cart-update to key on `spu_id`) before Phase 5/6 work.
4. **Cart item display name** — the `order/cart` payload carries ids but no product name; MVP shows the id. If a friendlier label is needed, fetch `product-card/:id` per cart row (defer unless required).
5. **No toolchain on the current box** — install Android SDK + JDK 17 (Android Studio) before any `./gradlew` / `installDebug` step.

## Deferred from spec (not in this plan, by MVP scope)

- **Category chips / category browse** on Home — `CatalogRepo.categories()` exists but no chip UI; search covers discovery for MVP.
- **Contact CRUD UI** — `AccountRepo.addContact()` exists and Checkout/Profile read the first contact, but there is no add/edit/delete address screen. Add in a follow-up task if needed.
- All other out-of-scope subsystems listed in spec §10 (chat, refund, seller, analytic, voucher, FCM, Google Sign-In, i18n, dark mode).

---

## Execution

Recommended: subagent-driven, one task per subagent, review between tasks. Note: build/smoke steps require an Android SDK environment; on a box without it, subagents can still write+commit code, but `./gradlew` verification must run where the SDK exists.
