import Machines.Grading

/-!
Claims about the grading table, proved over the table that
`internal/objects.GradingTable` renders into `Machines/Grading.lean`.
-/

namespace Proofs.Grading
open Machines.Grading Statemachine

/-- Only a pending grading has a step: every end is final. -/
theorem only_pending_has_steps :
    ∀ s ∈ machine.states, machine.next s ≠ [] → s = .«pending» := by
  decide

/-- A grading that ended ok keeps its result: nothing follows `ok`. -/
theorem ok_stays (s : State) (r : Reaches machine .«ok» s) :
    s = .«ok» :=
  machine.terminal_stays terminal_ok r

/-- A grading that ended without a run, because its cell parked, is never
run later: nothing follows `not_run`. -/
theorem not_run_stays (s : State) (r : Reaches machine .«not_run» s) :
    s = .«not_run» :=
  machine.terminal_stays terminal_not_run r

/-- Concurrent writers that check the state they land on keep the table,
whatever they read and in whatever order their writes land. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.Grading
