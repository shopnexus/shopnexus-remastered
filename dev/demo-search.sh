#!/usr/bin/env bash
# Walks the smart-search demo queries against a running gateway and prints, for each one, what the
# search understood, the phrases it actually searched, and the page it answered.
#
#   dev/demo-search.sh                  # the demo set, in order
#   dev/demo-search.sh "ao thu unilo"   # one query of your own
#
# Needs the gateway up with a real llm provider (internal/config/config.dev.yml: provider litellm
# and an api_key), because every query costs one model call.
set -euo pipefail

BASE=${BASE:-http://localhost:5000/api/v1}
IDENTIFIER=${IDENTIFIER:-khoakomlem@gmail.com}
PASSWORD=${PASSWORD:-visualc++}
LIMIT=${LIMIT:-5}

TOKEN=$(curl -sS -X POST "$BASE/login" -H 'content-type: application/json' \
  -d "{\"identifier\":\"$IDENTIFIER\",\"password\":\"$PASSWORD\"}" |
  python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["access_token"])')

show() {
  local q=$1
  printf '\n\033[1m%s\033[0m\n' "  gõ:  $q"
  curl -sS -G "$BASE/listings" --data-urlencode "q=$q" --data-urlencode "limit=$LIMIT" \
    -H "authorization: Bearer $TOKEN" |
    python3 -c '
import json,sys
d = json.load(sys.stdin)
if "error" in d:
    print("      lỗi:", d["error"]); raise SystemExit(1)
print("  hiểu:", d.get("understood") or "(không có — rơi về tìm kiếm nền)")
print("  tìm:  " + " · ".join(d.get("probes") or []))
for x in d.get("data", []):
    print("        {:>10,}đ  {}".format(x["price"], x["name"][:56]))
'
}

if [ $# -gt 0 ]; then
  for q in "$@"; do show "$q"; done
  exit 0
fi

# Each line is one capability. Order matters: spelling first, structure last.
show "ao thu unilo"            # sai chính tả + mất dấu
show "dt cu duoi 5tr"          # viết tắt, và ba ràng buộc trong một câu
show "qua tang sinh nhat re"   # mơ hồ, không có tên sản phẩm nào
show "op lung iphone"          # phụ kiện là thứ được tìm, không phải thứ bị hạ
