"use client";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { useEffect, useMemo, useState } from "react";
import { useLanguage } from "@/context/language";
import { getCustomers } from "@/features/customers/api/customers";
import type { Customer } from "@/validate/customerSchema";
import {
    downloadBillingZip,
    type BillingTripRow,
    type BillingCustomer,
    type BillingPeriod,
    type BillingProviderInfo,
} from "@/lib/billingDocument";
import { saveBillingStatement } from "@/lib/billingStatement";
import { getOwnerCompany } from "@/features/companies/api/companies";
import {
    getCustomerServiceFees,
    fetchBillingTripRows,
    fetchStandbyBillingDiagnostics,
    fetchTripsMissingBillingDate,
    UnpricedStandbyPanel,
    type StandbyBillingDiagnostics,
    type TripMissingBillingDate,
} from "@/features/accounting";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { AlertTriangle, Download, FileText, Loader2, RefreshCw, Wrench, X } from "lucide-react";
import { format } from "date-fns";
import { httpsCallable } from "firebase/functions";
import { functions } from "@/firebase/client";
import { bangkokDateStr } from "@/lib/billingDate";
import { snapshotCarriesFuel } from "@/lib/billingCompute";
import { WITHHOLDING_TAX_RATE } from "@/lib/billingConfig";
import { toast } from "sonner";
import { useAuth } from "@/context/auth";
import {
    MONTH_FILTER_ALL,
    MONTH_FILTER_NONE,
    buildMonthFilterOptions,
    monthFilterKey,
    rowMatchesMonthFilter,
} from "@/lib/monthFilter";
import {
    PLATE_FILTER_ALL,
    buildPlateFilterOptions,
    rowMatchesPlateFilter,
} from "@/lib/truckPlate";
import { PlateFilterCombobox } from "@/components/plate-filter-combobox";
import {
    VEHICLE_CLASS_FILTER_ALL,
    VEHICLE_CLASS_FILTER_NONE,
    buildVehicleClassOptions,
    rowMatchesVehicleClass,
} from "@/lib/vehicleClass";

// ─── helpers ──────────────────────────────────────────────────────────────────

