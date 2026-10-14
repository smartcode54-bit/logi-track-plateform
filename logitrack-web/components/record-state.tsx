"use client";

/**
 * What a detail or edit page shows instead of its record when it has none (UI3D, issue #143). Two cases, told apart
 * by the error of the record's query (lib/apiError.ts):
 *
 * - **Not found** (HTTP 404, or no id in the address): Go answers 404 both for a record that does not exist and for
 *   one outside the reader's scope, so the page never says which. H1 "Not found", the same in the crumb and the tab
 *   title, and a link back to the entry's list (the menu's entry, components/app-nav.ts).
 * - **No access** (HTTP 403): an access answer, so no retry that could never succeed; the way back.
 * - **Could not load** (a 5xx, the network, anything else): the reason in the reader's language, a retry, the way
 *   back, and the request id as a quiet reference for support.
 *
 * A page shows it only when it has no record to show (`recordUnavailable`): a failed background refetch keeps the
 * record on screen.
 *
 * Each page keeps its own loading state; this replaces only the old red "load failed" heading without an H1 or a retry.
 */
import Link from "next/link";
import { usePathname } from "next/navigation";
import { ArrowLeft, RotateCw } from "lucide-react";

import { PageHeader } from "@/components/page-header";
import { breadcrumbTrail } from "@/components/app-nav";
import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";
import { apiErrorText, isApiError } from "@/lib/apiError";

/** A 404 from Go or the BFF: the record does not exist, or is outside the reader's scope (never said which). */
export function isNotFoundError(error: unknown): boolean {
    return isApiError(error) && error.status === 404;
}

/**
 * Whether a record's query leaves the page nothing to show, so it renders `RecordState`: the record is gone (404),
 * or the query has settled without a record. A failed background refetch (TanStack keeps the data and sets the error;
 * window focus refetches) is not that: the page keeps the record it has, and an edit form keeps what is being typed.
 */
export function recordUnavailable(query: { data: unknown; error: unknown; status: "pending" | "error" | "success" }): boolean {
    if (isNotFoundError(query.error)) return true;
    if (query.data !== undefined && query.data !== null) return false;
    return query.status !== "pending";
}

export interface RecordStateProps {
    /** The record query's error; none (an address without an id, a record the page could not resolve) is "not found". */
    error?: unknown;
    /** Loads the record again (`query.refetch`). */
    onRetry?: () => void;
    /** The retry is under way. */
    retrying?: boolean;
}

export function RecordState({ error, onRetry, retrying = false }: RecordStateProps) {
    const { t } = useLanguage();
    const pathname = usePathname() ?? "";
    const { crumbs } = breadcrumbTrail(pathname);
    // The entry the record belongs to (its list), the last crumb with a link.
    const entry = [...crumbs].reverse().find((crumb) => crumb.href);
    const entryLabel = entry ? t(entry.labelKey) : "";
    // The only way on from "not found" (primary); beside "Try again" it is the second action (outline).
    const backToList = (variant: "default" | "outline") =>
        entry?.href ? (
            <Button asChild variant={variant}>
                <Link href={entry.href} prefetch={false}>
                    <ArrowLeft aria-hidden />
                    {t("pageState.record.backTo", { page: entryLabel })}
                </Link>
            </Button>
        ) : null;

    if (!error || isNotFoundError(error)) {
        return (
            <div data-testid="record-not-found" className="flex flex-col gap-section">
                <PageHeader
                    title={entryLabel}
                    recordName={t("pageState.record.notFound.title")}
                    description={t("pageState.record.notFound.body")}
                />
                {entry?.href ? <div className="flex flex-wrap gap-2">{backToList("default")}</div> : null}
            </div>
        );
    }

    // A 403 is an access answer, not a failure: trying again cannot change it, so no retry.
    if (isApiError(error) && error.status === 403) {
        return (
            <div data-testid="record-forbidden" className="flex flex-col gap-section">
                <PageHeader title={entryLabel} recordName={t("pageState.record.forbidden.title")} description={t("pageState.record.forbidden.body")} />
                {entry?.href ? <div className="flex flex-wrap gap-2">{backToList("default")}</div> : null}
            </div>
        );
    }

    const reference = isApiError(error) ? error.requestId : "";
    return (
        <div data-testid="record-load-error" className="flex flex-col gap-section">
            <PageHeader title={t("pageState.record.loadError.title")} description={<span role="alert">{apiErrorText(error, t)}</span>} />
            <div className="flex flex-wrap gap-2">
                {onRetry ? (
                    <Button type="button" onClick={onRetry} disabled={retrying}>
                        <RotateCw aria-hidden className={retrying ? "animate-spin" : undefined} />
                        {t("pageState.retry")}
                    </Button>
                ) : null}
                {backToList("outline")}
            </div>
            {reference ? <p className="text-xs text-muted-foreground">{t("pageState.reference", { id: reference })}</p> : null}
        </div>
    );
}
