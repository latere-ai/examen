-- SPDX-FileCopyrightText: 2026 Latere AI
-- SPDX-License-Identifier: Apache-2.0

-- The order in which an experiment's cells take its parallelism and its
-- external trials are leased, fixed at create. Every experiment made
-- before this column runs task by task, as it did.
ALTER TABLE experiments ADD COLUMN scheduling text NOT NULL DEFAULT 'task_by_task'
    CHECK (scheduling IN ('task_by_task', 'interleaved'));

-- The comparisons an experiment declared at create, fixed then, and its
-- declared analysis: their primary results corrected together, recomputed
-- under the experiment's row lock and replaced each time; NULL until the
-- first of them is computed.
ALTER TABLE experiments ADD COLUMN comparisons jsonb NOT NULL DEFAULT '[]';
ALTER TABLE experiments ADD COLUMN declared jsonb;

-- A declared comparison's position in its experiment's comparisons; NULL
-- for every other comparison.
ALTER TABLE comparisons ADD COLUMN declared integer CHECK (declared >= 0);
CREATE INDEX comparisons_declared ON comparisons (experiment, declared, created_at DESC) WHERE declared IS NOT NULL;
