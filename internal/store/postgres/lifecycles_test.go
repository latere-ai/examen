// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"latere.ai/x/pkg/statemachine"
	"latere.ai/x/pkg/statemachine/statemachinetest"

	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
)

// The writers of every lifecycle are driven through its table on Postgres:
// Exhaustive asks each writer for every state from every state, and Race
// starts two writers at once on a record in each state.

// execIn runs a statement on the database of the world.
func (w *world) execIn(ctx context.Context, sql string, args ...any) {
	w.t.Helper()
	if err := pgtest.Exec(ctx, w.dsn, sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

// scanIn reads one value of one row.
func (w *world) scanIn(ctx context.Context, dest any, sql string, args ...any) {
	w.t.Helper()
	conn, err := pgx.Connect(ctx, w.dsn)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(ctx); err != nil {
			w.t.Error(err)
		}
	}()
	if err := conn.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

// unchanged is the error a writer's answer of "nothing to do" becomes: the
// writer answered without error and did not take the record to the state.
func unchanged(what string) error {
	return fmt.Errorf("%s: the writer did not move the record: %w", what, statemachine.ErrIllegalTransition)
}

// noWriter is the answer for a state no writer takes a record to.
func noWriter(what string) error {
	return fmt.Errorf("%s: no writer takes a record there: %w", what, statemachine.ErrIllegalTransition)
}

// witnessSQL records every change of a lifecycle in lifecycle_writes with
// the application name of the session that made it. Two writers racing on a
// record each run on a store of their own, so the record tells which of them
// took a step, and which answered without error and changed nothing.
const witnessSQL = `
CREATE TABLE lifecycle_writes (tbl text NOT NULL, key text NOT NULL, old text NOT NULL, new text NOT NULL, app text NOT NULL);
CREATE FUNCTION lifecycle_state() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF to_jsonb(OLD)->>TG_ARGV[0] IS DISTINCT FROM to_jsonb(NEW)->>TG_ARGV[0] THEN
		INSERT INTO lifecycle_writes VALUES (TG_TABLE_NAME, concat_ws('|', to_jsonb(NEW)->>TG_ARGV[1], to_jsonb(NEW)->>TG_ARGV[2]),
			to_jsonb(OLD)->>TG_ARGV[0], to_jsonb(NEW)->>TG_ARGV[0], current_setting('application_name'));
	END IF;
	RETURN NEW;
END $$;
CREATE FUNCTION lifecycle_rollout() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF OLD.rollouts->0->>'state' IS DISTINCT FROM NEW.rollouts->0->>'state' THEN
		INSERT INTO lifecycle_writes VALUES ('rollouts', NEW.id, OLD.rollouts->0->>'state', NEW.rollouts->0->>'state', current_setting('application_name'));
	END IF;
	RETURN NEW;
END $$;
CREATE FUNCTION lifecycle_release() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF OLD.released_at IS NULL AND NEW.released_at IS NOT NULL THEN
		INSERT INTO lifecycle_writes VALUES (TG_TABLE_NAME, concat_ws('|', to_jsonb(NEW)->>TG_ARGV[0], to_jsonb(NEW)->>TG_ARGV[1]),
			'held', 'released', current_setting('application_name'));
	END IF;
	RETURN NEW;
END $$;
CREATE TRIGGER lifecycle AFTER UPDATE ON experiments FOR EACH ROW EXECUTE FUNCTION lifecycle_state('state', 'id');
CREATE TRIGGER lifecycle AFTER UPDATE ON cells FOR EACH ROW EXECUTE FUNCTION lifecycle_state('state', 'id');
CREATE TRIGGER lifecycle_rollout AFTER UPDATE ON cells FOR EACH ROW EXECUTE FUNCTION lifecycle_rollout();
CREATE TRIGGER lifecycle AFTER UPDATE ON trials FOR EACH ROW EXECUTE FUNCTION lifecycle_state('state', 'id');
CREATE TRIGGER lifecycle AFTER UPDATE ON gradings FOR EACH ROW EXECUTE FUNCTION lifecycle_state('state', 'id');
CREATE TRIGGER lifecycle AFTER UPDATE ON comparisons FOR EACH ROW EXECUTE FUNCTION lifecycle_state('state', 'id');
CREATE TRIGGER lifecycle AFTER UPDATE ON usage_records FOR EACH ROW EXECUTE FUNCTION lifecycle_state('status', 'experiment', 'request_id');
CREATE TRIGGER lifecycle AFTER UPDATE ON experiment_usage FOR EACH ROW EXECUTE FUNCTION lifecycle_release('experiment');
CREATE TRIGGER lifecycle AFTER UPDATE ON judge_keys FOR EACH ROW EXECUTE FUNCTION lifecycle_release('experiment', 'key_id');`

// witness is two stores over one world's database, each under an
// application name of its own, and the record of the steps they took.
type witness struct {
	w      *world
	stores [2]*postgres.Store
	apps   [2]string
	next   atomic.Int64
}

func newWitness(ctx context.Context, w *world) *witness {
	w.t.Helper()
	w.execIn(ctx, witnessSQL)
	l := &witness{w: w}
	for i := range l.stores {
		l.apps[i] = fmt.Sprintf("lifecycle-%d", i)
		u, err := url.Parse(w.dsn)
		if err != nil {
			w.t.Fatal(err)
		}
		q := u.Query()
		q.Set("application_name", l.apps[i])
		u.RawQuery = q.Encode()
		st, _, err := postgres.Open(ctx, u.String(), postgres.Options{})
		if err != nil {
			w.t.Fatal(err)
		}
		w.t.Cleanup(func() {
			if err := st.Close(); err != nil {
				w.t.Error(err)
			}
		})
		l.stores[i] = st
	}
	return l
}

// move runs a writer on one of the two stores, taking them in turn so two
// writers that start together run on different ones, and answers what the
// writer answered, or ErrIllegalTransition when it answered without error
// and took no step to the state: the record of its application holds no
// change of the record to it.
func (l *witness) move(ctx context.Context, table, key string, to any, what string, write func(*postgres.Store) error) error {
	i := int(l.next.Add(1) % 2)
	if err := write(l.stores[i]); err != nil {
		return err
	}
	var moved bool
	l.w.scanIn(ctx, &moved, `SELECT EXISTS (SELECT 1 FROM lifecycle_writes WHERE tbl = $1 AND key = $2 AND new = $3 AND app = $4)`,
		table, key, fmt.Sprint(to), l.apps[i])
	if !moved {
		return unchanged(what)
	}
	return nil
}

func TestExperimentWriters(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	l := newWitness(ctx, w)
	d := statemachinetest.Driver[objects.ExperimentState]{
		Enter: func(t *testing.T, s objects.ExperimentState) string {
			eid, _ := w.running(ctx, p, 2)
			w.execIn(ctx, `UPDATE experiments SET state = $2 WHERE id = $1`, eid, s)
			return eid
		},
		Move: func(eid string, to objects.ExperimentState) error {
			return l.move(ctx, "experiments", eid, to, "move the experiment", func(st *postgres.Store) error {
				return postgres.MoveExperiment(ctx, st, eid, to)
			})
		},
		State: func(t *testing.T, eid string) objects.ExperimentState {
			var s objects.ExperimentState
			w.scanIn(ctx, &s, `SELECT state FROM experiments WHERE id = $1`, eid)
			return s
		},
	}
	statemachinetest.Exhaustive(t, objects.ExperimentTable, d)
	statemachinetest.Race(t, objects.ExperimentTable, d)
}

func TestCellWriters(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	l := newWitness(ctx, w)
	d := statemachinetest.Driver[objects.CellState]{
		Enter: func(t *testing.T, s objects.CellState) string {
			_, cells := w.running(ctx, p, 2)
			w.execIn(ctx, `UPDATE cells SET state = $2 WHERE id = $1`, cells[0], s)
			return cells[0]
		},
		Move: func(cid string, to objects.CellState) error {
			return l.move(ctx, "cells", cid, to, "move the cell", func(st *postgres.Store) error {
				return postgres.MoveCell(ctx, st, cid, to)
			})
		},
		State: func(t *testing.T, cid string) objects.CellState {
			var s objects.CellState
			w.scanIn(ctx, &s, `SELECT state FROM cells WHERE id = $1`, cid)
			return s
		},
	}
	statemachinetest.Exhaustive(t, objects.CellTable, d)
	statemachinetest.Race(t, objects.CellTable, d)
}

// The rollouts of a cell move through the operations that submit and end
// them: a submit, and a park, which ends every rollout the cell has open.
func TestRolloutWriters(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	l := newWitness(ctx, w)
	d := statemachinetest.Driver[objects.RolloutState]{
		Enter: func(t *testing.T, s objects.RolloutState) string {
			_, cells := w.running(ctx, p, 2)
			w.execIn(ctx, `UPDATE cells SET rollouts = jsonb_set(rollouts, '{0,state}', to_jsonb($2::text)) WHERE id = $1`, cells[0], string(s))
			return cells[0]
		},
		Move: func(cid string, to objects.RolloutState) error {
			switch to {
			case objects.RolloutSubmitted:
				return l.move(ctx, "rollouts", cid, to, "submit the rollout", func(st *postgres.Store) error {
					return st.Experiments().SubmitRollout(ctx, cid, cid+".smoke")
				})
			case objects.RolloutEnded:
				return l.move(ctx, "rollouts", cid, to, "end the rollout", func(st *postgres.Store) error {
					return st.Experiments().Park(ctx, cid, objects.CellBlocked, "test")
				})
			case objects.RolloutQueued:
				return noWriter("queue a rollout")
			}
			return noWriter(string(to))
		},
		State: func(t *testing.T, cid string) objects.RolloutState {
			var s objects.RolloutState
			w.scanIn(ctx, &s, `SELECT rollouts->0->>'state' FROM cells WHERE id = $1`, cid)
			return s
		},
	}
	statemachinetest.Exhaustive(t, objects.RolloutTable, d)
	statemachinetest.Race(t, objects.RolloutTable, d)
}

// trialWriters are the writers of trials.state a driver takes a trial to a
// state with: a lease or a claim for running, and a result or a cancel for
// done. The lapse of a lease takes it back to pending.
type trialWriters struct {
	running func(ctx context.Context, st *postgres.Store, eid, cid string) error
	done    func(ctx context.Context, st *postgres.Store, eid, tid string) error
}

func TestTrialWriters(t *testing.T) {
	for name, tw := range map[string]trialWriters{
		"lease and result": {
			running: func(ctx context.Context, st *postgres.Store, eid, cid string) error {
				_, err := st.Experiments().Lease(ctx, store.LeaseRequest{Experiment: eid, Subject: owner, Cells: []string{cid}, Max: 1})
				return err
			},
			done: func(ctx context.Context, st *postgres.Store, _, tid string) error {
				_, err := st.Experiments().End(ctx, tid, outcome(objects.OutcomeCompleted), false)
				return err
			},
		},
		"claim and cancel": {
			running: func(ctx context.Context, st *postgres.Store, eid, cid string) error {
				_, err := st.Experiments().Claim(ctx, eid, []string{cid})
				return err
			},
			done: func(ctx context.Context, st *postgres.Store, eid, _ string) error {
				_, err := st.Experiments().Cancel(ctx, eid)
				return err
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			p := w.plan()
			ctx := t.Context()
			l := newWitness(ctx, w)
			type place struct{ eid, cid string }
			var mu sync.Mutex
			places := map[string]place{}
			var seq atomic.Int64
			d := statemachinetest.Driver[objects.TrialState]{
				Enter: func(t *testing.T, s objects.TrialState) string {
					eid, cells := w.running(ctx, p, 2)
					trials, err := w.st.Experiments().Trials(ctx, eid, store.TrialFilter{Cells: []string{cells[0]}}, store.Page{Limit: 10})
					if err != nil || len(trials) != 1 {
						t.Fatalf("the trials of the cell: %v %v", trials, err)
					}
					tid := trials[0].ID
					switch s {
					case objects.TrialRunning:
						// A lease that already lapsed, so the lapse has a trial to return.
						w.execIn(ctx, `UPDATE trials SET state = $2, lease_id = $3, lease_subject = $4, lease_expires_at = now() - interval '1 minute' WHERE id = $1`,
							tid, s, fmt.Sprintf("lse_%d", seq.Add(1)), owner)
					case objects.TrialDone:
						w.execIn(ctx, `UPDATE trials SET state = $2, outcome = 'completed', ended_at = now() WHERE id = $1`, tid, s)
					case objects.TrialPending, objects.TrialChecking:
						// A new trial is pending; no writer makes a checking one.
					}
					mu.Lock()
					places[tid] = place{eid, cells[0]}
					mu.Unlock()
					return tid
				},
				Move: func(tid string, to objects.TrialState) error {
					mu.Lock()
					at := places[tid]
					mu.Unlock()
					switch to {
					case objects.TrialRunning:
						return l.move(ctx, "trials", tid, to, "take the trial", func(st *postgres.Store) error { return tw.running(ctx, st, at.eid, at.cid) })
					case objects.TrialPending:
						return l.move(ctx, "trials", tid, to, "lapse the lease", func(st *postgres.Store) error {
							_, err := st.Experiments().ExpireLeases(ctx)
							return err
						})
					case objects.TrialDone:
						return l.move(ctx, "trials", tid, to, "end the trial", func(st *postgres.Store) error { return tw.done(ctx, st, at.eid, tid) })
					case objects.TrialChecking:
						return noWriter("check a trial")
					}
					return noWriter(string(to))
				},
				State: func(t *testing.T, tid string) objects.TrialState {
					var s objects.TrialState
					w.scanIn(ctx, &s, `SELECT state FROM trials WHERE id = $1`, tid)
					return s
				},
			}
			statemachinetest.Exhaustive(t, objects.TrialTable, d)
			statemachinetest.Race(t, objects.TrialTable, d)
		})
	}
}

// A grading ends with the result of its run, or not_run when its cell parks:
// the two writers of gradings.state.
func TestGradingWriters(t *testing.T) {
	for name, park := range map[string]bool{"end": false, "park": true} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			p := w.plan()
			ctx := t.Context()
			l := newWitness(ctx, w)
			var mu sync.Mutex
			cells := map[string]string{}
			var seq atomic.Int64
			d := statemachinetest.Driver[objects.GradingState]{
				Enter: func(t *testing.T, s objects.GradingState) string {
					eid, cs := w.running(ctx, p, 2)
					var tid string
					w.scanIn(ctx, &tid, `SELECT id FROM trials WHERE cell = $1`, cs[0])
					gid := fmt.Sprintf("%s.%d", tid, seq.Add(1))
					w.execIn(ctx, `INSERT INTO gradings (id, owner, experiment, cell, trial, grader, kind, position, state, due_at, created_at, updated_at, ended_at)
						VALUES ($1, $2, $3, $4, $5, $6, 'check', 0, $7, now(), now(), now(), CASE WHEN $7 = 'pending' THEN NULL ELSE now() END)`,
						gid, owner, eid, cs[0], tid, "sha256:"+strings.Repeat("c", 64), string(s))
					mu.Lock()
					cells[gid] = cs[0]
					mu.Unlock()
					return gid
				},
				Move: func(gid string, to objects.GradingState) error {
					mu.Lock()
					cid := cells[gid]
					mu.Unlock()
					switch {
					case to == objects.GradingPending:
						return noWriter("queue a grading")
					case park && to == objects.GradingNotRun:
						return l.move(ctx, "gradings", gid, to, "park the cell", func(st *postgres.Store) error {
							return st.Experiments().Park(ctx, cid, objects.CellBlocked, "test")
						})
					}
					return l.move(ctx, "gradings", gid, to, "end the grading", func(st *postgres.Store) error {
						return st.Gradings().End(ctx, store.GradingEnd{ID: gid, State: to, Record: map[string]string{"state": string(to)}})
					})
				},
				State: func(t *testing.T, gid string) objects.GradingState {
					var s objects.GradingState
					w.scanIn(ctx, &s, `SELECT state FROM gradings WHERE id = $1`, gid)
					return s
				},
			}
			statemachinetest.Exhaustive(t, objects.GradingTable, d)
			statemachinetest.Race(t, objects.GradingTable, d)
		})
	}
}

