/** Shared semantic status tokens for the Go monitor and trip dialogs. */
export const STATUS_COLOR: Record<string, string> = {
    in_transit: "bg-warning/10 text-warning border-warning/25",
    incident: "bg-destructive/10 text-destructive border-destructive/25",
    delivered: "bg-success/10 text-success border-success/25",
    standby: "bg-warning/10 text-warning border-warning/25",
    cancelled: "bg-muted text-muted-foreground border-border",
};

export const JOB_TYPE_COLOR: Record<string, string> = {
    first_mile: "bg-muted text-foreground border-border",
    line_haul: "bg-muted text-foreground border-border",
};
