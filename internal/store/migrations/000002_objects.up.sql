-- SPDX-FileCopyrightText: 2026 Latere AI
-- SPDX-License-Identifier: Apache-2.0

-- A row of these tables is a record that never changes once written: a
-- version, a baseline record, a taxonomy record. The trigger refuses any
-- update and any delete.
CREATE FUNCTION examen_never_changes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'a row of % is never changed or deleted', TG_TABLE_NAME;
END
$$;

-- Every version of a metric, grader, task, suite or candidate
-- (specs/002-objects.md): its kind's fields as applied, name included,
-- under the digest of their canonical JSON. A digest names one version of
-- one owner's kind, so applying the same content again finds it.
CREATE TABLE versions (
    id         text        PRIMARY KEY,
    kind       text        NOT NULL CHECK (kind IN ('metric', 'grader', 'task', 'suite', 'candidate')),
    owner      text        NOT NULL,
    name       text        NOT NULL,
    digest     text        NOT NULL,
    body       jsonb       NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (kind, owner, digest)
);
CREATE INDEX versions_by_name ON versions (kind, owner, name, id DESC);
CREATE INDEX versions_by_owner ON versions (kind, owner, id DESC);
CREATE TRIGGER versions_never_change BEFORE UPDATE OR DELETE ON versions
    FOR EACH ROW EXECUTE FUNCTION examen_never_changes();

-- Each name's current version.
CREATE TABLE heads (
    kind       text        NOT NULL,
    owner      text        NOT NULL,
    name       text        NOT NULL,
    version    text        NOT NULL REFERENCES versions (id),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (kind, owner, name)
);
CREATE INDEX heads_by_version ON heads (version);

-- Experiments. next_seq is the sequence the next event of the
-- experiment's log takes, advanced under the experiment's row lock.
CREATE TABLE experiments (
    id                  text        PRIMARY KEY,
    owner               text        NOT NULL,
    suite               text        NOT NULL,
    suite_id            text        NOT NULL REFERENCES versions (id),
    suite_name          text        NOT NULL,
    candidates          text[]      NOT NULL,
    candidate_ids       text[]      NOT NULL,
    trials              integer     NOT NULL CHECK (trials >= 1),
    budget              text        NOT NULL,
    parallelism         integer     NOT NULL CHECK (parallelism >= 1),
    reference_baseline  text,
    reference_candidate text,
    mode                text        NOT NULL CHECK (mode IN ('execute', 'import')),
    state               text        NOT NULL CHECK (state IN ('pending', 'running', 'grading', 'complete', 'canceled')),
    created_by          text        NOT NULL,
    metadata            jsonb       NOT NULL DEFAULT '{}',
    intake_closed       boolean     NOT NULL DEFAULT false,
    next_seq            bigint      NOT NULL DEFAULT 1,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    CHECK ((reference_baseline IS NULL) <> (reference_candidate IS NULL))
);
CREATE INDEX experiments_by_owner ON experiments (owner, id DESC);
CREATE INDEX experiments_active ON experiments (owner) WHERE state NOT IN ('complete', 'canceled');

-- Cells: one task and one candidate of an experiment, with their places in
-- the suite's and the experiment's order.
CREATE TABLE cells (
    id            text        PRIMARY KEY,
    owner         text        NOT NULL,
    experiment    text        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    task          text        NOT NULL,
    task_id       text        NOT NULL REFERENCES versions (id),
    candidate     text        NOT NULL,
    candidate_id  text        NOT NULL REFERENCES versions (id),
    task_pos      integer     NOT NULL,
    candidate_pos integer     NOT NULL,
    tuple         jsonb       NOT NULL,
    rollouts      jsonb       NOT NULL DEFAULT '[]',
    state         text        NOT NULL CHECK (state IN ('pending', 'smoking', 'running', 'grading', 'complete', 'blocked', 'over_budget', 'canceled')),
    parked_reason text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    UNIQUE (experiment, task, candidate)
);
CREATE INDEX cells_by_experiment ON cells (experiment, task_pos, candidate_pos);