func TestComparisonWriters(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	l := newWitness(ctx, w)
	d := statemachinetest.Driver[objects.ComparisonState]{
		Enter: func(t *testing.T, s objects.ComparisonState) string {
			eid, _ := w.running(ctx, p, 2)
			cs, err := w.st.Experiments().Comparisons(ctx, store.ComparisonFilter{Experiment: eid}, store.Page{Limit: 1})
			if err != nil || len(cs) != 1 {
				t.Fatalf("the comparisons of the experiment: %v %v", cs, err)
			}
			w.execIn(ctx, `UPDATE comparisons SET state = $2 WHERE id = $1`, cs[0].ID, s)
			return cs[0].ID
		},
		Move: func(cid string, to objects.ComparisonState) error {
			if to == objects.ComparisonComputing {
				return noWriter("compute a comparison again")
			}
			return l.move(ctx, "comparisons", cid, to, "store the results", func(st *postgres.Store) error {
				return st.Experiments().Computed(ctx, cid, to, objects.Computed{Confounds: []string{}}, nil)
			})
		},
		State: func(t *testing.T, cid string) objects.ComparisonState {
			var s objects.ComparisonState
			w.scanIn(ctx, &s, `SELECT state FROM comparisons WHERE id = $1`, cid)
			return s
		},
	}
	statemachinetest.Exhaustive(t, objects.ComparisonTable, d)
	statemachinetest.Race(t, objects.ComparisonTable, d)
}

