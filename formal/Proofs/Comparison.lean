import Machines.Comparison

/-!
Claims about the comparison table, proved over the table that
`internal/objects.ComparisonTable` renders into `Machines/Comparison.lean`.
-/

namespace Proofs.Comparison
open Machines.Comparison Statemachine

/-- A comparison never computes again from the start: no state has a step to
`computing`. -/
theorem computing_is_only_the_start :
    ∀ s ∈ machine.states, State.«computing» ∉ machine.next s := by
  decide

/-- A comparison that has a result is never computing again, whatever
recomputations follow. -/
theorem result_never_computing (s : State) (r : Reaches machine .«complete» s) :
    s ≠ .«computing» := by
  have := machine.reaches_closed (I := [State.«complete», .«failed»]) (by decide) r (by decide)
  intro h; subst h; simp at this

/-- Concurrent writers that check the state they land on keep the table,
whatever they read and in whatever order their writes land. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.Comparison
