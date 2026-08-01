-- The carrier registry has to have at least one live row or nothing can be checked out: a
-- checkout names a transport option, and an option nobody enabled is refused. A fresh
-- deployment with an empty registry is a marketplace where every purchase fails at the last
-- step, so the one every deployment wants is seeded here.
--
-- `provider` is 'platform' rather than a vendor: which courier actually prices this is
-- `TRANSPORT_PROVIDER`, a deployment's choice, and a row claiming 'ghn' under a stack
-- configured for something else would be a lie the operator has to notice. Add a
-- vendor-specific row beside this one when a deployment offers a real choice — the slug is
-- immutable, because past orders hold it as a plain string.
INSERT INTO "option" ("id", "is_enabled", "name", "description", "priority", "type", "provider")
VALUES (
    'standard-delivery',
    TRUE,
    'Standard delivery',
    'Door-to-door delivery, priced by the platform''s carrier at checkout and paid by the buyer.',
    100,
    'transport',
    'platform'
)
ON CONFLICT ("id") DO NOTHING;