// A call's status mirrors Lux's record of it: a read that finds the record
// changed stores the status it now holds.
func TestUsageStatusWriters(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	l := newWitness(ctx, w)
	var seq atomic.Int64
	keep := func(st *postgres.Store, eid, req string, s objects.UsageStatus) error {
		rec := store.UsageRecord{RequestID: req, At: time.Now().UTC(), Status: s, Tokens: jsontext.Value(`{}`), Priced: true,
			Record: jsontext.Value(fmt.Sprintf(`{"n":%d}`, seq.Add(1)))}
		return st.Usage().Read(ctx, eid, []store.UsageRecord{rec}, time.Now().UTC(), false)
	}
	d := statemachinetest.Driver[objects.UsageStatus]{
		Enter: func(t *testing.T, s objects.UsageStatus) string {
			eid, _ := w.running(ctx, p, 2)
			req := fmt.Sprintf("req_%d", seq.Add(1))
			if err := keep(w.st, eid, req, s); err != nil {
				t.Fatal(err)
			}
			return eid + "|" + req
		},
		Move: func(key string, to objects.UsageStatus) error {
			eid, req, _ := strings.Cut(key, "|")
			return l.move(ctx, "usage_records", key, to, "keep the record", func(st *postgres.Store) error { return keep(st, eid, req, to) })
		},
		State: func(t *testing.T, key string) objects.UsageStatus {
			eid, req, _ := strings.Cut(key, "|")
			var s objects.UsageStatus
			w.scanIn(ctx, &s, `SELECT status FROM usage_records WHERE experiment = $1 AND request_id = $2`, eid, req)
			return s
		},
	}
	statemachinetest.Exhaustive(t, objects.UsageStatusTable, d)
	statemachinetest.Race(t, objects.UsageStatusTable, d)
}

