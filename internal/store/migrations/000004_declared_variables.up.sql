-- SPDX-FileCopyrightText: 2026 Latere AI
-- SPDX-License-Identifier: Apache-2.0

-- An experiment's declared test of independence, fixed at create: a label
-- metric against a cell variable, stratified by others; NULL when the
-- experiment declares none. declared_dirty marks an experiment whose test
-- reads a cell that changed since its declared analysis was computed: the
-- test reads every cell, and an experiment may hold no comparison whose
-- mark would carry the change.
ALTER TABLE experiments ADD COLUMN independence jsonb;
ALTER TABLE experiments ADD COLUMN declared_dirty boolean NOT NULL DEFAULT false;
CREATE INDEX experiments_declared_dirty ON experiments (updated_at) WHERE declared_dirty;

-- A declared comparison of the general form's declaration, whose two
-- levels of a cell variable select its sides; NULL for every other
-- comparison, whose sides its candidate and reference columns name.
ALTER TABLE comparisons ADD COLUMN declaration jsonb;
