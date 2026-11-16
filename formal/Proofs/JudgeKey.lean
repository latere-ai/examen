import Machines.JudgeKey

/-!
Claims about the JudgeKey table, proved over the table that
`internal/objects.JudgeKeyTable` renders into `Machines/JudgeKey.lean`.
-/

namespace Proofs.JudgeKey
open Machines.JudgeKey Statemachine

/-- A released Key is never held again: nothing follows `released`. -/
theorem released_stays (s : State) (r : Reaches machine .«released» s) :
    s = .«released» :=
  machine.terminal_stays terminal_released r

/-- Only a held Key is released. -/
theorem released_only_from_held :
    ∀ s ∈ machine.states, State.«released» ∈ machine.next s → s = .«held» := by
  decide

/-- Concurrent writers that check the state they land on keep the table,
whatever they read and in whatever order their writes land. -/
theorem guarded_writers_follow (ws : List (Write State)) (row : State) :
    Reaches machine row (ws.foldl machine.guarded row) :=
  machine.guarded_follows ws row

end Proofs.JudgeKey
