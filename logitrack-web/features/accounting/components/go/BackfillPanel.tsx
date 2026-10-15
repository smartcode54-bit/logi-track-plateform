"use client";

/** Backfill submits one bounded server job for each selected kind; no automatic reruns. */
import { useState } from "react";
import { enUS, th as thDateLocale } from "date-fns/locale";
import { Loader2, Wand2 } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useCan, useMe } from "@/features/auth/api/useMe";
import { apiErrorText } from "@/lib/apiError";
import { CustomerSearchSelect } from "@/components/entity-pickers";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DateOnlyRangePicker } from "@/components/ui/date-range-picker";

import type { BackfillResult, BillingJobType } from "../../api/billingWrites.go";
import { useBillingJob } from "../../api/useBillingJob";
import { useCustomerLookup } from "../../api/useCustomerLookup";
import { formatBillingDay } from "../../lib/billingUiDates";
import { ForceRepriceDialog } from "./ForceRepriceDialog";
import { Field } from "./shared";

function ResultLine({ label, result }: { label: string; result: BackfillResult }) {
    const { t } = useLanguage();
    const failures = result.failures ?? [];
    return (
        <div className="space-y-2 rounded-md border px-3 py-2 text-sm">
            <p className="font-medium">{label}</p>
            <p>{t("accounting.income.backfill.resultSummary", {
                scanned: result.scanned, eligible: result.eligible ?? result.scanned,
                written: result.written, skipped: result.skipped, failed: result.failed,
            })}</p>
            {result.blocked ? <p className="text-warning">{t("accounting.go.backfill.blocked", { count: result.blocked, invoices: (result.blockedInvoices ?? []).join(", ") })}</p> : null}
            {result.scanned >= 500 ? <Alert variant="warning"><AlertDescription>{t("accounting.ui4.scanLimitHint")}</AlertDescription></Alert> : null}
            {result.capped ? <Alert variant="warning"><AlertDescription>{t("accounting.ui4.writeLimitHint")}</AlertDescription></Alert> : null}
            {failures.length > 0 ? (
                <ul className="max-h-48 list-disc space-y-2 overflow-y-auto pl-5 text-xs">
                    {failures.map((failure, i) => {
                        const display = failure.tripNo || [failure.driverName, failure.truckLicensePlate].filter(Boolean).join(" · ");
                        const id = failure.tripId ?? failure.standbyId ?? failure.id;
                        return <li key={id ?? i}>
                            {display ? `${display} — ` : ""}
                            {t(`accounting.billingDocument.unpriced.reason.${failure.error}`, t("accounting.ui4.unknownFailure"))}
                            {!display && id ? <details className="text-muted-foreground"><summary>{t("accounting.ui4.technicalDetails")}</summary><p className="break-all">{id}</p></details> : null}
                        </li>;
                    })}
                </ul>
            ) : null}
            {result.failed > failures.length ? <p className="text-xs">{t("accounting.ui4.failuresMore", { count: result.failed - failures.length })}</p> : null}
        </div>
    );
}

