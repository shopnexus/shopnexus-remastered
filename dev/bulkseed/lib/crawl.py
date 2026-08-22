#!/usr/bin/env python3
"""Harvest real Vietnamese product names from Tiki into a vocabulary the generator draws on.

Why a vocabulary and not the products themselves: 100 000 listings spread evenly over 34
leaves is ~2 900 each, and Tiki does not have 2 900 rice cookers to page through. So this
takes a few hundred real names per leaf and the generator permutes them — a real base name
carrying a real brand, then colour, capacity, size and packaging drawn per listing. The
names stay Vietnamese and plausible; only the combinations are invented.

Prices come along because a plausible name with an implausible price is worse than neither:
a generated "Nồi cơm điện Sharp 1.8L" priced at 39 000 000 đ reads as broken data. The
generator jitters around what Tiki actually charges for that kind of thing.

    ./crawl_tiki.py                     # ~400 names per leaf into vocab.json
    ./crawl_tiki.py --per-leaf 800      # a deeper pool; slower, more distinct names
    ./crawl_tiki.py --only "Nhạc cụ"    # re-crawl one leaf after widening its map

Runs on the host, not in the container: it needs the internet, and the container network
has no route out. The generator reads vocab.json from the mounted source tree.
"""

import argparse
import json
import re
import subprocess
import sys
import threading
import time

from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

from lib.leafmap import TREE

API = "https://tiki.vn/api/v2/products?limit=40&page={page}&category={cat}"
UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126 Safari/537.36"
from lib import paths

_print_lock = threading.Lock()


def log(msg):
    with _print_lock:
        print(msg, file=sys.stderr, flush=True)


class Throttle:
    """One global request pace, not one per worker.

    Tiki throttles on requests per second from an address, so the limit has to be shared:
    two workers each sleeping 0.5s between calls is one request per 0.25s arriving there,
    which is what got the first run of this script blocked. When it does throttle it stops
    answering JSON and returns an error page, so the backoff is driven by the parse failing
    and not by a status code — and it is global too, because a block applies to every worker
    at once and there is no point in the others discovering it one by one.
    """

    def __init__(self, interval):
        self.interval = interval
        self._lock = threading.Lock()
        self._next = 0.0
        self._penalty = 0.0
        self._strikes = 0

    def wait(self):
        with self._lock:
            due = max(self._next, time.monotonic() + self._penalty)
            self._next = due + self.interval
            self._penalty = 0.0        # the cooldown is served once, not once per caller
        delay = due - time.monotonic()
        if delay > 0:
            time.sleep(delay)

    def blocked(self):
        """A response that was not JSON: sit out a cooldown and slow the steady pace.

        Both halves matter. The cooldown gets past the block that already happened; the
        wider interval is what stops the next one, because a block means the pace was too
        fast and resuming at that pace walks straight back into it.
        """
        with self._lock:
            self._strikes += 1
            self.interval = min(4.0, self.interval * 1.6)
            self._penalty = min(90.0, 15.0 * self._strikes)
            return self._penalty

    def ok(self):
        """A successful parse: stop counting strikes, but keep the slower interval."""
        with self._lock:
            self._strikes = 0


PACE = Throttle(0.6)


class Proxies:
    """A pool of proxies, rotated per request, with the blocked ones benched.

    Tiki throttles per address, and it throttles hard: a single client at one request a
    second still gets benched. With a pool the limit stops being the problem it was —
    each address is asked for a page every len(pool) requests instead of every one — and
    the backoff becomes per address rather than global, because one blocked proxy says
    nothing about the others.

    Empty pool means direct, which is the no-proxy path and still governed by PACE.
    """

    def __init__(self, urls, per_proxy=8.0):
        self.urls = list(urls)
        # Each address gets its own pace, which is the point of the pool. Eight workers
        # rotating over nine proxies with no interval is still nine addresses being asked for
        # a page several times a second, and Tiki benched all nine inside a minute. Rotation
        # spreads the load; only an interval per address actually limits it.
        self.per_proxy = per_proxy
        self.bench_seconds = 2700.0
        self._lock = threading.Lock()
        self._i = 0
        self._ready = {}            # url -> monotonic time it may be used again

    def __bool__(self):
        return bool(self.urls)

    def take(self):
        """The next proxy due, or None to go direct. Sleeps until one comes due."""
        if not self.urls:
            return None
        while True:
            with self._lock:
                now = time.monotonic()
                for _ in range(len(self.urls)):
                    url = self.urls[self._i % len(self.urls)]
                    self._i += 1
                    if self._ready.get(url, 0.0) <= now:
                        self._ready[url] = now + self.per_proxy
                        return url
                soonest = min(self._ready.values())
            time.sleep(max(0.2, soonest - time.monotonic()))

    def bench(self, url, seconds=None):
        """Stand an address down: it was challenged, or the proxy itself failed.

        Forty-five minutes by default, not ninety seconds. The first version guessed short and
        every proxy came straight back to be challenged again, which spent the whole pool in a
        few minutes. Measured afterwards: an address that was challenged answered normally
        again about an hour later, so the block is a cooldown and the only useful response is
        to actually sit it out. A dead proxy — one that never completed a CONNECT — is a
        different thing and gets the short bench, since nothing about Tiki is involved.
        """
        if not url:
            return 0
        with self._lock:
            self._ready[url] = time.monotonic() + (self.bench_seconds
                                                   if seconds is None else seconds)
            now = time.monotonic()
            return sum(1 for u in self.urls if self._ready.get(u, 0.0) <= now)