function formatThb(n: number) {
    return new Intl.NumberFormat("th-TH", { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(n);
}

/**
 * A plan-basis row whose task plan date no longer agrees with the `billingDate` frozen on it when it
 * was priced (ADR 0027 §2). The documents bill the frozen date, so until someone force-recomputes,
 * the plan date shown here is NOT the month this row bills in. Marked rather than silently shown,
 * because the whole point of plan-date billing is that the invoice matches the customer's plan.
 * Only detectable for a single billing entity — the "all" view stamps no basis.
 */
function planDateDrifted(trip: BillingTripRow): boolean {
    if (trip.billingDateBasis !== "plan" || !trip.planDate || !trip.billingDate) return false;
    return bangkokDateStr(trip.planDate) !== bangkokDateStr(trip.billingDate);
}

function suppCarriesFuel(trip: BillingTripRow): boolean {
    return trip.rowType !== "standby" && trip.jobCategory === "SUPPLEMENTARY" && snapshotCarriesFuel(trip);
}

/**
 * A เสริม row priced WITH a fuel adjustment and no manual override. เสริม is a fixed price fuel never
 * moves (ADR-0005), so this is a corrupted snapshot — the old Driver Monitor browser writer ignored
 * หลัก/เสริม. The server does not treat it as frozen (`isFrozenBillingSnapshot`), so the repair
 * button fixes it.
 */
function suppPricedWithFuel(trip: BillingTripRow): boolean {
    return suppCarriesFuel(trip) && trip.billingManualOverride !== true;
}

/**
 * Same fuel fields, but the price was set by hand (`billingManualOverride`). The server keeps every
 * manual price frozen, so a recompute cannot touch it — the fuel fields are usually just the system's
 * computed suggestion saved alongside the typed price. Flagged for a human check, never repaired.
 */
function suppManualPriceWithFuel(trip: BillingTripRow): boolean {
    return suppCarriesFuel(trip) && trip.billingManualOverride === true;
}

/** Which date axis a billing period is cut on (ADR 0027): plan date, or delivery instant. */
type BillingAxis = "plan" | "delivered";

type RepairResponse = {
    ok: boolean;
    skipped?: boolean;
    error?: string;
    blockedInvoiceNumber?: string;
    billingDateMoved?: boolean;
};

// ─── Page ────────────────────────────────────────────────────────────────────

const currentYear = new Date().getFullYear();
const YEARS = Array.from({ length: 3 }, (_, i) => currentYear - i);
const MONTHS = [
    { value: 1, label: "มกราคม / January" },
    { value: 2, label: "กุมภาพันธ์ / February" },
    { value: 3, label: "มีนาคม / March" },
    { value: 4, label: "เมษายน / April" },
    { value: 5, label: "พฤษภาคม / May" },
    { value: 6, label: "มิถุนายน / June" },
    { value: 7, label: "กรกฎาคม / July" },
    { value: 8, label: "สิงหาคม / August" },
    { value: 9, label: "กันยายน / September" },
    { value: 10, label: "ตุลาคม / October" },
    { value: 11, label: "พฤศจิกายน / November" },
    { value: 12, label: "ธันวาคม / December" },
];

type BillingTotals = {
    grandTotal: number;
    withholdingTax: number;
    totalNet: number;
    breakdown: {
        tripOnlyCount: number; tripSubtotal: number;
        standbyCount: number; standbySubtotal: number;
        multiDropCount: number; multiDropSubtotal: number;
    };
};

/**
 * Totals for a set of billing rows. Called for the preview set (what the screen shows) and,
 * independently, for the invoice set (what handleDownload bills) so a plate review filter can never
 * change the billed amount — see ADR 0005 §1-3 and glossary "Invoice set vs preview set".
 */
function computeBillingTotals(rows: BillingTripRow[]): BillingTotals {
    const sum = (arr: BillingTripRow[]) => arr.reduce((s, r) => s + r.billingEstimateThb, 0);
    const tripRows      = rows.filter((r) => r.rowType === "trip");
    const standbyRows   = rows.filter((r) => r.rowType === "standby");
    const multiDropRows = rows.filter((r) => r.rowType === "multidrop_stop");
    const grandTotal = sum(rows);
    const withholdingTax = Math.round(grandTotal * WITHHOLDING_TAX_RATE * 100) / 100;
    return {
        grandTotal,
        withholdingTax,
        totalNet: grandTotal - withholdingTax,
        breakdown: {
            tripOnlyCount: tripRows.length,   tripSubtotal: sum(tripRows),
            standbyCount: standbyRows.length, standbySubtotal: sum(standbyRows),
            multiDropCount: multiDropRows.length, multiDropSubtotal: sum(multiDropRows),
        },
    };
}

export default function BillingDocumentPage() {
    const { t } = useLanguage();
    const auth = useAuth();
    // Same gate the Income page uses for its inline billing edits — repairing a standby price is the
    // same class of operation, so it stays on the same claim rather than inventing a new capability.
    const isAdmin = auth?.customClaims?.admin === true;

    const now = new Date();
    const [selectedMonth, setSelectedMonth] = useState(now.getMonth() + 1);
    const [selectedYear, setSelectedYear] = useState(now.getFullYear());
    const [selectedCustomerId, setSelectedCustomerId] = useState<string>("all");

    const [customers, setCustomers] = useState<Customer[]>([]);
    const [trips, setTrips] = useState<BillingTripRow[]>([]);
    // Standby that produced no billable row. Kept OUT of `trips` on purpose so an unpriced record can
    // never reach the invoice set (ADR 0008 §6, ADR 0005 §1-3).
    const [standbyDiagnostics, setStandbyDiagnostics] = useState<StandbyBillingDiagnostics | null>(null);

    // ── Type toggles (which charge types to include in billing) ──────────────
    const [includeTrips,     setIncludeTrips]     = useState(true);
    const [includeStandby,   setIncludeStandby]   = useState(true);
    const [includeMultiDrop, setIncludeMultiDrop] = useState(true);

    // ── Job category toggles (หลัก/เสริม) — standby rows have no jobCategory, unaffected ──
    const [includePrimary,      setIncludePrimary]      = useState(true);
    const [includeSupplementary, setIncludeSupplementary] = useState(true);
    // Rows whose หลัก/เสริม couldn't be resolved from trip OR task (ADR 0010) — their own visible
    // bucket, never silently folded into หลัก.
    const [includeUnverified,   setIncludeUnverified]   = useState(true);

    const [loading, setLoading] = useState(false);
    const [generating, setGenerating] = useState(false);
    const [ownerProvider, setOwnerProvider] = useState<BillingProviderInfo | undefined>(undefined);

    // Plate and vehicle class are REVIEW filters, not billing dimensions (ADR 0005 §7): they narrow
    // the on-screen preview only, and Download is blocked while either is active so the invoice can
    // never bill a subset.
    const [plateFilter, setPlateFilter] = useState<string>(PLATE_FILTER_ALL);
    // ADR 0028: toggle the "วันรับงานจริง" (actual pickup) column on-screen and in the exported detail.
    const [showActualPickup, setShowActualPickup] = useState(false);
    const [vehicleClassFilter, setVehicleClassFilter] = useState<string>(VEHICLE_CLASS_FILTER_ALL);
    // Month on the OTHER date axis from the one the period was loaded on — a review filter like plate
    // and vehicle class (ADR 0005): narrows the preview only and blocks Download while active.
    const [reviewMonthFilter, setReviewMonthFilter] = useState<string>(MONTH_FILTER_ALL);

    // The axis the period month is cut on, for what Load WILL fetch (ADR 0027): a single plan-basis
    // customer is cut by plan date; every other customer — and the "all" aggregate — by delivery.
    // Mirrors the branch in fetchBillingTripRows; the page only labels it honestly.
    const selectedBasis: BillingAxis = useMemo(() => {
        if (selectedCustomerId === "all") return "delivered";
        return customers.find((c) => c.id === selectedCustomerId)?.billingDateBasis === "plan" ? "plan" : "delivered";
    }, [selectedCustomerId, customers]);
    // ...and for what the rows on screen WERE fetched with. They differ when the customer is switched
    // without reloading (e.g. load "all", then pick CJSF) — the rows are then on the wrong axis.
    const [loaded, setLoaded] = useState<{ basis: BillingAxis; month: number; year: number } | null>(null);
    // Plan-basis only: trips of this customer delivered in the period that carry no `billingDate`, so
    // they sit in NO plan period and the period query can never return them. `null` = the lookup
    // failed — shown, never silently treated as "none" (ADR 0008 §8).
    const [missingBillingDate, setMissingBillingDate] = useState<TripMissingBillingDate[] | null>([]);

    const [repairing, setRepairing] = useState(false);
    const [repairProgress, setRepairProgress] = useState<{ done: number; total: number } | null>(null);

    // Load customers + owner company once
    useEffect(() => {
        getCustomers().then(setCustomers).catch(console.error);
        // Load owner company for PDF branding (stamp, signature, etc.)
        getOwnerCompany()
            .then((company) => {
                if (company) {
                    setOwnerProvider({
                        name: company.nameTh,
                        shortName: company.shortName,
                        address: company.address,
                        taxId: company.taxId,
                        bankName: company.bankName,
                        accountNumber: company.accountNumber,
                        accountName: company.accountName,
                        withholdingTaxRate: company.withholdingTaxRate,
                        stampUrl: company.stampUrl,
                        signatureUrl: company.signatureUrl,
                        signatoryName: company.signatoryName,
                    });
                }
            })
            .catch((e) => console.warn("[billing] getOwnerCompany failed (using default provider):", e));
    }, []);

    async function loadTrips() {
        setLoading(true);
        try {
            const period = { month: selectedMonth, year: selectedYear };
            const basis = selectedBasis;
            const checkMissing = basis === "plan" && selectedCustomerId !== "all";
            const [rows, diagnostics, missing] = await Promise.all([
                fetchBillingTripRows(selectedCustomerId, period),
                fetchStandbyBillingDiagnostics(selectedCustomerId, period),
                checkMissing
                    ? fetchTripsMissingBillingDate(selectedCustomerId, period).catch((e) => {
                          console.error("[billing] fetchTripsMissingBillingDate failed:", e);
                          return null;
                      })
                    : Promise.resolve([] as TripMissingBillingDate[]),
            ]);
            setTrips(rows);
            setStandbyDiagnostics(diagnostics);
            setMissingBillingDate(missing);
            setLoaded({ basis, ...period });
            // The review month is a key on the other axis; after a reload that axis may have flipped,
            // and a stale key would silently filter everything out.
            setReviewMonthFilter(MONTH_FILTER_ALL);
        } catch (e) {
            console.error("[billing] fetchBillingTripRows failed:", e);
            toast.error(t("accounting.billingDocument.loadError", "Failed to load trips — please try again."));
        } finally {
            setLoading(false);
        }
    }

    const filteredTrips = useMemo(() => {
        return trips.filter((r) => {
            if (selectedCustomerId !== "all" && r.billingCustomerId !== selectedCustomerId) return false;
            if (r.rowType === "trip"           && !includeTrips)     return false;
            if (r.rowType === "standby"        && !includeStandby)   return false;
            if (r.rowType === "multidrop_stop" && !includeMultiDrop) return false;
            // Job category only applies to trip / multidrop_stop rows — standby has no jobCategory.
            // Unresolved (undefined) is its own bucket (ADR 0010), not folded into หลัก.
            if (r.rowType !== "standby") {
                if (r.jobCategory === "SUPPLEMENTARY") { if (!includeSupplementary) return false; }
                else if (r.jobCategory === "PRIMARY") { if (!includePrimary) return false; }
                else { if (!includeUnverified) return false; }
            }
            return true;
        });
    }, [trips, selectedCustomerId, includeTrips, includeStandby, includeMultiDrop, includePrimary, includeSupplementary, includeUnverified]);

    // Review-filter options come from the invoice set, so every option provably matches ≥1 billable
    // row (orphan plates / a no-class bucket stay reachable) — same approach as elsewhere.
    const plateOptions = useMemo(
        () => buildPlateFilterOptions(filteredTrips.map((r) => ({ truckId: r.truckId, plate: r.truckLicensePlate }))),
        [filteredTrips]
    );
    const vehicleClassOptions = useMemo(
        () => buildVehicleClassOptions(filteredTrips.map((r) => r.vehicleClass)),
        [filteredTrips]
    );

    // The review month reads the axis the period was NOT cut on: a delivery-cut period is reviewed by
    // plan month, a plan-cut period by delivery month (standby's `deliveredTimestamp` is its endedAt).
    const reviewAxis: BillingAxis = loaded?.basis === "plan" ? "delivered" : "plan";
    const reviewMonthOptions = useMemo(
        () => buildMonthFilterOptions(
            filteredTrips.map((r) => (reviewAxis === "plan" ? r.planDate : r.deliveredTimestamp))
        ),
        [filteredTrips, reviewAxis]
    );

    // Preview set — the invoice set narrowed by the plate, vehicle-class and month review filters.
    // Drives the table + summary cards ONLY; handleDownload always bills filteredTrips (invoice set).
    // ADR 0005 §1-3.
    const previewTrips = useMemo(() => {
        return filteredTrips.filter((r) =>
            rowMatchesPlateFilter({ truckId: r.truckId, plate: r.truckLicensePlate }, plateFilter) &&
            rowMatchesVehicleClass(r.vehicleClass, vehicleClassFilter) &&
            rowMatchesMonthFilter(reviewAxis === "plan" ? r.planDate : r.deliveredTimestamp, reviewMonthFilter)
        );
    }, [filteredTrips, plateFilter, vehicleClassFilter, reviewAxis, reviewMonthFilter]);

    // Rows whose price or period a forced server recompute would fix. Read from every loaded row of
    // the customer — not the charge-type toggles — so hiding a type never hides a problem in it.
    const billingIssues = useMemo(() => {
        const base = selectedCustomerId === "all" ? trips : trips.filter((r) => r.billingCustomerId === selectedCustomerId);
        const periodKey = loaded ? `${loaded.year}-${String(loaded.month).padStart(2, "0")}` : "";
        const planCut = loaded?.basis === "plan";
        const outOfPeriod: BillingTripRow[] = [];
        const drifted: BillingTripRow[] = [];
        const suppWithFuel: BillingTripRow[] = [];
        const suppManualPrice: BillingTripRow[] = [];
        let standbyOtherMonth = 0;
        let noPlanDate = 0;
        for (const r of base) {
            if (suppPricedWithFuel(r)) suppWithFuel.push(r);
            if (suppManualPriceWithFuel(r)) suppManualPrice.push(r);
            // Plan-date checks only mean something on a plan-cut period (ADR 0027).
            if (!planCut) continue;
            if (r.rowType === "standby") {
                // Standby is cut on endedAt for every customer (ADR 0008) — informational only.
                if (r.planDate && monthFilterKey(r.planDate) !== periodKey) standbyOtherMonth++;
                continue;
            }
            if (!r.planDate) { noPlanDate++; continue; }
            if (monthFilterKey(r.planDate) !== periodKey) outOfPeriod.push(r);
            else if (planDateDrifted(r)) drifted.push(r);
        }
        // Only meaningful for the plan-cut load it was fetched with (state is [] otherwise).
        const missing = planCut ? missingBillingDate : [];
        const tripIds = [
            ...new Set([
                ...[...outOfPeriod, ...drifted, ...suppWithFuel]
                    .map((r) => r.tripRecordId)
                    .filter((id): id is string => !!id),
                ...(missing ?? []).map((m) => m.tripRecordId),
            ]),
        ];
        return {
            outOfPeriod,
            drifted,
            suppWithFuel,
            suppManualPrice,
            standbyOtherMonth,
            noPlanDate,
            missingBillingDate: missing ?? [],
            missingCheckFailed: planCut && missing === null,
            tripIds,
        };
    }, [trips, selectedCustomerId, loaded, missingBillingDate]);
    const hasBillingIssues =
        billingIssues.tripIds.length > 0 ||
        billingIssues.standbyOtherMonth > 0 ||
        billingIssues.noPlanDate > 0 ||
        billingIssues.suppManualPrice.length > 0 ||
        billingIssues.missingCheckFailed;

    // Count per type (before type-toggle filter, but after customer filter) for checkbox labels
    const typeCounts = useMemo(() => {
        const base = selectedCustomerId === "all" ? trips : trips.filter((r) => r.billingCustomerId === selectedCustomerId);
        return {
            trips:     base.filter((r) => r.rowType === "trip").length,
            standby:   base.filter((r) => r.rowType === "standby").length,
            multiDrop: base.filter((r) => r.rowType === "multidrop_stop").length,
        };
    }, [trips, selectedCustomerId]);

    // Count per job category (non-standby rows only) for checkbox labels
    const jobCategoryCounts = useMemo(() => {
        const base = (selectedCustomerId === "all" ? trips : trips.filter((r) => r.billingCustomerId === selectedCustomerId))
            .filter((r) => r.rowType !== "standby");
        return {
            primary:       base.filter((r) => r.jobCategory === "PRIMARY").length,
            supplementary: base.filter((r) => r.jobCategory === "SUPPLEMENTARY").length,
            unverified:    base.filter((r) => r.jobCategory !== "PRIMARY" && r.jobCategory !== "SUPPLEMENTARY").length,
        };
    }, [trips, selectedCustomerId]);

    // Display totals reflect the PREVIEW set (plate-narrowed). The invoice set's totals are computed
    // separately inside handleDownload so the two can never silently diverge (ADR 0005 §1-3).
    const { grandTotal, withholdingTax, totalNet, breakdown } = useMemo(
        () => computeBillingTotals(previewTrips),
        [previewTrips]
    );

    const selectedCustomer = useMemo<BillingCustomer | null>(() => {
        if (selectedCustomerId === "all") return null;
        const c = customers.find((c) => c.id === selectedCustomerId);
        if (!c) return null;
        return {
            id: c.id!,
            name: c.name,
            address: c.address,
            taxId: c.taxId,
            branchType: c.branchType,
            branchNumber: c.branchNumber,
            contactName: c.contactName,
            contactPhone: c.contactPhone,
            paymentTermsDays: c.paymentTermsDays,
            invoiceNote: c.invoiceNote,
        };
    }, [selectedCustomerId, customers]);

    async function handleDownload() {
        if (!selectedCustomer) return;
        setGenerating(true);
        try {
            const period: BillingPeriod = { month: selectedMonth, year: selectedYear };

            // Bill the INVOICE set (filteredTrips), independent of the plate review filter. The
            // Download guard already forbids reaching here while a plate filter is active, but binding
            // the statement to its own totals keeps a wrong invoice impossible even if that changes.
            const invoice = computeBillingTotals(filteredTrips);

            // Save billing statement (registry) before download
            const customerForStatement = customers.find((c) => c.id === selectedCustomer.id);
            let invoiceNumber: string | undefined;
            try {
                toast.loading(t("accounting.billingDocument.save.saving"));
                invoiceNumber = await saveBillingStatement({
                    customerId: selectedCustomer.id,
                    customerName: selectedCustomer.name,
                    customerCode: customerForStatement?.code ?? selectedCustomer.id,
                    period,
                    totalAmount: invoice.grandTotal,
                    withholdingTax: invoice.withholdingTax,
                    netAmount: invoice.totalNet,
                    tripCount: filteredTrips.length,
                    ...invoice.breakdown,
                    paymentTermsDays: selectedCustomer.paymentTermsDays,
                    generatedBy: auth?.currentUser?.uid,
                });
                toast.dismiss();
                toast.success(t("accounting.billingDocument.save.saved", { invoiceNumber }));
            } catch (saveErr) {
                toast.dismiss();
                console.error("[billing] Failed to save statement:", saveErr);
                toast.error(t("accounting.billingDocument.save.error"));
                // Still proceed with download even if statement save fails
            }

            await downloadBillingZip(filteredTrips, selectedCustomer, period, invoiceNumber, ownerProvider, showActualPickup);
        } finally {
            setGenerating(false);
        }
    }

    /**
     * Re-price the flagged trips on the SERVER with a forced recompute — the only writer of a price.
     * It re-derives หลัก/เสริม from the task (a เสริม row loses its fuel adjustment), re-stamps
     * `billingDate` from the plan date (a frozen row keeps its price and only moves period — ADR 0027
     * §9), and refuses any row in a period that already has a sent/paid invoice (ADR 0008 §5).
     */
    async function repairBillingIssues() {
        const ids = [...billingIssues.tripIds];
        if (ids.length === 0) return;
        setRepairing(true);
        const recompute = httpsCallable<{ tripId: string; forceRecompute: boolean }, RepairResponse>(
            functions,
            "computeTripBillingSnapshot"
        );
        let repriced = 0;
        let moved = 0;
        let unchanged = 0;
        let failed = 0;
        const blockedInvoices = new Set<string>();
        const queue = [...ids];
        let done = 0;
        setRepairProgress({ done, total: ids.length });
        const worker = async () => {
            for (let id = queue.shift(); id; id = queue.shift()) {
                try {
                    const { data } = await recompute({ tripId: id, forceRecompute: true });
                    if (data.blockedInvoiceNumber) blockedInvoices.add(data.blockedInvoiceNumber);
                    else if (!data.ok) failed++;
                    else if (data.billingDateMoved) moved++;
                    else if (data.skipped) unchanged++;
                    else repriced++;
                } catch (e) {
                    console.error("[billing] repair recompute failed:", id, e);
                    failed++;
                }
                done++;
                setRepairProgress({ done, total: ids.length });
            }
        };
        try {
            await Promise.all(Array.from({ length: Math.min(5, queue.length) }, worker));
            toast.success(t("accounting.billingDocument.repair.result", { repriced, moved, unchanged, failed }));
            if (blockedInvoices.size > 0) {
                toast.warning(
                    t("accounting.billingDocument.repair.blocked", { invoiceNumbers: [...blockedInvoices].join(", ") })
                );
            }
            await loadTrips();
        } finally {
            setRepairing(false);
            setRepairProgress(null);
        }
    }

    // A review filter (plate, vehicle class or month) narrows the preview only — never the invoice — so
    // Download is blocked while one is active, keeping the invoice and preview sets from diverging
    // into a wrong bill (ADR 0005 §3).
    const reviewFilterActive =
        plateFilter !== PLATE_FILTER_ALL ||
        vehicleClassFilter !== VEHICLE_CLASS_FILTER_ALL ||
        reviewMonthFilter !== MONTH_FILTER_ALL;
    // Rows on screen were cut on a different axis than this customer bills on — downloading them would
    // invoice the wrong set (the plan-date leak ADR 0027 closes). Reload first.
    const staleBasis = trips.length > 0 && loaded !== null && loaded.basis !== selectedBasis;
    const canDownload = selectedCustomerId !== "all" && filteredTrips.length > 0 && !reviewFilterActive && !staleBasis;
    const periodHint =
        selectedCustomerId === "all"
            ? t("accounting.billingDocument.filters.periodHint.all")
            : selectedBasis === "plan"
              ? t("accounting.billingDocument.filters.periodHint.plan")
              : t("accounting.billingDocument.filters.periodHint.delivered");

    return (
        <PagePermissionGuard capability={CAPABILITIES.accounting_billing_document}>
            <div className="p-6 space-y-6">
                <div>
                    <h1 className="text-2xl font-bold">{t("nav.billingDocument")}</h1>
                    <p className="text-muted-foreground text-sm mt-1">
                        {t("accounting.billingDocument.subtitle")}
                    </p>
                </div>

                {/* ── Filters ── */}
                <Card>
                    <CardHeader>
                        <CardTitle className="text-base">{t("accounting.billingDocument.filters.title")}</CardTitle>
                        <p className="text-xs text-muted-foreground">{periodHint}</p>
                    </CardHeader>
                    <CardContent className="flex flex-wrap gap-4 items-end">
                        {/* The billing period. Labelled by the axis Load will actually cut it on (ADR 0027). */}
                        <div className="space-y-1">
                            <Label className="flex items-center gap-1.5">
                                {selectedBasis === "plan"
                                    ? t("accounting.billingDocument.filters.planMonth")
                                    : t("accounting.billingDocument.filters.deliveredMonth")}
                                <Badge variant="secondary" className="text-[10px] px-1 py-0 font-normal">
                                    {t("accounting.billingDocument.filters.billingPeriodTag")}
                                </Badge>
                            </Label>
                            <Select value={String(selectedMonth)} onValueChange={(v) => setSelectedMonth(Number(v))}>
                                <SelectTrigger className="w-52">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    {MONTHS.map((m) => (
                                        <SelectItem key={m.value} value={String(m.value)}>{m.label}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        <div className="space-y-1">
                            <Label>{t("accounting.billingDocument.filters.year")}</Label>
                            <Select value={String(selectedYear)} onValueChange={(v) => setSelectedYear(Number(v))}>
                                <SelectTrigger className="w-32">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    {YEARS.map((y) => (
                                        <SelectItem key={y} value={String(y)}>{y}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        <div className="space-y-1">
                            <Label>{t("accounting.billingDocument.filters.customer")}</Label>
                            <Select value={selectedCustomerId} onValueChange={setSelectedCustomerId}>
                                <SelectTrigger className="w-52">
                                    <SelectValue placeholder={t("accounting.billingDocument.filters.customer")} />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("accounting.billingDocument.filters.allCustomers")}</SelectItem>
                                    {customers.map((c) => (
                                        <SelectItem key={c.id} value={c.id!}>{c.name}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        {/* ── Plate review filter (narrows preview only; blocks Download — ADR 0005) ── */}
                        {trips.length > 0 && (
                            <div className="space-y-1">
                                <Label>{t("accounting.billingDocument.filters.licensePlate")}</Label>
                                <PlateFilterCombobox
                                    options={plateOptions}
                                    value={plateFilter}
                                    onChange={setPlateFilter}
                                    keyPrefix="accounting.billingDocument.filters"
                                    className="w-52"
                                />
                            </div>
                        )}

                        {/* ── Vehicle-class review filter (same guard as plate — ADR 0005) ── */}
                        {trips.length > 0 && (
                            <div className="space-y-1">
                                <Label>{t("accounting.billingDocument.filters.vehicleClass")}</Label>
                                <Select value={vehicleClassFilter} onValueChange={setVehicleClassFilter}>
                                    <SelectTrigger className="w-52">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value={VEHICLE_CLASS_FILTER_ALL}>{t("accounting.billingDocument.filters.allVehicleClasses")}</SelectItem>
                                        {vehicleClassOptions.map((o) => (
                                            <SelectItem key={o.value} value={o.value}>
                                                {(o.value === VEHICLE_CLASS_FILTER_NONE ? t("accounting.billingDocument.filters.vehicleClassNotSpecified") : o.label)} ({o.count})
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                        )}

                        {/* ── Month review filter on the OTHER axis (same guard as plate — ADR 0005) ── */}
                        {trips.length > 0 && (
                            <div className="space-y-1">
                                <Label className="flex items-center gap-1.5">
                                    {reviewAxis === "plan"
                                        ? t("accounting.billingDocument.filters.planMonth")
                                        : t("accounting.billingDocument.filters.deliveredMonth")}
                                    <Badge variant="outline" className="text-[10px] px-1 py-0 font-normal">
                                        {t("accounting.billingDocument.filters.reviewTag")}
                                    </Badge>
                                </Label>
                                <Select value={reviewMonthFilter} onValueChange={setReviewMonthFilter}>
                                    <SelectTrigger className="w-60">
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value={MONTH_FILTER_ALL}>{t("accounting.billingDocument.filters.allMonths")}</SelectItem>
                                        {reviewMonthOptions.map((o) => (
                                            <SelectItem key={o.value} value={o.value}>
                                                {o.value === MONTH_FILTER_NONE
                                                    ? t("accounting.billingDocument.filters.monthNotSpecified")
                                                    : `${MONTHS[o.month - 1]?.label ?? o.value} ${o.year}`} ({o.count})
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>
                        )}

                        {/* ── Show/hide the actual pickup date, on-screen AND in the exported detail (ADR 0028) ── */}
                        <div className="space-y-1">
                            <Label>&nbsp;</Label>
                            <Button
                                type="button"
                                variant={showActualPickup ? "default" : "outline"}
                                onClick={() => setShowActualPickup((v) => !v)}
                                className="w-52"
                            >
                                {showActualPickup
                                    ? t("accounting.billingDocument.actualPickup.hide", "ซ่อนวันรับงานจริง")
                                    : t("accounting.billingDocument.actualPickup.show", "แสดงวันรับงานจริง")}
                            </Button>
                        </div>

                        {/* Clears the REVIEW filters only — never the charge-type / หลัก-เสริม
                            toggles below, which compose the invoice itself (ADR 0005 §1-3). Appears
                            exactly when Download is blocked, so it is the one-click way to unblock. */}
                        {reviewFilterActive && (
                            <div className="space-y-1">
                                <Label>&nbsp;</Label>
                                <Button
                                    variant="ghost"
                                    size="sm"
                                    onClick={() => {
                                        setPlateFilter(PLATE_FILTER_ALL);
                                        setVehicleClassFilter(VEHICLE_CLASS_FILTER_ALL);
                                        setReviewMonthFilter(MONTH_FILTER_ALL);
                                    }}
                                    className="h-9 whitespace-nowrap text-muted-foreground"
                                >
                                    <X className="h-4 w-4 mr-1.5" />
                                    {t("accounting.billingDocument.filters.clearReviewFilters")}
                                </Button>
                            </div>
                        )}

                        {/* ── Charge type toggles ── */}
                        {trips.length > 0 && (
                            <div className="space-y-1.5 border-l pl-4 ml-2">
                                <Label className="text-xs text-muted-foreground">ค่าบริการที่รวมในบิล</Label>
                                <div className="flex flex-col gap-2">
                                    <label className="flex items-center gap-2 cursor-pointer text-sm">
                                        <Checkbox
                                            checked={includeTrips}
                                            onCheckedChange={(v) => setIncludeTrips(!!v)}
                                        />
                                        <span className="flex items-center gap-1.5">
                                            <span className="w-2 h-2 rounded-full bg-blue-500 inline-block" />
                                            เที่ยวปกติ
                                            <Badge variant="secondary" className="text-xs px-1.5 py-0">{typeCounts.trips}</Badge>
                                        </span>
                                    </label>
                                    <label className="flex items-center gap-2 cursor-pointer text-sm">
                                        <Checkbox
                                            checked={includeStandby}
                                            onCheckedChange={(v) => setIncludeStandby(!!v)}
                                        />
                                        <span className="flex items-center gap-1.5">
                                            <span className="w-2 h-2 rounded-full bg-orange-500 inline-block" />
                                            Standby
                                            <Badge variant="secondary" className="text-xs px-1.5 py-0">{typeCounts.standby}</Badge>
                                        </span>
                                    </label>
                                    <label className="flex items-center gap-2 cursor-pointer text-sm">
                                        <Checkbox
                                            checked={includeMultiDrop}
                                            onCheckedChange={(v) => setIncludeMultiDrop(!!v)}
                                        />
                                        <span className="flex items-center gap-1.5">
                                            <span className="w-2 h-2 rounded-full bg-purple-500 inline-block" />
                                            Multi-drop stops
                                            <Badge variant="secondary" className="text-xs px-1.5 py-0">{typeCounts.multiDrop}</Badge>
                                        </span>
                                    </label>
                                </div>
                            </div>
                        )}

                        {/* ── Job category (หลัก/เสริม) toggles ── */}
                        {trips.length > 0 && (
                            <div className="space-y-1.5 border-l pl-4 ml-2">
                                <Label className="text-xs text-muted-foreground">{t("accounting.billingDocument.table.jobCategory")}</Label>
                                <div className="flex flex-col gap-2">
                                    <label className="flex items-center gap-2 cursor-pointer text-sm">
                                        <Checkbox
                                            checked={includePrimary}
                                            onCheckedChange={(v) => setIncludePrimary(!!v)}
                                        />
                                        <span className="flex items-center gap-1.5">
                                            {t("accounting.billingDocument.badge.jobCategoryPrimary")}
                                            <Badge variant="secondary" className="text-xs px-1.5 py-0">{jobCategoryCounts.primary}</Badge>
                                        </span>
                                    </label>
                                    <label className="flex items-center gap-2 cursor-pointer text-sm">
                                        <Checkbox
                                            checked={includeSupplementary}
                                            onCheckedChange={(v) => setIncludeSupplementary(!!v)}
                                        />
                                        <span className="flex items-center gap-1.5">
                                            {t("accounting.billingDocument.badge.jobCategorySupplementary")}
                                            <Badge variant="secondary" className="text-xs px-1.5 py-0">{jobCategoryCounts.supplementary}</Badge>
                                        </span>
                                    </label>
                                    {jobCategoryCounts.unverified > 0 && (
                                        <label className="flex items-center gap-2 cursor-pointer text-sm">
                                            <Checkbox
                                                checked={includeUnverified}
                                                onCheckedChange={(v) => setIncludeUnverified(!!v)}
                                            />
                                            <span className="flex items-center gap-1.5 text-red-600">
                                                {t("accounting.billingDocument.badge.jobCategoryUnknown")}
                                                <Badge variant="secondary" className="text-xs px-1.5 py-0">{jobCategoryCounts.unverified}</Badge>
                                            </span>
                                        </label>
                                    )}
                                </div>
                            </div>
                        )}

                        <Button onClick={loadTrips} disabled={loading} variant="outline">
                            {loading ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <RefreshCw className="h-4 w-4 mr-2" />}
                            {t("accounting.billingDocument.filters.load")}
                        </Button>
                    </CardContent>
                </Card>

                {/* ── Standby that produced no billable row (ADR 0008 §6) — never part of the invoice set ── */}
                <UnpricedStandbyPanel
                    diagnostics={standbyDiagnostics}
                    customers={customers.map((c) => ({ id: c.id!, name: c.name }))}
                    onFixed={loadTrips}
                    canRepair={isAdmin}
                />

                {/* ── Rows whose price or period disagrees with the plan / หลัก-เสริม (ADR 0027, ADR-0005) ── */}
                {hasBillingIssues && (
                    <Card className="border-amber-500/60">
                        <CardContent className="pt-4 space-y-3">
                            <div className="flex items-start gap-2">
                                <AlertTriangle className="h-4 w-4 text-amber-500 mt-0.5 shrink-0" />
                                <div className="space-y-1 text-sm">
                                    <p className="font-medium">{t("accounting.billingDocument.repair.title")}</p>
                                    <ul className="list-disc pl-5 text-muted-foreground space-y-0.5">
                                        {billingIssues.missingBillingDate.length > 0 && (
                                            <li className="text-red-600">
                                                {t("accounting.billingDocument.repair.missingBillingDate", { count: billingIssues.missingBillingDate.length })}
                                            </li>
                                        )}
                                        {billingIssues.missingCheckFailed && (
                                            <li className="text-red-600">{t("accounting.billingDocument.repair.missingCheckFailed")}</li>
                                        )}
                                        {billingIssues.suppWithFuel.length > 0 && (
                                            <li className="text-red-600">
                                                {t("accounting.billingDocument.repair.suppWithFuel", { count: billingIssues.suppWithFuel.length })}
                                            </li>
                                        )}
                                        {billingIssues.outOfPeriod.length > 0 && (
                                            <li>{t("accounting.billingDocument.repair.outOfPeriod", { count: billingIssues.outOfPeriod.length })}</li>
                                        )}
                                        {billingIssues.drifted.length > 0 && (
                                            <li>{t("accounting.billingDocument.repair.drifted", { count: billingIssues.drifted.length })}</li>
                                        )}
                                        {billingIssues.standbyOtherMonth > 0 && (
                                            <li>{t("accounting.billingDocument.repair.standbyOtherMonth", { count: billingIssues.standbyOtherMonth })}</li>
                                        )}
                                        {billingIssues.noPlanDate > 0 && (
                                            <li>{t("accounting.billingDocument.repair.noPlanDate", { count: billingIssues.noPlanDate })}</li>
                                        )}
                                        {billingIssues.suppManualPrice.length > 0 && (
                                            <li>{t("accounting.billingDocument.repair.suppManualPrice", { count: billingIssues.suppManualPrice.length })}</li>
                                        )}
                                    </ul>
                                    {loaded?.basis === "plan" && (
                                        <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.repair.limitation")}</p>
                                    )}
                                </div>
                            </div>
                            {billingIssues.tripIds.length > 0 && (
                                isAdmin ? (
                                    <Button size="sm" variant="outline" onClick={repairBillingIssues} disabled={repairing || loading}>
                                        {repairing ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <Wrench className="h-4 w-4 mr-2" />}
                                        {repairing
                                            ? t("accounting.billingDocument.repair.running", {
                                                  done: repairProgress?.done ?? 0,
                                                  total: repairProgress?.total ?? billingIssues.tripIds.length,
                                              })
                                            : t("accounting.billingDocument.repair.button", { count: billingIssues.tripIds.length })}
                                    </Button>
                                ) : (
                                    <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.repair.adminOnly")}</p>
                                )
                            )}
                        </CardContent>
                    </Card>
                )}

                {/* ── Summary cards ── */}
                {trips.length > 0 && (
                    <div className="space-y-3">
                        {/* Breakdown by type */}
                        <div className="grid grid-cols-3 gap-3">
                            <Card className="border-l-4 border-l-blue-500">
                                <CardContent className="pt-4 pb-3">
                                    <p className="text-xs text-muted-foreground">เที่ยวปกติ</p>
                                    <p className="text-xl font-bold">{breakdown.tripOnlyCount} เที่ยว</p>
                                    <p className="text-sm font-mono text-blue-600">{formatThb(breakdown.tripSubtotal)}</p>
                                </CardContent>
                            </Card>
                            <Card className="border-l-4 border-l-orange-500">
                                <CardContent className="pt-4 pb-3">
                                    <p className="text-xs text-muted-foreground">Standby</p>
                                    <p className="text-xl font-bold">{breakdown.standbyCount} ครั้ง</p>
                                    <p className="text-sm font-mono text-orange-600">{formatThb(breakdown.standbySubtotal)}</p>
                                </CardContent>
                            </Card>
                            <Card className="border-l-4 border-l-purple-500">
                                <CardContent className="pt-4 pb-3">
                                    <p className="text-xs text-muted-foreground">Multi-drop stops</p>
                                    <p className="text-xl font-bold">{breakdown.multiDropCount} จุด</p>
                                    <p className="text-sm font-mono text-purple-600">{formatThb(breakdown.multiDropSubtotal)}</p>
                                </CardContent>
                            </Card>
                        </div>
                        {/* Totals */}
                        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                            <Card>
                                <CardContent className="pt-4">
                                    <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.tripCount")}</p>
                                    <p className="text-2xl font-bold">{previewTrips.length}</p>
                                </CardContent>
                            </Card>
                            <Card>
                                <CardContent className="pt-4">
                                    <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.total")}</p>
                                    <p className="text-2xl font-bold">{formatThb(grandTotal)}</p>
                                </CardContent>
                            </Card>
                            <Card>
                                <CardContent className="pt-4">
                                    <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.withholdingTax")}</p>
                                    <p className="text-2xl font-bold text-red-600">-{formatThb(withholdingTax)}</p>
                                </CardContent>
                            </Card>
                            <Card>
                                <CardContent className="pt-4">
                                    <p className="text-xs text-muted-foreground">{t("accounting.billingDocument.summary.netTotal")}</p>
                                    <p className="text-2xl font-bold text-green-600">{formatThb(totalNet)}</p>
                                </CardContent>
                            </Card>
                        </div>
                    </div>
                )}

                {/* ── Download button ── */}
                {filteredTrips.length > 0 && (
                    <Card>
                        <CardContent className="pt-4 flex items-center justify-between">
                            <div className="flex items-center gap-2 text-sm text-muted-foreground">
                                <FileText className="h-4 w-4" />
                                <span>
                                    {t("accounting.billingDocument.download.filesInfo")} <code>invoice_summary.pdf</code>, <code>invoice_detail.xlsx</code>, <code>receipt.pdf</code>
                                </span>
                            </div>
                            <Button
                                onClick={handleDownload}
                                disabled={!canDownload || generating}
                            >
                                {generating ? <Loader2 className="h-4 w-4 animate-spin mr-2" /> : <Download className="h-4 w-4 mr-2" />}
                                Download Billing Package (.zip)
                            </Button>
                        </CardContent>
                        {!canDownload && selectedCustomerId === "all" && (
                            <CardContent className="pt-0">
                                <p className="text-xs text-amber-600">{t("accounting.billingDocument.download.selectCustomerWarning")}</p>
                            </CardContent>
                        )}
                        {reviewFilterActive && (
                            <CardContent className="pt-0">
                                <p className="text-xs text-amber-600">{t("accounting.billingDocument.download.reviewFilterActive")}</p>
                            </CardContent>
                        )}
                        {staleBasis && (
                            <CardContent className="pt-0">
                                <p className="text-xs text-amber-600">{t("accounting.billingDocument.download.staleBasis")}</p>
                            </CardContent>
                        )}
                    </Card>
                )}

                {/* ── Trip preview table ── */}
                {filteredTrips.length > 0 ? (
                    <Card>
                        <CardHeader><CardTitle className="text-base">{t("accounting.billingDocument.table.title", { count: previewTrips.length })}</CardTitle></CardHeader>
                        <CardContent className="p-0">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead>{t("accounting.billingDocument.table.tripNumber")}</TableHead>
                                        {/* วันแผนงาน (ADR 0027) — the axis a plan-basis customer's invoice is built on,
                                            so it is readable beside the delivery date it can disagree with. */}
                                        <TableHead>{t("accounting.billingDocument.table.planDate")}</TableHead>
                                        <TableHead>{t("accounting.billingDocument.table.deliveredDate")}</TableHead>
                                        {showActualPickup && (
                                            <TableHead>{t("accounting.billingDocument.table.actualPickup", "วันรับงานจริง")}</TableHead>
                                        )}
                                        <TableHead>{t("accounting.billingDocument.table.route")}</TableHead>
                                        <TableHead>{t("accounting.billingDocument.table.vehicleType")}</TableHead>
                                        <TableHead>{t("accounting.billingDocument.table.driver")}</TableHead>
                                        <TableHead>{t("accounting.billingDocument.table.jobCategory")}</TableHead>
                                        <TableHead className="text-right">{t("accounting.billingDocument.table.billingAmount")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {previewTrips.map((trip) => {
                                        // Origin shows the hub CODE (e.g. SPK-GW) for every customer, so the preview matches
                                        // both the invoice PDF (groupToLineItems) and the Excel detail sheet. Destination stays
                                        // the display NAME — the two ends of the route are deliberately asymmetric (ADR-0005).
                                        const originDisplay = trip.originHubCode || trip.billingLookupHubId || trip.hubDisplayName || "-";
                                        const destDisplay   = trip.destinationDisplayName ?? trip.billingLookupDestination ?? "-";
                                        return (
                                        <TableRow key={trip.id} className={trip.rowType === "standby" ? "bg-amber-950/20" : trip.rowType === "multidrop_stop" ? "bg-blue-950/10" : undefined}>
                                            <TableCell className="font-mono text-xs">
                                                <div className="flex flex-col gap-1">
                                                    <span>{trip.spxTripId ?? trip.id.slice(0, 8)}</span>
                                                    {trip.rowType === "standby" && (
                                                        <Badge variant="outline" className="w-fit text-amber-400 border-amber-600 text-[10px] px-1 py-0">
                                                            {t("accounting.billingDocument.badge.standby")}
                                                        </Badge>
                                                    )}
                                                    {trip.rowType === "multidrop_stop" && (
                                                        <Badge variant="outline" className="w-fit text-purple-400 border-purple-600 text-[10px] px-1 py-0">
                                                            {t("accounting.billingDocument.badge.stopN", { n: trip.stopIndex ?? 0 })}
                                                        </Badge>
                                                    )}
                                                    {trip.rowType === "trip" && (
                                                        <Badge variant="outline" className="w-fit text-blue-400 border-blue-600 text-[10px] px-1 py-0">
                                                            {t("accounting.billingDocument.badge.trip")}
                                                        </Badge>
                                                    )}
                                                </div>
                                            </TableCell>
                                            <TableCell className="text-xs whitespace-nowrap">
                                                {trip.planDate ? (
                                                    planDateDrifted(trip) ? (
                                                        <span
                                                            className="text-amber-500"
                                                            title={t("accounting.billingDocument.table.planDateDrift")}
                                                        >
                                                            {format(trip.planDate, "dd/MM/yyyy")} ⚠
                                                        </span>
                                                    ) : (
                                                        format(trip.planDate, "dd/MM/yyyy")
                                                    )
                                                ) : "-"}
                                            </TableCell>
                                            <TableCell className="text-xs">
                                                {trip.deliveredTimestamp ? format(trip.deliveredTimestamp, "dd/MM/yyyy HH:mm") : "-"}
                                            </TableCell>
                                            {showActualPickup && (
                                                <TableCell className="text-xs whitespace-nowrap">
                                                    {trip.actualPickupAt ? format(trip.actualPickupAt, "dd/MM/yyyy HH:mm") : "-"}
                                                </TableCell>
                                            )}
                                            <TableCell className="text-xs">
                                                {[originDisplay, destDisplay].filter(Boolean).join(" → ")}
                                            </TableCell>
                                            <TableCell>
                                                {trip.vehicleClass ? <Badge variant="outline">{trip.vehicleClass}</Badge> : "-"}
                                            </TableCell>
                                            <TableCell className="text-xs">{trip.driverName ?? "-"}</TableCell>
                                            <TableCell>
                                                {trip.jobCategory === "SUPPLEMENTARY" ? (
                                                    <div className="flex flex-col gap-1">
                                                        <Badge variant="outline" className="w-fit text-amber-400 border-amber-600">
                                                            {t("accounting.billingDocument.badge.jobCategorySupplementary")}
                                                        </Badge>
                                                        {suppPricedWithFuel(trip) && (
                                                            <Badge variant="outline" className="w-fit text-red-600 border-red-500 text-[10px] px-1 py-0">
                                                                {t("accounting.billingDocument.badge.suppWithFuel")}
                                                            </Badge>
                                                        )}
                                                        {suppManualPriceWithFuel(trip) && (
                                                            <Badge variant="outline" className="w-fit text-muted-foreground text-[10px] px-1 py-0">
                                                                {t("accounting.billingDocument.badge.suppManualPrice")}
                                                            </Badge>
                                                        )}
                                                    </div>
                                                ) : trip.jobCategory === "PRIMARY" ? (
                                                    <Badge variant="outline">
                                                        {t("accounting.billingDocument.badge.jobCategoryPrimary")}
                                                    </Badge>
                                                ) : (
                                                    <Badge variant="outline" className="text-red-600 border-red-500">
                                                        {t("accounting.billingDocument.badge.jobCategoryUnknown")}
                                                    </Badge>
                                                )}
                                            </TableCell>
                                            <TableCell className="text-right font-mono">
                                                {formatThb(trip.billingEstimateThb)}
                                            </TableCell>
                                        </TableRow>
                                        );
                                    })}
                                </TableBody>
                            </Table>
                        </CardContent>
                    </Card>
                ) : !loading && (
                    <Card>
                        <CardContent className="pt-6 text-center text-muted-foreground text-sm">
                            {t("accounting.billingDocument.empty")}
                        </CardContent>
                    </Card>
                )}
            </div>
        </PagePermissionGuard>
    );
}
