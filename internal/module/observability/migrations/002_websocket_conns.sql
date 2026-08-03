-- The socket route is excluded from http_requests (it is held for minutes, not
-- milliseconds), so open realtime connections are sampled into runtime_metrics
-- instead, on the same interval as the rest of the runtime snapshot.
ALTER TABLE "runtime_metrics" ADD COLUMN "websocket_conns" INTEGER NOT NULL DEFAULT 0;
