"use client";

/** The หลัก / เสริม / ตรวจสอบ badge of the Go accounting pages (T42), as the Firestore pages show it. */
import { useLanguage } from "@/context/language";
import { Badge } from "@/components/ui/badge";

export function JobCategoryBadge({ value, kind }: { value: string | null | undefined; kind?: string }) {
    const { t } = useLanguage();
    if (kind === "standby") return <span className="text-muted-foreground">—</span>;
    if (value === "SUPPLEMENTARY")
        return (
            <Badge variant="outline" className="border-amber-300 bg-amber-50 px-1 py-0 text-[10px] text-amber-700 dark:bg-amber-950/30 dark:text-amber-400">
                {t("accounting.billingDocument.badge.jobCategorySupplementary")}
            </Badge>
        );
    if (value === "PRIMARY")
        return (
            <Badge variant="outline" className="border-slate-300 bg-slate-50 px-1 py-0 text-[10px] text-slate-700 dark:bg-slate-900/30 dark:text-slate-300">
                {t("accounting.billingDocument.badge.jobCategoryPrimary")}
            </Badge>
        );
    return (
        <Badge variant="outline" className="border-red-300 bg-red-50 px-1 py-0 text-[10px] text-red-700 dark:bg-red-950/30 dark:text-red-400">
            {t("accounting.billingDocument.badge.jobCategoryUnknown")}
        </Badge>
    );
}
