import Machines.Cell

/-!
Claims about the cell table, proved over the table that
`internal/objects.CellTable` renders into `Machines/Cell.lean`.
-/

namespace Proofs.Cell
open Machines.Cell Statemachine

/-- A canceled cell stays canceled: nothing follows `canceled`. -/
theorem canceled_stays (s : State) (r : Reaches machine .«canceled» s) :
    s = .«canceled» :=
  machine.terminal_stays terminal_canceled r

/-- A complete cell is never run again: nothing follows `complete`. -/
theorem complete_never_runs (s : State) (r : Reaches machine .«complete» s) :
    s ≠ .«running» := by
  have := machine.terminal_stays terminal_complete r
  subst this; decide

/-- A parked cell leaves only by a retry, to smoking or running or, when no
trial needs a run, to complete, or by the cancel of its experiment. -/
theorem parked_leaves_by_retry_or_cancel :
    ∀ s ∈ [State.«blocked», .«over_budget»], ∀ t ∈ machine.next s,
      t ∈ [State.«smoking», .«running», .«complete», .«canceled»] := by
  decide

/-- A cell is graded only after its trials ran: grading follows smoking or
running. -/
theorem grading_only_after_a_run :
    ∀ s ∈ machine.states, State.«grading» ∈ machine.next s → s ∈ [State.«smoking», .«running»] := by
  decide

/-- Concurrent writers that check the state they land on keep the table,
whatever they read and in whatever order their writes land. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.Cell
