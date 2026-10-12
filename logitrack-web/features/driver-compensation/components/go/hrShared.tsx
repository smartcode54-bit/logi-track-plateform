"use client";

/**
 * Small pieces the Go HR pages share (T46): the error banner over `hrErrorText`, status badges, the
 * "load more" footer of a keyset list and the payroll round label. Nothing here imports Firebase.
 */
import { format, isValid, parseISO } from "date-fns";
import { enUS, th as thLocale } from "date-fns/locale";
import { Loader2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { cn } from "@/lib/utils";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

import { hrErrorText } from "../../api/hrErrors";

export function HrErrorAlert({ error, title }: { error: unknown; title?: string }) {
    const { t } = useLanguage();
    if (!error) return null;
    return (
        <Alert variant="destructive" role="alert">
            <AlertDescription>
                {title ? <span className="font-medium">{title}: </span> : null}
                {hrErrorText(error, t)}
            </AlertDescription>
        </Alert>
    );
}

const STATUS_CLASSES: Record<string, string> = {
    draft: "bg-gray-500/10 text-gray-600 border-gray-500/20",
    pending: "bg-yellow-500/10 text-yellow-700 border-yellow-500/20",
    pending_approval: "bg-yellow-500/10 text-yellow-700 border-yellow-500/20",
    partially_deducted: "bg-blue-500/10 text-blue-600 border-blue-500/20",
    approved: "bg-blue-500/10 text-blue-600 border-blue-500/20",
    paid: "bg-green-500/10 text-green-600 border-green-500/20",
    cleared: "bg-green-500/10 text-green-600 border-green-500/20",
    rejected: "bg-red-500/10 text-red-600 border-red-500/20",
    cancelled: "bg-red-500/10 text-red-600 border-red-500/20",
};

/** A status badge; `labelKey` is the catalogue key of the status's text. */
export function StatusBadge({ status, labelKey, className }: { status: string; labelKey: string; className?: string }) {
    const { t } = useLanguage();
    return (
        <Badge variant="outline" className={cn(STATUS_CLASSES[status] ?? STATUS_CLASSES.draft, className)}>
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
