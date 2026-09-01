"use client";

import { useMemo, useState } from "react";
import { endOfDay, format, parse, startOfDay, startOfMonth } from "date-fns";
import { enUS, th as thDateLocale } from "date-fns/locale";
import { FolderArchive, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { DateOnlyRangePicker } from "@/components/ui/date-range-picker";
import { Label } from "@/components/ui/label";
import {
    buildBatchZipEntriesFromRows,
    downloadImagesAsZip,
    shouldSuggestCorsHint,
    type ZipBatchExpenseLike,
} from "@/lib/download-image-urls-zip";

export function AccountingBatchImagesZipCard({
    records,
    kind,
}: {
    records: ZipBatchExpenseLike[];
    kind: "fuel" | "other";
}) {
    const { t, language } = useLanguage();
    const dateLocale = language === "th" ? thDateLocale : enUS;
    const [dateFrom, setDateFrom] = useState(() => format(startOfMonth(new Date()), "yyyy-MM-dd"));
    const [dateTo, setDateTo] = useState(() => format(new Date(), "yyyy-MM-dd"));
    const [zipping, setZipping] = useState(false);

    const rowsInRange = useMemo(() => {
        const fromD = startOfDay(parse(dateFrom, "yyyy-MM-dd", new Date()));
        const toD = endOfDay(parse(dateTo, "yyyy-MM-dd", new Date()));
        if (Number.isNaN(fromD.getTime()) || Number.isNaN(toD.getTime())) return [];
        if (fromD > toD) return [];
        return records.filter((r) => r.date >= fromD && r.date <= toD);
    }, [records, dateFrom, dateTo]);

    const zipEntries = useMemo(() => buildBatchZipEntriesFromRows(rowsInRange), [rowsInRange]);

    const handleDownload = async () => {
        const fromD = startOfDay(parse(dateFrom, "yyyy-MM-dd", new Date()));
        const toD = endOfDay(parse(dateTo, "yyyy-MM-dd", new Date()));
        if (Number.isNaN(fromD.getTime()) || Number.isNaN(toD.getTime())) {
            toast.error(t("accounting.batchZip.invalidRange"));
            return;
        }
        if (fromD > toD) {
            toast.error(t("accounting.batchZip.invalidRange"));
            return;
        }
        if (rowsInRange.length === 0) {
            toast.error(t("accounting.batchZip.noRecordsInRange"));
            return;
        }
        if (zipEntries.length === 0) {
            toast.error(t("accounting.batchZip.noImagesInRange"));
            return;
        }
        if (zipping) return;

        const zipFilename = `${kind}-expense-images_${format(fromD, "yyyyMMdd")}_${format(toD, "yyyyMMdd")}.zip`;

        setZipping(true);
        try {
            const result = await downloadImagesAsZip(zipFilename, zipEntries);
            if (result.added.length === 0) {
                toast.error(t("accounting.error.zipFailed"));
                if (shouldSuggestCorsHint(result.failed)) {
                    toast.message(t("accounting.error.corsHint"), { duration: 10_000 });
                }
                return;
            }
            if (result.failed.length > 0) {
                toast.warning(
                    t("accounting.error.partialDownload", {
                        failed: result.failed.length,
                        added: result.added.length,
                    })
                );
            }
        } catch {
            toast.error(t("accounting.error.zipFailed"));
        } finally {
            setZipping(false);
        }
    };

    return (
        <Card>
            <CardHeader className="pb-3">
                <CardTitle className="text-base">{t("accounting.batchZip.title")}</CardTitle>
                <p className="text-sm text-muted-foreground font-normal">{t("accounting.batchZip.hint")}</p>
            </CardHeader>
            <CardContent className="flex flex-col gap-4 sm:flex-row sm:flex-wrap sm:items-end">
                <div className="flex flex-col gap-1.5">
                    <Label className="text-xs text-muted-foreground">{t("accounting.batchZip.dateRange")}</Label>
                    <DateOnlyRangePicker
                        from={dateFrom}
                        to={dateTo}
                        onChange={(from, to) => {
                            setDateFrom(from);
                            setDateTo(to);
                        }}
                        locale={dateLocale}
                        disabled={zipping}
                        className="w-[230px]"
                    />
                </div>
                <Button
                    type="button"
                    variant="secondary"
                    className="gap-2"
                    disabled={zipping}
                    onClick={() => void handleDownload()}
                >
                    {zipping ? (
                        <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                        <FolderArchive className="h-4 w-4" />
                    )}
                    {zipping ? t("accounting.batchZip.loading") : t("accounting.batchZip.download")}
                </Button>
                <p className="text-xs text-muted-foreground sm:w-full">
                    {t("accounting.batchZip.summary", {
                        records: rowsInRange.length,
                        images: zipEntries.length,
                    })}
                </p>
            </CardContent>
        </Card>
    );
}
