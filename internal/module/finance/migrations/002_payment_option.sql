-- The rail registry, for the same reason as order's carrier one: starting a payment names an
-- option, an option nobody enabled is refused, and an empty registry is a deployment where no
-- session can ever be paid.
--
-- `provider` is 'platform' because `PAYMENT_PROVIDER` is what decides who charges the card;
-- a row naming a vendor the stack is not configured for would be a claim this database cannot
-- keep. A deployment that offers a real choice of rails adds a row per rail beside this one.
INSERT INTO "option" ("id", "is_enabled", "name", "description", "priority", "type", "provider")
VALUES (
    'platform-checkout',
    TRUE,
    'Card or bank transfer',
    'The platform''s configured payment provider. Whether it redirects or charges directly is the provider''s business.',
    100,
    'payment',
    'platform'
)
ON CONFLICT ("id") DO NOTHING;
