-- Sessions for local accounts (BACKEND.md Phase 1).
--
-- Not in CONTRACT §6.1's schema block, which describes the domain rather than
-- the auth mechanism. The contract fixes the shape that crosses the API
-- boundary — an HttpOnly, SameSite=Lax cookie (§7) — and leaves how it is
-- stored to the backend.

CREATE TABLE sessions (
  id UUID PRIMARY KEY,

  -- The SHA-256 of the cookie value, never the value itself. A leaked database
  -- dump must not hand over live sessions, and there is no legitimate reason to
  -- read a session token back out: verification only ever needs to compare.
  token_hash BYTEA NOT NULL UNIQUE,

  user_id      UUID NOT NULL REFERENCES users(id),
  created_at   TIMESTAMPTZ NOT NULL,
  last_seen_at TIMESTAMPTZ NOT NULL,
  expires_at   TIMESTAMPTZ NOT NULL,

  -- Revoked rather than deleted, so "this session was ended at 14:02" stays
  -- answerable alongside the audit log.
  revoked_at TIMESTAMPTZ,

  user_agent TEXT,
  ip         TEXT
);

CREATE INDEX sessions_user ON sessions (user_id, created_at DESC);
CREATE INDEX sessions_live ON sessions (expires_at) WHERE revoked_at IS NULL;

COMMENT ON COLUMN sessions.token_hash IS
  'SHA-256 of the cookie value. The token itself is never stored: it is shown '
  'to the client once, at issue, and thereafter only compared by hash.';