export function BackfillPanel({ defaultFrom, defaultTo, defaultCustomer = "all", withCustomer = true }: {
    defaultFrom: string;
    defaultTo: string;
    defaultCustomer?: string;
    withCustomer?: boolean;
}) {
    const { t, language } = useLanguage();
    const canForce = useCan("accounting:recompute_force").allowed;
    const canRun = useCan("accounting:edit_rate_card").allowed;
    const me = useMe();
    const [from, setFrom] = useState(defaultFrom);
    const [to, setTo] = useState(defaultTo);
    const [customerId, setCustomerId] = useState(defaultCustomer);
    const [force, setForce] = useState(false);
    const [trips, setTrips] = useState(true);
    const [standby, setStandby] = useState(true);
    const [confirm, setConfirm] = useState(false);
    const customers = useCustomerLookup(customerId === "all" ? [] : [customerId]);
    const scopeKey = (type: BillingJobType) => me.data ? `billing-job:${JSON.stringify([me.data.id, me.data.tenant?.id ?? null, type, from, to, withCustomer ? customerId : "all"])}` : null;
    const notices = {
        onRestored: () => toast.info(t("accounting.ui4.jobRestored")),
        onAlreadyRunning: () => toast.info(t("accounting.ui4.jobAlreadyRunning")),
    };
    const tripJob = useBillingJob<BackfillResult>({ storageKey: scopeKey("billing.backfill-trips"), ...notices });
    const standbyJob = useBillingJob<BackfillResult>({ storageKey: scopeKey("billing.backfill-standby"), ...notices });
    const running = tripJob.running || standbyJob.running;
    const forced = force && canForce;
    const valid = canRun && !running && from !== "" && to !== "" && from <= to && (trips || standby);
    const run = () => {
        if (!valid) return;
        setConfirm(false);
        const input = {
            fromDateStr: from, toDateStr: to,
            customerId: withCustomer && customerId !== "all" ? customerId : undefined,
            forceRecompute: forced ? true : undefined,
        };
        if (trips) void tripJob.start("billing.backfill-trips", input);
        if (standby) void standbyJob.start("billing.backfill-standby", input);
    };

    return (
        <div className="space-y-4">
            <div className="flex flex-wrap items-end gap-4">
                <Field label={t("accounting.income.backfill.range")} htmlFor="backfill-range">
                    <DateOnlyRangePicker id="backfill-range" aria-label={t("accounting.income.backfill.range")} from={from} to={to}
                        onChange={(f, end) => { setFrom(f); setTo(end); }} disabled={running}
                        locale={language === "th" ? thDateLocale : enUS} className="w-[230px] max-md:min-h-11" />
                </Field>
                {withCustomer ? <Field label={t("accounting.income.filter.customer")} htmlFor="backfill-customer">
                    <CustomerSearchSelect id="backfill-customer" aria-label={t("accounting.income.filter.customer")} value={customerId}
                        onValueChange={(id) => setCustomerId(id || "all")} allValue="all" allLabel={t("accounting.filter.all")} disabled={running} className="w-full max-md:min-h-11 sm:w-[230px]" />
                </Field> : null}
                <Button type="button" className="max-md:min-h-11" disabled={!valid} onClick={() => forced ? setConfirm(true) : run()}>
                    {running ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Wand2 className="mr-2 h-4 w-4" />}
                    {t(forced ? "accounting.ui4.forceAction" : "accounting.income.backfill.runByDate")}
                </Button>
            </div>
            {!canRun ? <p className="text-sm text-muted-foreground">{t("accounting.ui4.runDenied")}</p> : null}
            <div className="flex flex-wrap items-center gap-4">
                <label className="flex min-h-11 items-center gap-2 text-sm"><Checkbox checked={trips} disabled={running} onCheckedChange={(v) => setTrips(v === true)} />{t("accounting.go.backfill.trips")}</label>
                <label className="flex min-h-11 items-center gap-2 text-sm"><Checkbox checked={standby} disabled={running} onCheckedChange={(v) => setStandby(v === true)} />{t("accounting.go.backfill.standby")}</label>
                {canForce ? <label className="flex min-h-11 items-center gap-2 text-sm"><Checkbox checked={force} disabled={running} onCheckedChange={(v) => setForce(v === true)} />{t("accounting.income.backfill.forceRecompute")}</label> : null}
            </div>
            <p className="text-xs text-muted-foreground">{t("accounting.ui4.dateAxis")}</p>
            <p className="text-xs text-muted-foreground">{t("accounting.go.backfill.jobHint")}</p>
            {[
                { job: tripJob, label: t("accounting.go.backfill.trips") },
                { job: standbyJob, label: t("accounting.go.backfill.standby") },
            ].map(({ job, label }) => <div key={label} className="space-y-1">
                {job.running ? <p role="status" className="text-xs text-muted-foreground">{label}: {job.progress?.done ?? "—"}/{job.progress?.total ?? "—"}</p> : null}
                {job.error ? <Alert variant="destructive"><AlertDescription>{label}: {apiErrorText(job.error, t)}</AlertDescription></Alert> : null}
                {job.result ? <ResultLine label={label} result={job.result} /> : null}
            </div>)}
            {confirm ? <ForceRepriceDialog perJob scope={{ from: formatBillingDay(from), to: formatBillingDay(to), customer: customerId === "all" ? t("accounting.filter.all") : customers.label(customerId), kinds: [trips && t("accounting.go.backfill.trips"), standby && t("accounting.go.backfill.standby")].filter(Boolean).join(", "), limit: 200 }} onCancel={() => setConfirm(false)} onConfirm={run} /> : null}
        </div>
    );
}
