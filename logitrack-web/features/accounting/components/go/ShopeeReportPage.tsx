"use client";

/**
 * Shopee Express report over Go (T42, P3; Appendix E §E.2.3 row "shopee-express-report"; R69).
 *
 * `['billing','shopee',cust,y,m,half]` over `GET /v1/billing/shopee-report` reads one customer's delivered
 * trips of the round on the server (the Firestore page read every customer's trips of the period and
 * filtered in the browser, `billing.ts:1264-1270`). The PDF is rendered by Go: `GET
 * /v1/billing/shopee-report.pdf` answers `302` to the newest current render, or `202 {jobId}` while one
 * renders; the page then follows `['jobs',id]` (SSE `job.updated`) and downloads once it succeeds.
 * No jspdf in the browser.
 */
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { createColumnHelper } from "@tanstack/react-table";
import { Download, FileText, Loader2, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { isJobFinished, useJob } from "@/features/jobs/api/jobs";
import { apiErrorText } from "@/lib/apiError";
import { downloadFromGo } from "@/lib/goDownload";
import { CustomerSearchSelect } from "@/components/entity-pickers";
import { VirtualTable } from "@/components/virtual-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

import { SHOPEE_PDF_PATH, shopeePdfQuery, shopeeReportQuery, type ShopeeHalf, type ShopeeTripDTO } from "../../api/billing.go";
import { customerSearchQuery } from "../../api/pickers.go";
import { ApiErrorAlert, formatDateTime, MONTHS, monthName, yearOptions } from "./shared";

interface Loaded {
    customerId: string;
    year: number;
    month: number;
    half: ShopeeHalf;
}

const col = createColumnHelper<ShopeeTripDTO>();

export function ShopeeReportPage() {
    const { t, language } = useLanguage();
    const now = new Date();
    const [month, setMonth] = useState(now.getMonth() + 1);
    const [year, setYear] = useState(now.getFullYear());
    const [half, setHalf] = useState<ShopeeHalf>("full");
    const [customerId, setCustomerId] = useState("");
    const [loaded, setLoaded] = useState<Loaded | null>(null);
    const [jobId, setJobId] = useState<string | null>(null);
    const [downloading, setDownloading] = useState(false);

    // Default to the TTP customer, as the Firestore page did, else the first customer by code: the picker's
    // server search for "TTP" (and its first page only when no customer has that code), never every customer.
    const ttpPage = useQuery({ ...customerSearchQuery("TTP"), enabled: customerId === "" });
    const ttp = ttpPage.data?.find((c) => c.code.trim().toUpperCase() === "TTP");
    const firstPage = useQuery({ ...customerSearchQuery(""), enabled: customerId === "" && ttpPage.isSuccess && !ttp });
    useEffect(() => {
        if (customerId) return;
        const pick = ttp ?? firstPage.data?.[0];
        if (pick) setCustomerId(pick.id);
    }, [customerId, ttp, firstPage.data]);

    const report = useQuery(shopeeReportQuery(loaded?.customerId ?? "", loaded?.year ?? 0, loaded?.month ?? 0, loaded?.half ?? "full", loaded !== null));
    const job = useJob(jobId);
    const trips = useMemo(() => report.data?.trips ?? [], [report.data]);

    const download = async (ctx: Loaded) => {
        setDownloading(true);
        try {
            const probe = await downloadFromGo(SHOPEE_PDF_PATH, shopeePdfQuery(ctx.customerId, ctx.year, ctx.month, ctx.half));
            if (probe.kind === "accepted") setJobId(probe.jobId);
            else setJobId(null);
        } catch (e) {
            toast.error(`${t("accounting.shopeeReport.generateError")} ${apiErrorText(e, t)}`);
            setJobId(null);
        } finally {
            setDownloading(false);
        }
    };

    // When the render job finishes, download (succeeded) or say why not (failed).
    useEffect(() => {
        if (!jobId || !loaded || !isJobFinished(job.data)) return;
        const finished = job.data;
        setJobId(null);
        if (finished?.status === "succeeded") void download(loaded);
        else toast.error(`${t("accounting.shopeeReport.generateError")} ${finished?.error ?? ""}`);
        // download is stable for this purpose; the effect runs once per finished job.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [job.data, jobId]);

    const columns = useMemo(
        () => [
            col.accessor("deliveredAt", { header: () => t("accounting.shopeeReport.table.deliveredDate"), cell: (c) => formatDateTime(c.getValue()), meta: { cellClassName: "whitespace-nowrap text-xs" } }),
            col.accessor("tripNo", { header: () => t("accounting.shopeeReport.table.tripNumber"), meta: { cellClassName: "font-mono text-xs" } }),
            col.display({
                id: "route",
                header: () => t("accounting.shopeeReport.table.route"),
                cell: ({ row: { original: r } }) => [r.originDisplay, r.destinationDisplay].filter(Boolean).join(" → "),
                meta: { cellClassName: "text-xs" },
            }),
            col.accessor("vehicleClass", { header: () => t("accounting.shopeeReport.table.vehicleType"), cell: (c) => c.getValue() ?? "—" }),
            col.accessor("truckLicensePlate", { header: () => t("accounting.shopeeReport.table.plate"), cell: (c) => c.getValue() ?? "—", meta: { cellClassName: "text-xs" } }),
            col.accessor("driverName", { header: () => t("accounting.shopeeReport.table.driver"), meta: { cellClassName: "text-xs" } }),
            col.display({
                id: "runsheets",
                header: () => t("accounting.shopeeReport.table.runsheets"),
                cell: ({ row: { original: r } }) =>
                    r.signedRunsheetPhotos.length > 0 ? (
                        <Badge variant="secondary">{r.signedRunsheetPhotos.length}</Badge>
                    ) : (
                        <Badge variant="outline" className="border-red-500 text-red-600">
                            0
                        </Badge>
                    ),
            }),
        ],
        [t]
    );

    const busy = downloading || (jobId !== null && !isJobFinished(job.data));
    const stale = loaded !== null && (loaded.customerId !== customerId || loaded.year !== year || loaded.month !== month || loaded.half !== half);

    return (
        <div className="flex flex-col gap-section">
            <PageHeader title={t("nav.shopeeExpressReport")} description={t("accounting.shopeeReport.subtitle")} />

            <Card>
                <CardHeader>
                    <CardTitle className="text-base">{t("accounting.shopeeReport.filters.title")}</CardTitle>
                </CardHeader>
                <CardContent className="flex flex-wrap items-end gap-4">
                    <div className="space-y-1">
                        <Label>{t("accounting.shopeeReport.filters.month")}</Label>
                        <Select value={String(month)} onValueChange={(v) => setMonth(Number(v))}>
                            <SelectTrigger className="w-44">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {MONTHS.map((m) => (
                                    <SelectItem key={m} value={String(m)}>
                                        {monthName(m, language)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.shopeeReport.filters.year")}</Label>
                        <Select value={String(year)} onValueChange={(v) => setYear(Number(v))}>
                            <SelectTrigger className="w-28">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {yearOptions().map((y) => (
                                    <SelectItem key={y} value={String(y)}>
                                        {y}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.shopeeReport.filters.round")}</Label>
                        <Select value={half} onValueChange={(v) => setHalf(v as ShopeeHalf)}>
                            <SelectTrigger className="w-44">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {(["full", "first", "second"] as const).map((h) => (
                                    <SelectItem key={h} value={h}>
                                        {t(`accounting.shopeeReport.round.${h}`)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="space-y-1">
                        <Label>{t("accounting.shopeeReport.filters.customer")}</Label>
                        <CustomerSearchSelect
                            aria-label={t("accounting.shopeeReport.filters.customer")}
                            value={customerId}
                            onValueChange={setCustomerId}
                            className="min-w-[200px]"
                        />
                    </div>
                    <Button
                        variant="outline"
                        disabled={!customerId || report.isFetching}
                        onClick={() => {
                            const next = { customerId, year, month, half };
                            if (loaded && !stale) void report.refetch();
                            else setLoaded(next);
                        }}
                    >
                        {report.isFetching ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <RefreshCw className="mr-2 h-4 w-4" />}
                        {t("accounting.shopeeReport.filters.load")}
                    </Button>
                </CardContent>
            </Card>

            <ApiErrorAlert error={report.error} title={t("accounting.shopeeReport.loadError")} />

            {report.data ? (
                <>
                    <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.shopeeReport.summary.trips")}</p>
                                <p className="text-2xl font-bold">{report.data.tripCount}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.shopeeReport.summary.drivers")}</p>
                                <p className="text-2xl font-bold">{report.data.driverCount}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.shopeeReport.summary.signedPhotos")}</p>
                                <p className="text-2xl font-bold">{report.data.photoCount}</p>
                            </CardContent>
                        </Card>
                        <Card>
                            <CardContent className="pt-4">
                                <p className="text-xs text-muted-foreground">{t("accounting.shopeeReport.summary.missing")}</p>
                                <p className={`text-2xl font-bold ${report.data.missingPhotoCount > 0 ? "text-red-600" : ""}`}>{report.data.missingPhotoCount}</p>
                            </CardContent>
                        </Card>
                    </div>
                    {report.data.missingPhotoCount > 0 ? (
                        <p className="text-sm text-amber-600">{t("accounting.shopeeReport.missingWarning", { count: report.data.missingPhotoCount })}</p>
                    ) : null}

                    {trips.length > 0 ? (
                        <Card>
                            <CardContent className="flex flex-wrap items-center justify-between gap-3 pt-4">
                                <div className="flex items-center gap-2 text-sm text-muted-foreground">
                                    <FileText className="h-4 w-4" />
                                    <span>{t("accounting.shopeeReport.download.info")}</span>
                                </div>
                                <Button onClick={() => loaded && void download(loaded)} disabled={busy || stale}>
                                    {busy ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Download className="mr-2 h-4 w-4" />}
                                    {jobId ? t("accounting.shopeeReport.download.rendering") : t("accounting.shopeeReport.download.button")}
                                </Button>
                            </CardContent>
                        </Card>
                    ) : null}

                    <Card>
                        <CardHeader>
                            <CardTitle className="text-base">{t("accounting.shopeeReport.table.title", { count: trips.length })}</CardTitle>
                        </CardHeader>
                        <CardContent className="p-0">
                            <VirtualTable
                                data={trips}
                                columns={columns}
                                getRowId={(r) => r.id}
                                aria-label={t("accounting.shopeeReport.table.title", { count: trips.length })}
                                emptyState={<p className="py-8 text-center text-sm text-muted-foreground">{t("accounting.shopeeReport.empty")}</p>}
                                resetScrollKey={JSON.stringify(loaded)}
                            />
                        </CardContent>
                    </Card>
                </>
            ) : null}
        </div>
    );
}