-- Trials. A lease of external trials is a hold on trials and no row of
-- its own: the lease columns name it, its holder and its expiry, and stay
-- after it lapses, so a renewal or a submit under a lapsed lease is told
-- from one under a lease that never held the trial.
CREATE TABLE trials (
    id               text        PRIMARY KEY,
    owner            text        NOT NULL,
    experiment       text        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    cell             text        NOT NULL REFERENCES cells (id) ON DELETE CASCADE,
    idx              integer     NOT NULL CHECK (idx >= 0),
    rollout          text        NOT NULL DEFAULT '',
    executor_ref     text        NOT NULL DEFAULT '',
    lease_id         text,
    lease_subject    text,
    lease_expires_at timestamptz,
    state            text        NOT NULL CHECK (state IN ('pending', 'running', 'checking', 'done')),
    outcome          text        CHECK (outcome IN ('completed', 'declined', 'timed_out', 'turn_limit', 'over_budget', 'infra_failed', 'canceled')),
    failure          jsonb,
    retry_of         text        REFERENCES trials (id),
    retried          boolean     NOT NULL DEFAULT false,
    auto_retry       boolean     NOT NULL DEFAULT false,
    observed         jsonb       NOT NULL DEFAULT '{}',
    declared         jsonb,
    evidence         jsonb       NOT NULL DEFAULT '[]',
    outputs          jsonb,
    imported         boolean     NOT NULL DEFAULT false,
    started_at       timestamptz,
    agent_ended_at   timestamptz,
    ended_at         timestamptz,
    submit_lease     text,
    submit_digest    text,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL,
    CHECK ((state = 'done') = (outcome IS NOT NULL))
);
CREATE INDEX trials_by_cell ON trials (cell, state, outcome);
CREATE INDEX trials_by_experiment ON trials (experiment, id DESC);
CREATE INDEX trials_by_lease ON trials (lease_id) WHERE lease_id IS NOT NULL;
CREATE INDEX trials_leased ON trials (lease_expires_at) WHERE state = 'running';
CREATE UNIQUE INDEX trials_imported_ref ON trials (experiment, executor_ref) WHERE imported AND executor_ref <> '';

-- The measurement store of specs/007-measurements-and-evidence.md.
CREATE TABLE measurements (
    id            text        PRIMARY KEY,
    owner         text        NOT NULL,
    experiment    text        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    cell          text,
    trial         text        REFERENCES trials (id) ON DELETE CASCADE,
    candidate     text,
    metric        text        NOT NULL,
    grader        text        NOT NULL,
    annotator     text        NOT NULL DEFAULT '',
    self_reported boolean     NOT NULL DEFAULT false,
    generation    integer     NOT NULL CHECK (generation >= 1),
    value_bool    boolean,
    value_num     double precision,
    value_text    text,
    rationale     text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL,
    UNIQUE (trial, grader, annotator, generation, metric),
    CHECK (num_nonnulls(value_bool, value_num, value_text) = 1),
    CHECK (self_reported = (grader LIKE 'self:%')),
    CHECK ((trial IS NOT NULL AND cell IS NOT NULL AND candidate IS NULL)
        OR (trial IS NULL AND cell IS NULL AND candidate IS NOT NULL))
);
CREATE UNIQUE INDEX measurements_candidate_level
    ON measurements (experiment, candidate, grader, generation, metric)
    WHERE trial IS NULL;
CREATE INDEX measurements_cell_grader
    ON measurements (cell, grader, trial, annotator, generation DESC);
CREATE INDEX measurements_experiment_metric
    ON measurements (experiment, metric);
CREATE INDEX measurements_by_trial ON measurements (trial, id);

-- Append-only: an update always raises, and a delete raises while the
-- measurement's trial, or for a candidate-level one its experiment, still
-- exists. The cascade from deleting either runs after its row is gone.
CREATE FUNCTION examen_measurements_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'a measurement is never updated';
    END IF;
    IF OLD.trial IS NOT NULL AND EXISTS (SELECT 1 FROM trials WHERE id = OLD.trial) THEN
        RAISE EXCEPTION 'a measurement is never deleted while its trial exists';
    END IF;
    IF OLD.trial IS NULL AND EXISTS (SELECT 1 FROM experiments WHERE id = OLD.experiment) THEN
        RAISE EXCEPTION 'a measurement is never deleted while its experiment exists';
    END IF;
    RETURN OLD;
