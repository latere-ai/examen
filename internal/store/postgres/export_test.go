// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"

	"latere.ai/x/examen/internal/objects"
)

// MigrateWith is Migrate with the migration files given, for a test that
// applies a migration the binary does not carry.
var MigrateWith = migrate

// MoveExperiment takes an experiment to a state through the writer of
// experiments.state, in a transaction that holds the experiment's row lock,
// as every change of an experiment does.
func MoveExperiment(ctx context.Context, s *Store, eid string, to objects.ExperimentState) error {
	return experiments{s}.begin(ctx, eid, func(c *change) error {
		return c.experimentState(ctx, to)
	})
}

// MoveCell takes a cell to a state through the writers of cells.state, in
// the transaction that holds its experiment's row lock.
func MoveCell(ctx context.Context, s *Store, cid string, to objects.CellState) error {
	x := experiments{s}
	cell, err := x.Cell(ctx, cid)
	if err != nil {
		return err
	}
	return x.begin(ctx, cell.Experiment, func(c *change) error {
		cell, _, err := c.cell(ctx, cid)
		if err != nil {
			return err
		}
		if err := c.cellState(ctx, &cell, to, "test"); err != nil {
			return err
		}
		return c.saveCell(ctx, cell)
	})
}

// MoveRollout takes the rollout of a cell to a state through the writer of a
// rollout's state, in the transaction that holds its experiment's row lock.
func MoveRollout(ctx context.Context, s *Store, cid, key string, to objects.RolloutState) error {
	x := experiments{s}
	cell, err := x.Cell(ctx, cid)
	if err != nil {
		return err
	}
	return x.begin(ctx, cell.Experiment, func(c *change) error {
		cell, _, err := c.cell(ctx, cid)
		if err != nil {
			return err
		}
		for i := range cell.Rollouts {
			if cell.Rollouts[i].Key != key {
				continue
			}
			if err := moveRollout(&cell.Rollouts[i], to); err != nil {
				return err
			}
		}
		return c.saveCell(ctx, cell)
	})
}
