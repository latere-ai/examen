import Machines.Rollout

/-!
Claims about the rollout table, proved over the table that
`internal/objects.RolloutTable` renders into `Machines/Rollout.lean`.
-/

namespace Proofs.Rollout
open Machines.Rollout Statemachine

/-- An ended rollout is never submitted again: nothing follows `ended`. -/
theorem ended_never_submitted (s : State) (r : Reaches machine .«ended» s) :
    s ≠ .«submitted» := by
  have := machine.terminal_stays terminal_ended r
  subst this; decide

/-- Only a queued rollout is submitted. -/
theorem submitted_only_from_queued :
    ∀ s ∈ machine.states, State.«submitted» ∈ machine.next s → s = .«queued» := by
  decide

/-- Concurrent writers that check the state they land on keep the table,
whatever they read and in whatever order their writes land. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.Rollout