END
$$;
CREATE TRIGGER measurements_append_only BEFORE UPDATE OR DELETE ON measurements
    FOR EACH ROW EXECUTE FUNCTION examen_measurements_append_only();

-- An experiment's event log, written in the transaction of the change it
-- reports. A sink event's delivery is acknowledged at once while no sink
-- is configured.
CREATE TABLE events (
    id         text        PRIMARY KEY,
    owner      text        NOT NULL,
    experiment text        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    seq        bigint      NOT NULL CHECK (seq >= 1),
    type       text        NOT NULL,
    data       jsonb       NOT NULL,
    at         timestamptz NOT NULL,
    delivery   text        CHECK (delivery IN ('pending', 'acknowledged', 'dropped')),
    UNIQUE (experiment, seq)
);

-- Baseline records: a lineage per owner, suite and name, the latest
-- current.
CREATE TABLE baselines (
    id          text        PRIMARY KEY,
    owner       text        NOT NULL,
    name        text        NOT NULL,
    suite_owner text        NOT NULL,
    suite_name  text        NOT NULL,
    experiment  text        NOT NULL REFERENCES experiments (id),
    candidate   text        NOT NULL,
    previous    text        REFERENCES baselines (id),
    created_by  text        NOT NULL,
    pinned_at   timestamptz NOT NULL,
    created_at  timestamptz NOT NULL
);
CREATE INDEX baselines_by_name ON baselines (owner, suite_owner, suite_name, name, id DESC);
CREATE TRIGGER baselines_never_change BEFORE UPDATE OR DELETE ON baselines
    FOR EACH ROW EXECUTE FUNCTION examen_never_changes();

-- Taxonomy records: a lineage per suite and open-vocabulary metric.
CREATE TABLE taxonomies (
    id          text        PRIMARY KEY,
    owner       text        NOT NULL,
    suite_owner text        NOT NULL,
    suite_name  text        NOT NULL,
    metric      text        NOT NULL,
    version     integer     NOT NULL CHECK (version >= 1),
    canonical   jsonb       NOT NULL,
    map         jsonb       NOT NULL,
    source      jsonb       NOT NULL,
    previous    text        REFERENCES taxonomies (id),
    digest      text        NOT NULL,
    created_by  text        NOT NULL,
    created_at  timestamptz NOT NULL,
    UNIQUE (suite_owner, suite_name, metric, version)
);
CREATE TRIGGER taxonomies_never_change BEFORE UPDATE OR DELETE ON taxonomies
    FOR EACH ROW EXECUTE FUNCTION examen_never_changes();

-- Comparisons. A provisional one is recomputed in place; dirty marks one
-- whose inputs changed since it was last computed.
CREATE TABLE comparisons (
    id                  text        PRIMARY KEY,
    owner               text        NOT NULL,
    experiment          text        NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
    candidate           text        NOT NULL,
    baseline            text,
    reference_candidate text,
    task                text,
    reference_task      text,
    automatic           boolean     NOT NULL,
    exploratory         boolean     NOT NULL,
    state               text        NOT NULL CHECK (state IN ('computing', 'complete', 'failed')),
    final               boolean     NOT NULL DEFAULT false,
    dirty               boolean     NOT NULL DEFAULT true,
    variable            jsonb       NOT NULL DEFAULT '[]',
    declared_provenance jsonb       NOT NULL DEFAULT '[]',
    computed            jsonb       NOT NULL DEFAULT '{}',
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL
);
CREATE INDEX comparisons_by_experiment ON comparisons (experiment, id DESC);
CREATE INDEX comparisons_by_owner ON comparisons (owner, id DESC);
CREATE INDEX comparisons_dirty ON comparisons (updated_at) WHERE dirty;

-- What a comparison read, one row per measurement.
CREATE TABLE comparison_inputs (
    comparison  text NOT NULL REFERENCES comparisons (id) ON DELETE CASCADE,
    measurement text NOT NULL,
    PRIMARY KEY (comparison, measurement)
);
