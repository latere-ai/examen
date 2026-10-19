-- SPDX-FileCopyrightText: 2026 Latere AI
-- SPDX-License-Identifier: Apache-2.0

-- An experiment's declared test of independence, fixed at create: a label
-- metric against a cell variable, stratified by others; NULL when the
-- experiment declares none.
ALTER TABLE experiments ADD COLUMN independence jsonb;

-- A declared comparison of the general form's declaration, whose two
-- levels of a cell variable select its sides; NULL for every other
-- comparison, whose sides its candidate and reference columns name.
ALTER TABLE comparisons ADD COLUMN declaration jsonb;
