import Machines.UsageStatus

/-!
Claims about the usage status table, proved over the table that
`internal/objects.UsageStatusTable` renders into `Machines/UsageStatus.lean`.
-/

namespace Proofs.UsageStatus
open Machines.UsageStatus Statemachine

/-- The status mirrors Lux's record: any status may be followed by either of
the others. -/
theorem any_status_follows_any_other :
    ∀ a ∈ machine.states, ∀ b ∈ machine.states, a ≠ b → b ∈ machine.next a := by
  decide

/-- A status never follows itself: a read that finds the record unchanged
writes nothing. -/
theorem no_status_follows_itself :
    ∀ a ∈ machine.states, a ∉ machine.next a := by
  decide

end Proofs.UsageStatus
