-- SPDX-FileCopyrightText: 2026 Latere AI
-- SPDX-License-Identifier: Apache-2.0

-- The check runs and judgments Examen runs of a finished trial
-- (specs/006-grading.md): one row per grader of the trial, made in the
-- transaction that records the trial done, pending until its run ends.
-- position orders a trial's gradings, its task's checks first in their
-- order; kind is the grader's kind. A replica takes rows by holding them
-- until held_until, so a replica that stopped part way leaves them to the
-- next; due_at is when a row whose run waited, such as for an outage, is
-- taken again; attempts counts a judgment's calls. record is the check run
-- or judgment record once the row ended, and generation the generation of
-- the grader's measurements it appended, NULL for a run that appended
-- none.
CREATE TABLE gradings (
    id          text        PRIMARY KEY,
    owner       text        NOT NULL,
    experiment  text        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    cell        text        NOT NULL REFERENCES cells (id) ON DELETE CASCADE,
    trial       text        NOT NULL REFERENCES trials (id) ON DELETE CASCADE,
    grader      text        NOT NULL,
    kind        text        NOT NULL CHECK (kind IN ('check', 'model_judge')),
    position    integer     NOT NULL CHECK (position >= 0),
    state       text        NOT NULL,
    attempts    integer     NOT NULL DEFAULT 0,
    due_at      timestamptz NOT NULL,
    held_until  timestamptz,
    record      jsonb,
    generation  integer,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    ended_at    timestamptz,
    UNIQUE (trial, grader),
    CHECK ((state = 'pending') = (ended_at IS NULL))
);
CREATE INDEX gradings_due ON gradings (due_at) WHERE state = 'pending';
CREATE INDEX gradings_by_trial ON gradings (trial, position);
CREATE INDEX gradings_by_cell ON gradings (cell) WHERE state = 'pending';

-- The Keys a model judge of an experiment calls through, by their id at
-- the key source, never their value, and when Examen released each at the
-- experiment's end.
CREATE TABLE judge_keys (
    experiment  text        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    key_id      text        NOT NULL CHECK (key_id <> ''),
    grader      text        NOT NULL,
    released_at timestamptz,
    created_at  timestamptz NOT NULL,
    PRIMARY KEY (experiment, key_id)
);
CREATE INDEX judge_keys_unreleased ON judge_keys (experiment) WHERE released_at IS NULL;
