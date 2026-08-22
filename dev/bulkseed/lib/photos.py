#!/usr/bin/env python3
"""Download the product photographs the crawl found, so listings show their own goods.

The catalogue's existing resources are hotlinks into Shopee's CDN, scraped alongside the
original listings. Reusing them for generated rows is the best that can be done without new
images — a per-leaf pool at least puts a kitchen photo on kitchenware — but it is still
somebody else's picture of a different product, and the storefront shows it.

crawl_tiki.py already recorded a thumbnail URL per product name, and that one *is* the photo of
that product. So: fetch it, keep it, and point the listings generated from that name at it. The
URLs are `cache/280x280`; Tiki serves the same object at other sizes from the same path and
750x750 is what a product card wants — 280 is visibly soft, w1200 triples the bytes for a
detail view nothing here renders.

Two steps, because only the second one needs the database:

    ./04_fetch_photos.py                 # download into ./photos/, resumable
    ./04_fetch_photos.py --install       # docker cp into the store + insert resource rows

Downloads to a staging directory on the host rather than straight into the volume: the volume
is root-owned, and `docker cp` is the sanctioned way in. Re-running skips what is already on
disk, so an interrupted run costs only what it had not finished.
"""

import argparse
import hashlib
import json
import subprocess
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

from lib import paths

STAGE = paths.PHOTOS
# The prefix groups these in the object store the way the module prefixes already do, so an
# operator holding only a key can tell what it is and where it came from.
PREFIX = paths.PHOTO_PREFIX
SIZE = "750x750"
UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/126 Safari/537.36"

_lock = threading.Lock()
_done = 0
_bytes = 0


def key_for(url):
    """The object key for a Tiki photo URL.

    Tiki's own path ends in a content hash — `.../f3/5f/90/8bbcc20a8f7571c20017f69227d38895.jpg`
    — so that is the identity: two products sharing a photograph share a key and the file is
    fetched once. Fanned out one level so the directory does not hold twenty thousand entries.
    """
    stem = url.rsplit("/", 1)[-1]
    name = stem if stem.lower().endswith((".jpg", ".jpeg", ".png", ".webp")) else stem + ".jpg"
    return f"{PREFIX}/{name[:2]}/{name}"


def fetch(job):
    url, key = job
    dest = STAGE / key
    if dest.exists() and dest.stat().st_size > 0:
        return key, dest.stat().st_size, None
    dest.parent.mkdir(parents=True, exist_ok=True)
    tmp = dest.with_suffix(dest.suffix + ".part")
    for attempt in range(3):
        p = subprocess.run(["curl", "-sS", "--compressed", "-m", "40", "-A", UA,
                            "-o", str(tmp), "-w", "%{http_code}", url],
                           capture_output=True, text=True)
        if p.returncode == 0 and p.stdout.strip() == "200" and tmp.exists() and tmp.stat().st_size > 500:
            tmp.replace(dest)
            return key, dest.stat().st_size, None
        time.sleep(1.5 * (attempt + 1))
    tmp.unlink(missing_ok=True)
    return key, 0, f"{p.stdout.strip() or p.returncode}"


def download(workers):
    vocab = json.loads(paths.VOCAB.read_text(encoding="utf-8"))
    # name -> key, and the set of distinct downloads. A photo shared by two products is one file.
    manifest, jobs = [], {}
    for leaf, items in vocab.items():
        for it in items:
            if not it.get("thumb"):
                continue
            url = it["thumb"].replace("280x280", SIZE)
            key = key_for(url)
            jobs[key] = url
            manifest.append({"leaf": leaf, "name": it["name"], "key": key})
    print(f"{len(manifest)} names -> {len(jobs)} distinct photos", file=sys.stderr)

    global _done, _bytes
    failed = []
    started = time.time()
    with ThreadPoolExecutor(max_workers=workers) as ex:
        for key, size, err in ex.map(fetch, [(u, k) for k, u in jobs.items()]):
            with _lock:
                _done += 1
                _bytes += size
                if err:
                    failed.append((key, err))
                if _done % 500 == 0:
                    rate = _done / max(0.001, time.time() - started)
                    print(f"  {_done}/{len(jobs)}  {_bytes / 1e9:.2f} GB  {rate:.0f}/s  "
                          f"{len(failed)} failed", file=sys.stderr)

    sizes = {}
    for key in jobs:
        f = STAGE / key
        if f.exists() and f.stat().st_size > 0:
            sizes[key] = f.stat().st_size
    manifest = [m for m in manifest if m["key"] in sizes]
    for m in manifest:
        m["size"] = sizes[m["key"]]
    (paths.MANIFEST).write_text(
        json.dumps(manifest, ensure_ascii=False), encoding="utf-8")
    print(f"\n{len(sizes)} photos, {_bytes / 1e9:.2f} GB, {len(failed)} failed "
          f"in {time.time() - started:.0f}s", file=sys.stderr)
    if failed:
        print("first failures:", failed[:5], file=sys.stderr)
    print(f"manifest: {len(manifest)} name->key rows", file=sys.stderr)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--workers", type=int, default=12)
    ap.add_argument("--install", action="store_true",
                    help="copy into the object store and write the resource rows")
    args = ap.parse_args()
    if args.install:
        sys.exit("run 05_install_photos.sh for the install step")
    download(args.workers)


if __name__ == "__main__":
    main()
