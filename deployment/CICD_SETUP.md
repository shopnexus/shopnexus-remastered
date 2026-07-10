# CI/CD — Auto redeploy khi push `main`

Luồng: **push `main` → GitHub Actions build Docker image → push lên GHCR → SSH vào VPS pull image + restart riêng service `server`** (Postgres/Redis/Restate/MinIO giữ nguyên, không downtime cả cụm).

File liên quan:

- `.github/workflows/deploy.yml` — pipeline.
- `deployment/docker-compose.prod.yml` — service `server` giờ dùng `image:` từ GHCR (biến `SERVER_IMAGE`).

---

## 1. Chuẩn bị trên VPS (làm 1 lần)

```bash
# a) Cài docker + compose plugin nếu chưa có
#    (bỏ qua nếu đã cài)

# b) Đưa mã nguồn / thư mục deployment lên server, ví dụ:
mkdir -p /opt/shopnexus && cd /opt/shopnexus
# copy docker-compose.prod.yml + file .env vào đây

# c) Tạo file .env cạnh docker-compose.prod.yml (chứa POSTGRES_PASSWORD, REDIS_PASSWORD, ...)
#    docker compose tự đọc .env này.

# d) Khởi động cụm lần đầu (build tại chỗ lần đầu hoặc để CI push image trước rồi pull)
docker compose -f docker-compose.prod.yml up -d
```

## 2. Tạo SSH deploy key cho CI

Trên máy local (hoặc server), tạo 1 cặp key riêng cho CI:

```bash
ssh-keygen -t ed25519 -C "github-actions-deploy" -f deploy_key -N ""
```

- **Public key** (`deploy_key.pub`) → thêm vào `~/.ssh/authorized_keys` của user deploy trên VPS.
- **Private key** (`deploy_key`) → dán vào secret `SSH_KEY` (bước 4).

Nên tạo user riêng (vd `deploy`) thay vì root, và cấp quyền chạy docker (`usermod -aG docker deploy`).

## 3. Tạo GHCR token để VPS pull image (private package)

Vào GitHub → **Settings → Developer settings → Personal access tokens (classic)** → tạo token với scope **`read:packages`**.
→ Dùng cho secret `GHCR_TOKEN`.

> Hoặc để package public (GHCR → package `shopnexus-server` → Package settings → Change visibility → Public) thì **không cần** `GHCR_TOKEN`/`GHCR_USER` và bỏ bước `docker login` trong workflow.

## 4. Khai báo Secrets trên GitHub repo

`Settings → Secrets and variables → Actions → New repository secret`:

| Secret        | Giá trị                                                        |
| ------------- | -------------------------------------------------------------- |
| `SSH_HOST`    | IP / domain VPS, vd `123.45.67.89`                             |
| `SSH_USER`    | user SSH, vd `deploy`                                          |
| `SSH_PORT`    | cổng SSH, thường `22`                                          |
| `SSH_KEY`     | nội dung **private key** `deploy_key` (cả `-----BEGIN...END`)  |
| `DEPLOY_PATH` | thư mục chứa `docker-compose.prod.yml`, vd `/opt/shopnexus`    |
| `GHCR_USER`   | username GitHub của bạn                                        |
| `GHCR_TOKEN`  | PAT `read:packages` ở bước 3                                   |

`GITHUB_TOKEN` (để push image lên GHCR) là sẵn có, không cần khai báo.

## 5. Chạy thử

```bash
git commit --allow-empty -m "test ci" && git push origin main
```

Xem tiến trình ở tab **Actions**. Job `build` push image `ghcr.io/shopnexus/shopnexus-server:sha-<commit>` và `:latest`, job `deploy` SSH vào VPS pull đúng commit đó rồi restart service `server`.

---

## Rollback

Deploy dùng tag pin theo commit (`SERVER_IMAGE=...:sha-<commit>`), nên rollback = SSH vào VPS và chạy lại với SHA cũ:

```bash
cd /opt/shopnexus
SERVER_IMAGE="ghcr.io/shopnexus/shopnexus-server:sha-<commit-cũ>" \
  docker compose -f docker-compose.prod.yml up -d --no-deps server
```

## Ghi chú

- Nếu commit có **migration DB**, cân nhắc chạy `make migrate` trong bước deploy trước khi `up -d server`, hoặc để server tự migrate lúc khởi động.
- Muốn an toàn hơn: đổi trigger sang deploy khi **tạo git tag** (thay `branches: [main]` bằng `tags: ['v*']`) để không phải mọi push đều lên production.
- Muốn có gate test trước khi deploy: thêm 1 job `test` chạy `go test ./...` và cho `build` phụ thuộc (`needs: test`).
