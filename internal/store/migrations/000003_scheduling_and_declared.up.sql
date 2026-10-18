-- SPDX-FileCopyrightText: 2026 Latere AI
-- SPDX-License-Identifier: Apache-2.0

-- The order in which an experiment's cells take its parallelism and its
-- external trials are leased, fixed at create. Every experiment made
-- before this column runs task by task, as it did.
ALTER TABLE experiments ADD COLUMN scheduling text NOT NULL DEFAULT 'task_by_task'
    CHECK (scheduling IN ('task_by_task', 'interleaved'));
