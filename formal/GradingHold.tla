----------------------------- MODULE GradingHold -----------------------------
(***************************************************************************)
(* The hold on a grading (internal/store/postgres/gradings.go): replicas   *)
(* take the gradings due and run them. A hold only stamps held_until, so a *)
(* grading a replica holds is due again once its hold lapses, and another  *)
(* replica may take it then. A run that outlives its hold is run by two    *)
(* replicas at once. Time is in whole minutes. Hold and Budget are the     *)
(* hold of a job and the longest its run takes, set by the cfg from the Go *)
(* constants that TestGradingHoldConstants renders into                    *)
(* GradingHoldConstants.tla. A job of several check rows holds and ends    *)
(* them together, so the model keeps one row to a job.                     *)
(***************************************************************************)
EXTENDS Integers, FiniteSets, GradingHoldConstants

CONSTANTS Gradings, Replicas, Horizon, Hold, Budget

None == "none"
Never == -1

VARIABLES
    clock,    \* minutes since the start
    held,     \* grading -> the clock of its last hold, or Never
    ended,    \* grading -> TRUE once a run ended it
    running,  \* replica -> the grading it is running, or None
    elapsed   \* replica -> minutes spent on the current run

vars == <<clock, held, ended, running, elapsed>>

Due(g) == /\ ~ended[g]
          /\ \/ held[g] = Never
             \/ clock - held[g] >= Hold

Init == /\ clock = 0
        /\ held = [g \in Gradings |-> Never]
        /\ ended = [g \in Gradings |-> FALSE]
        /\ running = [r \in Replicas |-> None]
        /\ elapsed = [r \in Replicas |-> 0]

\* Hold: stamp a due grading and start its run. The statement takes a row
\* only while it is pending and its hold has lapsed.
Take(r) ==
    /\ running[r] = None
    /\ \E g \in Gradings :
        /\ Due(g)
        /\ held' = [held EXCEPT ![g] = clock]
        /\ running' = [running EXCEPT ![r] = g]
    /\ elapsed' = [elapsed EXCEPT ![r] = 0]
    /\ UNCHANGED <<clock, ended>>

\* The run ends within Budget minutes. It ends the grading, or fails and
\* leaves it pending to be taken again when its hold lapses. Ending a grading
\* another run already ended changes nothing.
Finish(r) ==
    /\ running[r] /= None
    /\ \E ok \in BOOLEAN :
        ended' = IF ok THEN [ended EXCEPT ![running[r]] = TRUE] ELSE ended
    /\ running' = [running EXCEPT ![r] = None]
    /\ UNCHANGED <<clock, held, elapsed>>

\* A minute passes, and a run that has used its whole budget ends first.
Tick ==
    /\ clock < Horizon
    /\ \A r \in Replicas : running[r] /= None => elapsed[r] < Budget
    /\ clock' = clock + 1
    /\ elapsed' = [r \in Replicas |-> IF running[r] /= None THEN elapsed[r] + 1 ELSE elapsed[r]]
    /\ UNCHANGED <<held, ended, running>>

\* The model stops at Horizon minutes.
Stop == clock = Horizon /\ UNCHANGED vars

Next == Tick \/ Stop \/ \E r \in Replicas : Take(r) \/ Finish(r)

Spec == Init /\ [][Next]_vars

\* No grading is run by two replicas at once.
Exclusive ==
    \A r \in Replicas : running[r] /= None =>
        \A s \in Replicas \ {r} : running[s] /= running[r]

TypeOK == /\ clock \in 0..Horizon
          /\ \A r \in Replicas : elapsed[r] \in 0..Budget

\* A run longer than its hold: 10 minutes held, 20 minutes to run.
\* GradingHold.overlong.cfg uses these.
OverlongHold == 10
OverlongBudget == 20
=============================================================================
