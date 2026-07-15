import { TASK_TRUCK_TYPE_ENUM, type TaskTruckType } from "@/validate/taskSchema";

/**
 * Maps the truck master's `type` (trucks/{id}.type — full words, set in VehicleDetailsSection)
 * to the abbreviated vehicle class carried on a task (tasks.truckType).
 *
 * Billing selects the rate card from tasks.truckType (see lib/billingCompute.ts), so this mapping
 * is the only place the two vocabularies are allowed to meet. Mirror of
 * logitrack-mobile/lib/core/utils/truck_type.dart — keep both in sync.
 *
 * "4 Wheels" is legacy (no longer offered in the truck form) and still maps to 4WJ.
 */
const TRUCK_DOC_TYPE_TO_TASK_TYPE: Record<string, TaskTruckType> = {
    "Pickup": "4W",
    "4 Wheels": "4WJ",
    "4 Wheels Jumbo": "4WJ",
    "6 Wheels": "6WH",
    "10 Wheels": "10WH",
    "18 Wheels": "18WH",
    "Van": "VAN",
};

function isTaskTruckType(value: string): value is TaskTruckType {
    return (TASK_TRUCK_TYPE_ENUM as readonly string[]).includes(value);
}

/**
 * Derives a task's truckType from a truck doc's `type`.
 * Returns undefined for an unknown/blank type — callers must not invent a class,
 * because a wrong class silently bills against the wrong rate card.
 */
export function taskTruckTypeFromTruckDoc(type: string | undefined | null): TaskTruckType | undefined {
    const raw = (type ?? "").trim();
    if (!raw) return undefined;
    const mapped = TRUCK_DOC_TYPE_TO_TASK_TYPE[raw];
    if (mapped) return mapped;
    // Some legacy truck docs already store the abbreviation.
    const upper = raw.toUpperCase();
    return isTaskTruckType(upper) ? upper : undefined;
}

/** True when a truck doc belongs to the given task truckType. Used to filter the plate picker. */
export function truckMatchesTaskTruckType(
    truckDocType: string | undefined | null,
    taskTruckType: TaskTruckType | undefined,
): boolean {
    if (!taskTruckType) return true;
    return taskTruckTypeFromTruckDoc(truckDocType) === taskTruckType;
}