POOL = Proxies([])


def fetch(cat, page, attempts=5):
    """One page of one Tiki category, fetched through curl.

    curl and not urllib, which is not a style preference: Tiki fronts the API with a bot
    check that decides on the TLS handshake, and Python's stack fails it — every request
    comes back as an 18KB challenge page instead of JSON, whatever the headers say. curl's
    handshake passes. Shelling out per page costs a process spawn, which is nothing beside
    the pace this has to keep anyway.

    Returns [] on a page past the end or a category that has gone away — both are ordinary
    and neither should stop a crawl of 189 categories. A body that is not JSON means the
    throttle, and that one is retried after the global backoff.
    """
    url = API.format(page=page, cat=cat)
    for i in range(attempts):
        proxy = POOL.take()
        if not proxy:
            PACE.wait()             # one address, so the pace is the only defence
        cmd = ["curl", "-sS", "--compressed", "-m", "30", "-A", UA,
               "-H", "Accept: application/json"]
        if proxy:
            cmd += ["-x", proxy]
        proc = subprocess.run(cmd + [url], capture_output=True)
        if proc.returncode != 0:
            if proxy:               # a dead proxy, not a dead category
                POOL.bench(proxy, 120)   # dead socket, not a Tiki block
                continue
            if i == attempts - 1:
                log(f"    ! cat {cat} p{page}: curl exit {proc.returncode}")
                return []
            time.sleep(1.5 * (i + 1))
            continue
        try:
            body = json.loads(proc.stdout)
        except json.JSONDecodeError:
            if proxy:
                free = POOL.bench(proxy)
                if i == attempts - 1:
                    log(f"    ! cat {cat} p{page}: throttled on every proxy tried")
                    return []
                if free == 0:
                    log("    ~ every proxy benched, waiting")
                continue
            held = PACE.blocked()
            if i == attempts - 1:
                log(f"    ! cat {cat} p{page}: throttled, gave up")
                return []
            log(f"    ~ throttled, holding {held:.0f}s")
            continue
        PACE.ok()
        if isinstance(body, dict) and body.get("error"):
            return []
        return body.get("data", [])
    return []


# Tiki names carry seller noise that a catalogue of our own should not inherit: shipping
# promises, flash-sale shouting, marketplace-specific bracket tags.
NOISE = re.compile(
    r"\[[^\]]*\]|\([^)]*(giao|ship|freeship|tặng|khuyến mãi|hcm|hn|combo \d)[^)]*\)"
    r"|freeship\w*|chính hãng \- bảo hành[^,]*|hàng có sẵn|giá rẻ nhất|siêu sale|sale off"
    r"|\bmới 100%\b|\bmới 99%\b|\bchính hãng\b",
    re.IGNORECASE,
)


def clean(name):
    """Strip the marketplace noise and normalise whitespace and dashes."""
    n = NOISE.sub(" ", name)
    n = re.sub(r"\s*[-–|/]\s*$", "", n)
    n = re.sub(r"\s+", " ", n).strip(" -–|,.")
    return n


def keyname(name):
    """The identity used for de-duplication: case- and punctuation-insensitive."""
    return re.sub(r"[^a-z0-9à-ỹ ]", "", name.lower()).strip()


