import type { MaintenanceStatus } from "../api/maintenance.go";

const transitions: Partial<Record<MaintenanceStatus, readonly MaintenanceStatus[]>> = {
    pm_booking: ["scheduled", "in_progress", "completed", "cancelled"],
    scheduled: ["pm_booking", "in_progress", "completed", "cancelled"],
    in_progress: ["completed", "cancelled"],
};

export function canonicalMaintenanceStatus(status: string): MaintenanceStatus {
    return status.trim().toLowerCase().replace(/\s+/g, "_") as MaintenanceStatus;
}

/** Mirrors internal/maintenance/records.go CanTransition; both terminal states allow only same-status edits. */
export function canTransitionMaintenance(from: string, to: string): boolean {
    const current = canonicalMaintenanceStatus(from);
    const next = canonicalMaintenanceStatus(to);
    return current === next || (transitions[current]?.includes(next) ?? false);
}
