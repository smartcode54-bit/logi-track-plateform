/** Presentation only: callers translate labels and retain the domain's status values and rules. */
export type StatusTone = "success" | "warning" | "destructive" | "primary" | "neutral";

const BADGE_VARIANTS = {
    success: "success",
    warning: "warning",
    destructive: "destructive",
    primary: "default",
    neutral: "secondary",
} as const;

const TEXT_CLASSES = {
    success: "text-success",
    warning: "text-warning",
    destructive: "text-destructive",
    primary: "text-primary",
    neutral: "text-muted-foreground",
} as const;

export function toneBadgeVariant(tone: StatusTone) {
    return BADGE_VARIANTS[tone];
}

export function toneTextClass(tone: StatusTone) {
    return TEXT_CLASSES[tone];
}

// API values and the existing form/legacy spellings share a presentation, without rewriting records.
function statusKey(status: string | null | undefined): string {
    return (status ?? "").trim().toLowerCase().replace(/[- ]/g, "_");
}

export function truckStatusTone(status: string | null | undefined): StatusTone {
    switch (statusKey(status)) {
        case "active":
        case "available": return "success";
        case "maintenance":
        case "in_transit": return "warning";
        case "insurance_claim": return "destructive";
        default: return "neutral";
    }
}

export function driverStatusTone(status: string | null | undefined): StatusTone {
    switch (statusKey(status)) {
        case "active":
        case "on_duty": return "success";
        default: return "neutral";
    }
}

export function taskStatusTone(status: string | null | undefined): StatusTone {
    switch (statusKey(status)) {
        case "assigned":
        case "checked_in": return "primary";
        case "in_transit": return "warning";
        case "completed": return "success";
        default: return "neutral";
    }
}

export function carrierStatusTone(status: string | null | undefined): StatusTone {
    switch (statusKey(status)) {
        case "active": return "success";
        case "pending": return "warning";
        default: return "neutral";
    }
}