def harvest_leaf(leaf, cats, per_leaf, max_pages):
    """Page each of the leaf's Tiki categories in turn until the pool is full.

    Round-robin over the categories rather than draining one at a time: a leaf fed by
    twenty clothing categories should not end up with two thousand dresses because
    "Đầm nữ" happened to be listed first.
    """
    pool, seen = [], set()
    page = 1
    live = list(cats)
    while live and len(pool) < per_leaf and page <= max_pages:
        for cat in list(live):
            if len(pool) >= per_leaf:
                break
            rows = fetch(cat, page)
            if not rows:
                live.remove(cat)
                continue
            for d in rows:
                name = clean(d.get("name") or "")
                if len(name) < 12 or len(name) > 160:
                    continue
                k = keyname(name)
                if k in seen:
                    continue
                seen.add(k)
                price = d.get("price") or d.get("original_price") or 0
                if price < 1000:                    # a price Tiki did not really quote
                    continue
                sold = d.get("quantity_sold")
                pool.append({
                    "name": name,
                    "brand": (d.get("brand_name") or "").strip() or None,
                    "price": int(price),
                    "thumb": d.get("thumbnail_url"),
                    "rating": d.get("rating_average") or 0,
                    "sold": (sold or {}).get("value") if isinstance(sold, dict) else None,
                    "tiki_cat": cat,
                })
        page += 1
    log(f"  {leaf:38s} {len(pool):>5} names  ({len(cats)} cats, {page - 1} pages deep)")
    return leaf, pool


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--per-leaf", type=int, default=400, help="names to collect per leaf")
    ap.add_argument("--max-pages", type=int, default=30, help="page depth per source category")
    ap.add_argument("--workers", type=int, default=1, help="leaves crawled in parallel")
    ap.add_argument("--pace", type=float, default=0.7,
                    help="seconds between requests, shared across all workers")
    ap.add_argument("--only", action="append", help="crawl just this leaf (repeatable)")
    ap.add_argument("--per-proxy", type=float, default=90.0,
                    help="seconds between requests through any one proxy; the measured "
                         "threshold is around ten requests per address per ten minutes, so "
                         "this is deliberately an order of magnitude under it")
    ap.add_argument("--bench", type=float, default=2700.0,
                    help="seconds to stand a challenged address down (the cooldown Tiki "
                         "applies looks like about an hour)")
    ap.add_argument("--proxies", help="file of proxy URLs, one per line "
                                     "(http://user:pass@host:port); '#' comments ignored")
    ap.add_argument("--resume", action="store_true",
                    help="skip leaves that already have half the target in the output file")
    ap.add_argument("--out", default=str(paths.VOCAB))
    args = ap.parse_args()

    PACE.interval = args.pace
    if args.proxies:
        POOL.urls = [ln.strip() for ln in Path(args.proxies).read_text().splitlines()
                     if ln.strip() and not ln.startswith("#")]
        POOL.per_proxy = args.per_proxy
        POOL.bench_seconds = args.bench
        log(f"{len(POOL.urls)} proxies, one request per {args.per_proxy:.0f}s each "
            f"= {len(POOL.urls) / args.per_proxy * 60:.0f} req/min; "
            f"a challenged one sits out {args.bench / 60:.0f} min")

    leaves = {leaf: cats for kids in TREE.values() for leaf, cats in kids.items()}
    if args.only:
        missing = [o for o in args.only if o not in leaves]
        if missing:
            sys.exit(f"unknown leaf(s): {missing}")
        leaves = {k: v for k, v in leaves.items() if k in args.only}

    out = {}
    outpath = Path(args.out)
    if outpath.exists() and (args.only or args.resume):
        out = json.loads(outpath.read_text(encoding="utf-8"))

    if args.resume:
        # Tiki throttles, and a crawl that gives up an hour in should not throw away the
        # thirty leaves it already has. "Enough" is half the target: a leaf whose sources
        # are genuinely that thin will never reach it and re-crawling it every run is waste.
        done = [k for k in leaves if len(out.get(k, [])) >= args.per_leaf // 2]
        for k in done:
            del leaves[k]
        log(f"resuming: {len(done)} leaves already have enough, {len(leaves)} to go")

    if not leaves:
        log("nothing to crawl")
        return

    log(f"crawling {len(leaves)} leaves, target {args.per_leaf} names each")
    start = time.time()

    def save():
        # After every leaf, not at the end: the run above this one died to a throttle and
        # took everything with it.
        tmp = outpath.with_suffix(".tmp")
        tmp.write_text(json.dumps(out, ensure_ascii=False, indent=1), encoding="utf-8")
        tmp.replace(outpath)

    with ThreadPoolExecutor(max_workers=args.workers) as ex:
        for leaf, pool in ex.map(lambda kv: harvest_leaf(*kv, args.per_leaf, args.max_pages),
                                 leaves.items()):
            out[leaf] = pool
            save()

    save()
    total = sum(len(v) for v in out.values())
    thin = {k: len(v) for k, v in out.items() if len(v) < args.per_leaf // 2}
    log(f"\n{total} names across {len(out)} leaves in {time.time() - start:.0f}s -> {outpath}")
    if thin:
        log("thin leaves (widen their map in leafmap.py, then --only them):")
        for k, n in sorted(thin.items(), key=lambda x: x[1]):
            log(f"  {n:>5}  {k}")


if __name__ == "__main__":
    main()
