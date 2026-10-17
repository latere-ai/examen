-- SPDX-FileCopyrightText: 2026 Latere AI
-- SPDX-License-Identifier: Apache-2.0

-- The major release whose schema the database holds, one row, written by
-- the first migration of each major and read by `examend serve`.
CREATE TABLE examen_schema (
    one   boolean PRIMARY KEY DEFAULT true CHECK (one),
    major integer NOT NULL CHECK (major >= 0)
);
INSERT INTO examen_schema (major) VALUES (0);

-- The idempotency keys of specs/003-api.md: one subject's key, the route it
-- was first sent to and the digest of its body, and once the request was
-- answered, the answer a repeat replays until the key expires. A key whose
-- request is still being answered expires when its reservation does.
CREATE TABLE idempotency_keys (
    subject      text        NOT NULL,
    key          text        NOT NULL,
    route        text        NOT NULL,
    body_digest  text        NOT NULL,
    done         boolean     NOT NULL DEFAULT false,
    status       integer     NOT NULL DEFAULT 0,
    content_type text        NOT NULL DEFAULT '',
    body         bytea,
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (subject, key)
);
CREATE INDEX idempotency_keys_expires_at ON idempotency_keys (expires_at);
