-- The mock rail's scenarios, one row per outcome, so a client can pick "decline this payment" from
-- the same list it picks a real rail from. Before this, `PAYMENT_PROVIDER=mock` meant every
-- checkout succeeded and no client could be built against a refusal, a late webhook or a gateway
-- that is down — the paths a payment flow actually has to handle.
--
-- `provider` is 'mock', not 'platform', and that is what keeps them out of a real deployment:
-- `common.Option.Offered` shows a row only when its provider is the configured one, so these
-- disappear the moment PAYMENT_PROVIDER names a rail that moves money. Seeded by a migration
-- rather than by `cmd/seed`, because a client needs them in every dev stack and the seed is
-- optional and host-only.
--
-- The slugs have to match `internal/provider/payment/mock`, which decides what each one does;
-- finance's own test asserts they still do. A slug is permanent once published: a settled
-- `transaction.payment_option` holds it as a plain string with no foreign key.
INSERT INTO "option" ("id", "is_enabled", "name", "description", "priority", "type", "provider")
VALUES
    ('mock-success', TRUE, 'Mock: pay now (succeeds)',
     'Settles immediately, no redirect and no webhook. The happy path.',
     90, 'payment', 'mock'),
    ('mock-decline', TRUE, 'Mock: pay now (declined)',
     'Refused immediately. The session goes back on the shelf, so another rail can be tendered.',
     89, 'payment', 'mock'),
    ('mock-slow-success', TRUE, 'Mock: pay now (slow, succeeds)',
     'Holds the request open for a few seconds, then succeeds — a spinner and a client timeout.',
     88, 'payment', 'mock'),
    ('mock-redirect', TRUE, 'Mock: hosted page',
     'Redirects to a fake gateway page where you press Pay or Decline, then comes back. The shape of a real redirect rail.',
     87, 'payment', 'mock'),
    ('mock-webhook-success', TRUE, 'Mock: webhook succeeds later',
     'Answers pending and reports success about eight seconds later, so a client has to wait for the notification rather than trust the response.',
     86, 'payment', 'mock'),
    ('mock-webhook-decline', TRUE, 'Mock: webhook declines later',
     'Answers pending, then reports a failure. The other half of the asynchronous path.',
     85, 'payment', 'mock'),
    ('mock-webhook-retried', TRUE, 'Mock: webhook delivered twice',
     'Reports the same success twice, the way a real gateway retries until it gets a 200. Nothing may be counted twice.',
     84, 'payment', 'mock'),
    ('mock-webhook-mismatch', TRUE, 'Mock: webhook reports another amount',
     'Reports success for half the amount charged. The leg settles on its own figure, never on the one the rail claims.',
     83, 'payment', 'mock'),
    ('mock-unreachable', TRUE, 'Mock: rail is down',
     'The charge call itself fails. Not a declined payment: nothing settles and the payer can try again.',
     82, 'payment', 'mock'),
    ('mock-no-answer', TRUE, 'Mock: never answers',
     'Answers pending and never reports. The session sits until the expiry job voids it — settle it by hand with POST /webhooks/payment/mock.',
     81, 'payment', 'mock')
ON CONFLICT ("id") DO NOTHING;
