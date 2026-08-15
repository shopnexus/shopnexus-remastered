-- A listing's name is not a key.
--
-- `slug` is derived from the name and was UNIQUE across the whole table, which made two ordinary
-- things impossible. Deleting a listing and posting the same goods again failed, because the soft
-- delete leaves the row — and its slug — behind on purpose, so order history stays resolvable.
-- And two sellers could not both list "iPhone 15 Pro", though two sellers offering the same phone
-- being two listings is the entire reason a listing is not an entry in a shared product master.
--
-- Nothing looked a listing up by this column. The public slug a link carries now carries the
-- listing's id on the end (catalogapi.PublicSlug), so a slug resolves by decoding that id and no
-- index on the text is needed to serve it.

ALTER TABLE "listing" DROP CONSTRAINT IF EXISTS "listing_slug_key";
