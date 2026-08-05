-- The mock courier's scenarios, one row per way a parcel can go, so a buyer's carrier list is a
-- real choice and a client can be built against the ways delivery fails. Before this,
-- `TRANSPORT_PROVIDER=mock` meant one flat fee and one outcome: every parcel arrived in thirty
-- seconds, so nothing exercised a carrier that will not price a route, a booking that is refused
-- after the fee was collected, or a parcel that goes quiet in transit.
--
-- `provider` is 'mock', not 'platform', and that is what keeps them out of a real deployment:
-- `common.Option.Offered` shows a row only when its provider is the configured one, so these
-- disappear the moment TRANSPORT_PROVIDER names a courier that moves parcels.
--
-- The slugs have to match `internal/provider/transport/mock`, which decides what each one does;
-- order's own test asserts they still do. A slug is permanent once published: a shipped
-- `item.transport_option` holds it as a plain string with no foreign key.
INSERT INTO "option" ("id", "is_enabled", "name", "description", "priority", "type", "provider")
VALUES
    ('mock-standard', TRUE, 'Mock: standard (2 days)',
     'Delivers about fifteen seconds after booking. The happy path.',
     90, 'transport', 'mock'),
    ('mock-express', TRUE, 'Mock: express (same day)',
     'Costs more and delivers in about five seconds, so the whole escrow lifecycle is watchable.',
     89, 'transport', 'mock'),
    ('mock-economy', TRUE, 'Mock: economy (a week)',
     'Cheapest, and the slowest this courier goes: about thirty seconds, long enough to see an order sit in transit.',
     88, 'transport', 'mock'),
    ('mock-no-service', TRUE, 'Mock: does not serve this route',
     'Refuses to quote, so it is missing from the shipping-quote list instead of failing the page. Choosing it at checkout is refused.',
     87, 'transport', 'mock'),
    ('mock-booking-fails', TRUE, 'Mock: booking is refused',
     'Quotes and takes the fee, then refuses the booking. The case the unbooked-shipment retry exists for.',
     86, 'transport', 'mock'),
    ('mock-stuck', TRUE, 'Mock: parcel goes quiet',
     'Reports in-transit and never anything else. Move it along by hand with POST /webhooks/transport/mock.',
     85, 'transport', 'mock'),
    ('mock-failed-delivery', TRUE, 'Mock: delivery fails',
     'Goes in-transit and then comes back undelivered, about fifteen seconds after booking.',
     84, 'transport', 'mock')
ON CONFLICT ("id") DO NOTHING;
