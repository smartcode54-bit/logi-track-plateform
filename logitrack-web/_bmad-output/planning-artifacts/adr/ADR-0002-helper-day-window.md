# ADR-0002: Helper / training-day pay — the "day" is a 12:00→11:59 work window

- **Status:** Accepted (2026-06-26)
- **Context owners:** Driver Compensation
- **Amends:** [ADR-0001](ADR-0001-helper-pay-data-model.md) decision 5 (helper-day was counted by the Bangkok calendar day `tasks.date`). The data model in ADR-0001 is unchanged; only the **definition of "one day"** changes.

## Context

ADR-0001 counted a helper-day by the Bangkok **calendar day** of `tasks.date` (00:00–23:59). Operations span midnight: a driver who picks up the first task in the afternoon and finishes the last task after midnight is doing **one** work day, but a calendar-day boundary splits it into two and pays twice. The 2026-06-26 grilling session redefined "one day" as a **shifted work window** that brackets a single operational day.

### Decisions locked in the grilling session

1. **A "helper-day" is the window 12:00:00 of day D → 11:59:59 of day D+1** (Asia/Bangkok, UTC+7), **keyed to day D** (the 12:00 side).
2. **The window is determined by the main driver's task `checkInAt`.** check-in is the single, always-present-when-a-helper-exists timestamp that anchors the work day. (`deliveredAt` was considered and rejected — see below.)
3. **Fallback:** if a task has no `checkInAt`, use `tasks.date` at 12:00 of that date to derive the window key. (Legacy/missing-data guard; `helperDriverIds` is normally written at check-in so `checkInAt` is normally present.)
4. **One helper-day per window**, regardless of how many tasks/check-ins the helper rode on in that window. First task → last task = one day = one unit.
5. **A helper still earns the day even if no trip was delivered** in that window (cancellations, breakdowns). check-in is proof the helper worked; a failed delivery is not the helper's fault.
6. **No double pay:** if, within the same window, the driver has their **own** assigned task / delivered trip, that window pays as a **trip**, not as a helper-day.
7. **Round assignment** follows the **window key day D**. A check-in at 11:00 on the 16th falls in the window keyed to the 15th → R1, not R2. A check-in at 13:00 on the 15th is keyed to the 15th → R1.

## Decision

Replace the calendar-day key with a **work-window key**:

```
windowKey(t) = bangkokDateKey( t - 12h )            // shift back 12h, then take the Bangkok date
  where t = task.checkInAt  ?? atBangkokNoon(task.date)
```

Equivalently: timestamps from **12:00 of D** up to **11:59:59 of D+1** all map to key **D**.

- Helper pay = tasks where `helperDriverIds array-contains <authId>`, mapped to `windowKey`, de-duplicated to **one helper-day per window key**, **minus** any window key in which the driver had their own assigned task / delivered trip, × `helperDayRateThb`.
- Round membership is decided by the window key day, not by the raw timestamp.
- The data model (`tasks.helperDriverIds`, the `tasks(helperDriverIds CONTAINS, date ASC)` index) is **unchanged**. `date` still bounds the query range; the window key is computed in the orchestrator after the read. Query range must be widened by ±12h at the round edges so edge windows are not missed.

## Alternatives considered (and rejected)

1. **`deliveredAt` of the first→last delivered trip defines the window** (the literal first reading of the request).
   - Rejected: `deliveredAt` is absent when every trip is cancelled, so a helper on a failed day would earn nothing (contradicts decision 5). Multiple deliveries can also straddle two windows, producing 2 days for one work day (contradicts decision 4). `checkInAt` is single per task and always present when a helper is assigned.

2. **Keep the calendar-day key (`tasks.date`, ADR-0001).**
   - Rejected: splits a midnight-crossing operational day into two paid days. The whole point of this ADR.

3. **Key to the 11:59 side (day D+1) instead of the 12:00 side.**
   - Rejected: the operator's mental model is "the day the shift started." Keying to D matches how the work day is named and reported.

## Consequences

- **Positive:** one operational day = one helper-day even across midnight; helpers are paid for showing up even when deliveries fail; round assignment is deterministic at the boundary.
- **Negative / follow-ups:**
  - The orchestrator query range must be widened ±12h at round edges, and the window key computed post-read — `tasks.date` alone no longer decides round membership. A check-in late on the last day of a round can pull into / out of the round vs. the previous logic; recompute (ADR-0001 decision 6) must use the same window logic.
  - The "own delivered trip / own task" exclusion (decision 6) is evaluated **per window key**: the driving-window set is the union of delivered-trip windows (by `deliveredAt`) and the driver's own assigned-task windows (by the same `checkInAt`/`tasks.date`-noon anchor). The own-task arm covers tasks that produced no delivered trip (e.g. all stops cancelled) — that window still pays as driving, not as a helper-day.
  - `checkInAt` is `z.any().optional()` in both task schemas; the compute path must coerce it safely and apply the `tasks.date`-noon fallback.

Tracked in [Story 3.6](../epics.md) (helper / training-day pay).
