-- Same as finance's: `provider = 'platform'` no longer resolves to anything, because a carrier row's
-- provider is now the key naming the courier that books it.
--
-- Only the registry row goes. `standard-delivery` stays on every order and shipment that already
-- names it — that is what the immutable slug is for — and those resolve through the row's history,
-- not through a live registry entry.
DELETE FROM "option" WHERE "type" = 'transport' AND "provider" = 'platform';
