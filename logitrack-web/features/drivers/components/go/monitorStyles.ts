/** Badge colours of the Go monitor (the Firestore page's, `DriverMonitorDashboard.tsx:101-112`). */
export const STATUS_COLOR: Record<string, string> = {
    in_transit: "bg-amber-500/15 text-amber-600 border-amber-500/25 dark:text-amber-400",
    incident: "bg-red-500/15 text-red-600 border-red-500/25 dark:text-red-400",
    delivered: "bg-emerald-500/15 text-emerald-600 border-emerald-500/25 dark:text-emerald-400",
    standby: "bg-orange-500/15 text-orange-600 border-orange-500/25 dark:text-orange-400",
    cancelled: "bg-zinc-500/15 text-zinc-500 border-zinc-500/25 dark:text-zinc-400",
};

export const JOB_TYPE_COLOR: Record<string, string> = {
    first_mile: "bg-indigo-500/15 text-indigo-600 border-indigo-500/25 dark:text-indigo-400",
    line_haul: "bg-orange-500/15 text-orange-600 border-orange-500/25 dark:text-orange-400",
};
