"use client";

import { AlertTriangle, Info, Shield } from "lucide-react";

import { Badge } from "@/components/ui/badge";

type Translate = (key: string, fallbackOrParams?: string | Record<string, string | number>) => string;

/** The label of an event type: its catalogue text, else the raw type (a legacy or future type). */
export function eventTypeLabel(type: string, t: Translate): string {
    return t(`securityCenter.securityEvents.types.${type}`, type);
}

export function severityIcon(severity: string) {
    if (severity === "critical" || severity === "warning") return AlertTriangle;
    if (severity === "info") return Info;
    return Shield;
}

export function severityColor(severity: string): string {
    if (severity === "critical") return "text-red-500";
    if (severity === "warning") return "text-amber-500";
    return "text-blue-500";
}

export function SeverityBadge({ severity, t }: { severity: string; t: Translate }) {
    const variant = severity === "critical" ? "destructive" : severity === "warning" ? "secondary" : "outline";
    return <Badge variant={variant}>{t(`securityCenter.severity.${severity}`, severity)}</Badge>;
}

/** `1234` -> `1.2k` for the overview's donut. */
export function formatCompactInt(n: number): string {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
    if (n >= 10_000) return `${Math.round(n / 1000)}k`;
    if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
    return String(n);
}

/** A timestamp of the API (RFC 3339) as a local date and time, "—" when absent or invalid. */
export function formatDateTime(value: string | null | undefined): string {
    if (!value) return "—";
    const d = new Date(value);
    return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString();
}
