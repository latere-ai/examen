-- SPDX-FileCopyrightText: 2026 Latere AI
-- SPDX-License-Identifier: Apache-2.0

-- A trial whose run Examen started at a core (specs/005-execution.md, the
-- `topos` executor) and that ended canceled while its run went on: a cancel
-- of the experiment or a parked cell records the trial's end at once, and
-- the executor then stops the run at the core, clearing stop_pending once
-- the core reports it stopped. A run whose trial ended before the core
-- answered its create is stopped the same way.
ALTER TABLE trials ADD COLUMN stop_pending boolean NOT NULL DEFAULT false;
CREATE INDEX trials_stop_pending ON trials (experiment) WHERE stop_pending;
