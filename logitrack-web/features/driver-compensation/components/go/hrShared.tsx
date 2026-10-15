"use client";

/**
 * Small pieces the Go HR pages share (T46): the error banner over `hrErrorText`, status badges, the
 * "load more" footer of a keyset list and the payroll round label. Nothing here imports Firebase.
 */
import { format, isValid, parseISO } from "date-fns";
import { enUS, th as thLocale } from "date-fns/locale";
import { Loader2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { type StatusTone, toneBadgeVariant } from "@/lib/statusPresentation";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

import { hrErrorText } from "../../api/hrErrors";

export function HrErrorAlert({ error, title, fieldLabels }: { error: unknown; title?: string; fieldLabels?: Readonly<Record<string, string>> }) {
    const { t } = useLanguage();
    if (!error) return null;
    return (
        <Alert variant="destructive" role="alert">
            <AlertDescription>
                {title ? <span className="font-medium">{title}: </span> : null}
                {hrErrorText(error, t, fieldLabels)}
            </AlertDescription>
        </Alert>
    );
}

const STATUS_TONES: Record<string, StatusTone> = {
    draft: "neutral",
    pending: "warning",
    pending_approval: "warning",
    partially_deducted: "primary",
    approved: "primary",
    paid: "success",
    cleared: "success",
    rejected: "destructive",
    cancelled: "destructive",
};

/** A status badge; `labelKey` is the catalogue key of the status's text. */
export function StatusBadge({ status, labelKey, className }: { status: string; labelKey: string; className?: string }) {
    const { t } = useLanguage();
    return (
        <Badge variant={toneBadgeVariant(STATUS_TONES[status] ?? "neutral")} className={className}>
            {t(labelKey, status)}
        </Badge>
    );
}

/** "Showing n of m" and the "load more" button of a keyset list. */
export function LoadMoreFooter({
    shown,
    total,
    hasNextPage,
    fetchingNext,
    onLoadMore,
}: {
    shown: number;
    total?: number;
    hasNextPage: boolean;
    fetchingNext: boolean;
    onLoadMore: () => void;
}) {
    const { t } = useLanguage();
    if (shown === 0) return null;
    return (
        <div className="flex items-center justify-between gap-4 border-t px-4 py-3 text-sm text-muted-foreground">
            <span data-testid="hr-shown">
                {total !== undefined
                    ? t("hr.list.showingOf", { shown: shown.toLocaleString(), total: total.toLocaleString() })
                    : t("hr.list.showing", { shown: shown.toLocaleString() })}
            </span>
            {hasNextPage ? (
                <Button variant="outline" size="sm" onClick={onLoadMore} disabled={fetchingNext}>
                    {fetchingNext ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                    {t("hr.list.loadMore")}
                </Button>
            ) : null}
        </div>
    );
}

/** `MMMM yyyy` of a pay period `yyyy-MM` in the page's language. */
export function periodLabel(period: string, language: string): string {
    const d = parseISO(`${period}-01`);
    return isValid(d) ? format(d, "MMMM yyyy", { locale: language === "th" ? thLocale : enUS }) : period;
}

/** A Bangkok day `yyyy-MM-dd` (or an instant) as `d MMM yyyy` in the page's language. */
export function dayLabel(day: string | null | undefined, language: string, pattern = "d MMM yyyy"): string {
    if (!day) return "—";
    const d = parseISO(day);
    return isValid(d) ? format(d, pattern, { locale: language === "th" ? thLocale : enUS }) : "—";
}
