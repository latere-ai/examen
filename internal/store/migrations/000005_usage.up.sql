-- SPDX-FileCopyrightText: 2026 Latere AI
-- SPDX-License-Identifier: Apache-2.0

-- What an experiment's external Key was used for (specs/005-execution.md,
-- "Attribution and cost"). One row per experiment whose runs a person's own
-- setup does through the Key the key source leased for it: the Key's id at
-- the key source, never its value; when Examen released it at the
-- experiment's end; and how far Lux's records of its calls were read.
-- read_to is the end of the last window read, read_at when it was read,
-- ended_at when a read first found the experiment ended, and done is set
-- once a read covers the experiment's end with the lookback past it.
CREATE TABLE experiment_usage (
    experiment  text        PRIMARY KEY REFERENCES experiments (id) ON DELETE CASCADE,
    owner       text        NOT NULL,
    key_id      text        NOT NULL CHECK (key_id <> ''),
    released_at timestamptz,
    read_to     timestamptz,
    read_at     timestamptz,
    ended_at    timestamptz,
    done        boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL
);
CREATE INDEX experiment_usage_open ON experiment_usage (read_at) WHERE NOT done;

-- Lux's records of the calls made with an experiment's Key, each once by
-- its request id. trial is the trial the record is attributed to: the one
-- its request label examen.trial names, or the one that submitted its
-- request id; NULL for a call no trial of the experiment claims, which
-- counts toward the experiment and no trial. label is examen.trial as the
-- call sent it, '' for none. sent_model is the model the request named,
-- served the model that served it. record is Lux's record as read, less
-- the prefix of the Key's value, which the key source never answers.
CREATE TABLE usage_records (
    experiment text        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    request_id text        NOT NULL,
    trial      text        REFERENCES trials (id) ON DELETE SET NULL,
    at         timestamptz NOT NULL,
    label      text        NOT NULL DEFAULT '',
    sent_model text        NOT NULL DEFAULT '',
    served     text        NOT NULL DEFAULT '',
    provider   text        NOT NULL DEFAULT '',
    status     text        NOT NULL CHECK (status IN ('ok', 'refused', 'failed')),
    error      text        NOT NULL DEFAULT '',
    tokens     jsonb       NOT NULL,
    cost_micro bigint      NOT NULL DEFAULT 0,
    currency   text        NOT NULL DEFAULT '',
    priced     boolean     NOT NULL,
    record     jsonb       NOT NULL,
    read_at    timestamptz NOT NULL,
    PRIMARY KEY (experiment, request_id)
);
CREATE INDEX usage_records_trial ON usage_records (trial) WHERE trial IS NOT NULL;

-- A trial's sent layer (specs/007-measurements-and-evidence.md), from Lux's
-- records of its calls, and when its cost was last settled from them; NULL
-- until it is, and again when a record of it arrives after.
ALTER TABLE trials ADD COLUMN sent jsonb;
ALTER TABLE trials ADD COLUMN usage_settled_at timestamptz;
