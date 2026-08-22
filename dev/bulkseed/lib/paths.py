"""Where everything lives, so no module has to guess from its own __file__.

Every script here used to compute `HERE = Path(__file__).parent` and hang the data files off
it. That was fine while they all sat in one directory and wrong the moment they did not — a
module in lib/ resolving vocab.json against its own directory looks for it in the wrong place.
One module owns the layout instead.
"""

from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

SQL = ROOT / "sql"
LIB = ROOT / "lib"

# Crawler output: the product-name vocabulary the generator draws on.
VOCAB = ROOT / "vocab.json"
# Downloaded photographs, and the name -> object-key map for them.
PHOTOS = ROOT / "photos"
MANIFEST = ROOT / "photos_manifest.json"
# COPY-ready TSVs plus the load.sql that stages and inserts them.
OUT = ROOT / "out"
# Proxy list for the crawler. Gitignored: it carries credentials.
PROXIES = ROOT / "proxies.txt"

# The object-store prefix the photographs are installed under, and the store that holds them.
PHOTO_PREFIX = "catalog/tiki"
PHOTO_PROVIDER = "local"