// A Key is released once: the release sets released_at where it is NULL, and
// the release of a released Key answers without error and changes nothing.
func TestKeyReleaseWriters(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	l := newWitness(ctx, w)
	var seq atomic.Int64
	t.Run("experiment", func(t *testing.T) {
		d := statemachinetest.Driver[objects.KeyState]{
			Enter: func(t *testing.T, s objects.KeyState) string {
				eid, _ := w.running(ctx, p, 2)
				if err := w.st.Usage().Hold(ctx, eid, owner, "key_1"); err != nil {
					t.Fatal(err)
				}
				if s == objects.KeyReleased {
					if err := w.st.Usage().Released(ctx, eid); err != nil {
						t.Fatal(err)
					}
				}
				return eid
			},
			Move: func(eid string, to objects.KeyState) error {
				if to == objects.KeyHeld {
					return noWriter("hold a released key")
				}
				return l.move(ctx, "experiment_usage", eid, to, "release the key", func(st *postgres.Store) error { return st.Usage().Released(ctx, eid) })
			},
			State: func(t *testing.T, eid string) objects.KeyState {
				var s objects.KeyState
				w.scanIn(ctx, &s, `SELECT CASE WHEN released_at IS NULL THEN 'held' ELSE 'released' END FROM experiment_usage WHERE experiment = $1`, eid)
				return s
			},
		}
		statemachinetest.Exhaustive(t, objects.ExperimentKeyTable, d)
		statemachinetest.Race(t, objects.ExperimentKeyTable, d)
	})
	t.Run("judge", func(t *testing.T) {
		d := statemachinetest.Driver[objects.KeyState]{
			Enter: func(t *testing.T, s objects.KeyState) string {
				eid, _ := w.running(ctx, p, 2)
				kid := fmt.Sprintf("key_%d", seq.Add(1))
				if err := w.st.Gradings().HoldKey(ctx, eid, "sha256:"+strings.Repeat("d", 64), kid); err != nil {
					t.Fatal(err)
				}
				if s == objects.KeyReleased {
					if err := w.st.Gradings().ReleasedKey(ctx, eid, kid); err != nil {
						t.Fatal(err)
					}
				}
				return eid + "|" + kid
			},
			Move: func(key string, to objects.KeyState) error {
				if to == objects.KeyHeld {
					return noWriter("hold a released key")
				}
				eid, kid, _ := strings.Cut(key, "|")
				return l.move(ctx, "judge_keys", key, to, "release the key", func(st *postgres.Store) error { return st.Gradings().ReleasedKey(ctx, eid, kid) })
			},
			State: func(t *testing.T, key string) objects.KeyState {
				eid, kid, _ := strings.Cut(key, "|")
				var s objects.KeyState
				w.scanIn(ctx, &s, `SELECT CASE WHEN released_at IS NULL THEN 'held' ELSE 'released' END FROM judge_keys WHERE experiment = $1 AND key_id = $2`, eid, kid)
				return s
			},
		}
		statemachinetest.Exhaustive(t, objects.JudgeKeyTable, d)
		statemachinetest.Race(t, objects.JudgeKeyTable, d)
	})
}

