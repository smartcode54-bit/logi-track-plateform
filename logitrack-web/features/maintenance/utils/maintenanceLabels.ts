import { canonicalMaintenanceStatus } from "./maintenanceTransitions";

type Translate = (key: string) => string;
const statusKeys: Record<string, string> = {
    pm_booking: "maintenance.pmBookingBadge", scheduled: "maintenance.scheduledBadge",
    in_progress: "maintenance.form.inProgress", completed: "maintenance.form.completed", cancelled: "maintenance.form.cancelled",
};
const serviceCodes = new Set(["periodic_check", "tire_change", "tire_rotation", "brake_service"]);
const truckStatusKeys: Record<string, string> = {
    active: "trucks.detail.status.active", available: "trucks.detail.status.available",
    inactive: "trucks.detail.status.inactive", maintenance: "trucks.detail.status.maintenance",
    insurance_claim: "trucks.detail.status.insuranceClaim", sold: "trucks.detail.status.sold",
    in_transit: "trucks.status.inTransit",
};

export function maintenanceStatusLabel(status: string, t: Translate): string {
    return t(statusKeys[canonicalMaintenanceStatus(status)] ?? "maintenance.go.unknownStatus");
}

/** Only known codes are translated; descriptions authored by a user remain verbatim. */
export function maintenanceServiceLabel(service: string, t: Translate): string {
    return serviceCodes.has(service) ? t(`maintenance.service.${service}`) : service;
}

export function maintenanceTruckStatusLabel(status: string, t: Translate): string {
    return t(truckStatusKeys[status] ?? "maintenance.go.unknownStatus");
}
