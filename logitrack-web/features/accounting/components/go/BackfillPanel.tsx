"use client";

/**
 * Billing backfill over Go (T42): `POST /v1/jobs/billing.backfill-trips` and `.../billing.backfill-standby`
 * (`{fromDateStr, toDateStr, customerId?, forceRecompute?}` -> `202`), each followed through `['jobs',id]`.
 * The trips and the standby records of a range land on the same invoice (ADR 0008 §5), so one panel runs
 * either or both. `forceRecompute` needs `accounting:recompute_force`; sent and paid periods are never
 * repriced (the result counts them as blocked). Used by the income page, the rate card and
 * `/app/utilities/backfill`.
 */
import { useState } from "react";
import { enUS, th as thDateLocale } from "date-fns/locale";
import { Loader2, Wand2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { CustomerSearchSelect } from "@/components/entity-pickers";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DateOnlyRangePicker } from "@/components/ui/date-range-picker";
import { Label } from "@/components/ui/label";

import type { BackfillResult } from "../../api/billingWrites.go";
import { useBillingJob } from "../../api/useBillingJob";

function ResultLine({ label, result }: { label: string; result: BackfillResult }) {
    const { t } = useLanguage();
    return (
        <div className="space-y-1 rounded-md border bg-muted/30 px-3 py-2 text-sm">
            <p className="font-medium">{label}</p>
            <p>
                {t("accounting.income.backfill.resultSummary", {
                    scanned: result.scanned,
                    eligible: result.eligible ?? result.scanned,
                    written: result.written,
                    skipped: result.skipped,
                    failed: result.failed,
                })}
            </p>
            {result.blocked ? (
                <p className="text-amber-600">{t("accounting.go.backfill.blocked", { count: result.blocked, invoices: (result.blockedInvoices ?? []).join(", ") })}</p>
            ) : null}
            {result.capped ? <p className="text-amber-600">{t("accounting.income.backfill.cappedHint")}</p> : null}
            {(result.failures ?? []).length > 0 ? (
                <ul className="max-h-32 list-disc overflow-y-auto pl-5 text-xs text-muted-foreground">
                    {(result.failures ?? []).map((f, i) => (
                        <li key={`${f.tripId ?? f.standbyId ?? f.id ?? i}`}>
                            {f.tripId ?? f.standbyId ?? f.id}
                            {f.error ? ` — ${f.error}` : ""}
                        </li>
                    ))}
                </ul>
            ) : null}
        </div>
    );
}

export function BackfillPanel({
    defaultFrom,
    defaultTo,
    withCustomer = true,
}: {
    defaultFrom: string;
    defaultTo: string;
    /** Offer a customer filter (all customers otherwise). */
    withCustomer?: boolean;
}) {
    const { t, language } = useLanguage();
    const canForce = useCan("accounting:recompute_force").allowed;
    const [from, setFrom] = useState(defaultFrom);
    const [to, setTo] = useState(defaultTo);
    const [customerId, setCustomerId] = useState("all");
    const [force, setForce] = useState(false);
    const [trips, setTrips] = useState(true);
    const [standby, setStandby] = useState(true);
    const tripJob = useBillingJob<BackfillResult>();
    const standbyJob = useBillingJob<BackfillResult>();
    const running = tripJob.running || standbyJob.running;

    const run = () => {
        const input = {
            fromDateStr: from,
            toDateStr: to,
            customerId: withCustomer && customerId !== "all" ? customerId : undefined,
            forceRecompute: force && canForce ? true : undefined,
        };
        if (trips) void tripJob.start("billing.backfill-trips", input);
        if (standby) void standbyJob.start("billing.backfill-standby", input);
    };

    return (
        <div className="space-y-4">
            <div className="flex flex-wrap items-end gap-4">
                <div className="flex flex-col gap-1.5">
                    <Label>{t("accounting.income.backfill.range")}</Label>
                    <DateOnlyRangePicker
                        from={from}
                        to={to}
                        onChange={(f, tt) => {
                            setFrom(f);
                            setTo(tt);
                        }}
                        locale={language === "th" ? thDateLocale : enUS}
                        className="w-[230px]"
                    />
                </div>
                {withCustomer ? (
                    <div className="flex flex-col gap-1.5">
                        <Label>{t("accounting.income.filter.customer")}</Label>
                        <CustomerSearchSelect
                            aria-label={t("accounting.income.filter.customer")}
                            value={customerId}
                            onValueChange={(id) => setCustomerId(id || "all")}
                            allValue="all"
                            allLabel={t("accounting.filter.all")}
                            className="min-w-[200px]"
                        />
                    </div>
                ) : null}
                <Button type="button" variant="secondary" disabled={running || !from || !to || (!trips && !standby)} onClick={run}>
                    {running ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Wand2 className="mr-2 h-4 w-4" />}
                    {t("accounting.income.backfill.runByDate")}
                </Button>
            </div>
            <div className="flex flex-wrap items-center gap-4">
                <label className="flex cursor-pointer items-center gap-2 text-sm">
                    <Checkbox checked={trips} onCheckedChange={(v) => setTrips(v === true)} />
                    {t("accounting.go.backfill.trips")}
                </label>
                <label className="flex cursor-pointer items-center gap-2 text-sm">
                    <Checkbox checked={standby} onCheckedChange={(v) => setStandby(v === true)} />
                    {t("accounting.go.backfill.standby")}
                </label>
                {canForce ? (
                    <label className="flex cursor-pointer items-center gap-2 text-sm">
                        <Checkbox checked={force} onCheckedChange={(v) => setForce(v === true)} />
                        {t("accounting.income.backfill.forceRecompute")}
                    </label>
                ) : null}
            </div>
            <p className="text-xs text-muted-foreground">{t("accounting.go.backfill.jobHint")}</p>
            {[
                { job: tripJob, label: t("accounting.go.backfill.trips") },
                { job: standbyJob, label: t("accounting.go.backfill.standby") },
            ].map(({ job, label }) => (
                <div key={label} className="space-y-1">
                    {job.running && job.progress ? (
                        <p className="text-xs text-muted-foreground">
                            {label}: {job.progress.done ?? 0}/{job.progress.total ?? "?"}
                        </p>
                    ) : null}
                    {job.error ? (
                        <p className="text-sm text-destructive">
                            {label}: {apiErrorText(job.error, t)}
                        </p>
                    ) : null}
                    {job.result ? <ResultLine label={label} result={job.result} /> : null}
                </div>
            ))}
        </div>
    );
}
