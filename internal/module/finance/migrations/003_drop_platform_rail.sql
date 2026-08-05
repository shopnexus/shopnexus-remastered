-- `provider = 'platform'` meant "whatever PAYMENT_PROVIDER says", which stopped being a thing a row
-- can mean: the row's provider is now the key that resolves to the client charging it, so a value
-- naming no implementation is a rail nothing can serve. There was one such row and it was seeded
-- here, so it goes from here.
--
-- A deployment therefore starts with no rails and cannot take money until it has one — which is the
-- honest state, and louder than a row that claims to be a payment method and is not. Dev gets its
-- rails from MOCK_ENABLED at startup; production gets a row per rail it has actually contracted.
DELETE FROM "option" WHERE "type" = 'payment' AND "provider" = 'platform';