// A writer refuses the step its table lacks, wherever the record is
// reached, and leaves the record as it was.
func TestWritersRefuseStepsTheTablesLack(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	x := w.st.Experiments()
	refused := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, statemachine.ErrIllegalTransition) {
			t.Errorf("%s = %v, want ErrIllegalTransition", what, err)
		}
	}
	eid, cells := w.running(ctx, p, 2)

	// A rollout that ended is not submitted again.
	if err := x.Park(ctx, cells[0], objects.CellBlocked, "test"); err != nil {
		t.Fatal(err)
	}
	refused("submit an ended rollout", postgres.MoveRollout(ctx, w.st, cells[0], cells[0]+".smoke", objects.RolloutSubmitted))

	// A grading is not made pending again by its own end.
	var tid string
	w.scanIn(ctx, &tid, `SELECT id FROM trials WHERE cell = $1`, cells[1])
	gid := tid + ".0"
	w.execIn(ctx, `INSERT INTO gradings (id, owner, experiment, cell, trial, grader, kind, position, state, due_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'check', 0, 'pending', now(), now(), now())`, gid, owner, eid, cells[1], tid, "sha256:"+strings.Repeat("e", 64))
	refused("end a grading pending", w.st.Gradings().End(ctx, store.GradingEnd{ID: gid, State: objects.GradingPending, Record: map[string]string{}}))

	// A trial no table state describes is not given a result.
	w.execIn(ctx, `UPDATE trials SET state = $2 WHERE id = $1`, tid, objects.TrialChecking)
	_, err := x.End(ctx, tid, outcome(objects.OutcomeCompleted), false)
	refused("end a checking trial", err)

	// A comparison is not computed again, and one starts computing.
	cs, err := x.Comparisons(ctx, store.ComparisonFilter{Experiment: eid}, store.Page{Limit: 1})
	if err != nil || len(cs) != 1 {
		t.Fatalf("the comparisons of the experiment: %v %v", cs, err)
	}
	if err := x.Computed(ctx, cs[0].ID, objects.ComparisonComplete, objects.Computed{Confounds: []string{}}, nil); err != nil {
		t.Fatal(err)
	}
	refused("compute a complete comparison again", x.Computed(ctx, cs[0].ID, objects.ComparisonComputing, objects.Computed{Confounds: []string{}}, nil))
	refused("store a comparison that starts complete", x.CreateComparison(ctx, objects.Comparison{ID: id.New(id.Comparison), Owner: owner, Experiment: eid,
		Candidate: p.b.Digest, ReferenceCandidate: p.a.Digest, State: objects.ComparisonComplete}))

	// A status Lux never wrote is not kept.
	bogus, ok := objects.ParseUsageStatus("not-a-status")
	if ok {
		t.Fatal("ParseUsageStatus accepted a status Lux never wrote")
	}
	rec := store.UsageRecord{RequestID: "req_bogus", At: time.Now().UTC(), Status: bogus, Tokens: jsontext.Value(`{}`), Record: jsontext.Value(`{}`)}
	refused("keep a record with no status", w.st.Usage().Read(ctx, eid, []store.UsageRecord{rec}, time.Now().UTC(), false))
}
