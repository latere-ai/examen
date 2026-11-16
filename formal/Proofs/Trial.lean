import Machines.Trial

/-!
Claims about the trial table, proved over the table that
`internal/objects.TrialTable` renders into `Machines/Trial.lean`.
-/

namespace Proofs.Trial
open Machines.Trial Statemachine

/-- A done trial stays done: nothing follows `done`, so a result is never
replaced and a retry is a new trial. -/
theorem done_stays (s : State) (r : Reaches machine .«done» s) :
    s = .«done» :=
  machine.terminal_stays terminal_done r

/-- A trial returns to pending only from running, by the lapse of its lease. -/
theorem pending_again_only_from_running :
    ∀ s ∈ machine.states, State.«pending» ∈ machine.next s → s = .«running» := by
  decide

/-- Concurrent writers that check the state they land on keep the table,
whatever they read and in whatever order their writes land. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.Trial
