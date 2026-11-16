import Machines.Experiment

/-!
Claims about the experiment table, proved over the table that
`internal/objects.ExperimentTable` renders into `Machines/Experiment.lean`.
-/

namespace Proofs.Experiment
open Machines.Experiment Statemachine

/-- A canceled experiment stays canceled: nothing follows `canceled`. -/
theorem canceled_stays (s : State) (r : Reaches machine .«canceled» s) :
    s = .«canceled» :=
  machine.terminal_stays terminal_canceled r

/-- A complete experiment leaves `complete` only by the reopening of a
retried cell, to `running`. -/
theorem complete_only_reopens : machine.next State.«complete» = [State.«running»] := by
  decide

/-- An experiment completes only from grading. -/
theorem complete_only_after_grading :
    ∀ s ∈ machine.states, State.«complete» ∈ machine.next s → s = .«grading» := by
  decide

/-- Concurrent writers that check the state they land on keep the table,
whatever they read and in whatever order their writes land. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.Experiment
