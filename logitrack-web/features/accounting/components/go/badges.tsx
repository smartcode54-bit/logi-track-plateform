"use client";

/** The หลัก / เสริม / ตรวจสอบ badge of the Go accounting pages (T42), as the Firestore pages show it. */
import { useLanguage } from "@/context/language";
import { Badge } from "@/components/ui/badge";

export function JobCategoryBadge({ value, kind }: { value: string | null | undefined; kind?: string }) {
    const { t } = useLanguage();
    if (kind === "standby") return <span className="text-muted-foreground">—</span>;
    if (value === "SUPPLEMENTARY")
        return (
            <Badge variant="warning">
                {t("accounting.billingDocument.badge.jobCategorySupplementary")}
            </Badge>
        );
    if (value === "PRIMARY")
        return (
            <Badge variant="outline">
                {t("accounting.billingDocument.badge.jobCategoryPrimary")}
            </Badge>
        );
    return (
        <Badge variant="destructive">
            {t("accounting.billingDocument.badge.jobCategoryUnknown")}
        </Badge>
    );
}
