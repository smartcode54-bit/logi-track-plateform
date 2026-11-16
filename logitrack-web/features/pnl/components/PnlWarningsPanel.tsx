"use client";

import { AlertTriangle, Info } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { useLanguage } from "@/context/language";
import type { PnlResult } from "@/lib/pnlCompute";
import { EXCLUDING_CODES, fmtThb } from "../utils/format";

/** Banners (R19) and the fail-loud summary (R20): nothing excluded from totals goes unmentioned. */
export function PnlWarningsPanel({ result }: { result: PnlResult }) {
    const { t } = useLanguage();
    const { flags, statement, warnings } = result;
    const sorted = [...warnings].sort(
        (a, b) => Number(EXCLUDING_CODES.includes(b.code)) - Number(EXCLUDING_CODES.includes(a.code)),
    );

    return (
        <div className="space-y-3">
            <div className="flex flex-col gap-2 text-sm">
                {flags.monthInProgress && (
                    <Banner tone="info">{t("pnl.banner.monthInProgress")}</Banner>
                )}
                {flags.hasPlanBasisCustomer && <Banner tone="info">{t("pnl.banner.planBasis")}</Banner>}
                <Banner tone="muted">{t("pnl.banner.liveCompute")}</Banner>
            </div>

            {(statement.excludedCount > 0 || sorted.length > 0) && (
                <Card className="border-amber-300 dark:border-amber-700">
                    <CardContent className="pt-4 space-y-2 text-sm">
                        {statement.excludedCount > 0 && (
                            <p className="font-medium flex items-center gap-2 text-amber-700 dark:text-amber-400">
                                <AlertTriangle className="w-4 h-4" />
                                {t("pnl.warn.excludedSummary", {
                                    count: statement.excludedCount,
                                    amount: fmtThb(statement.excludedRevenueThb),
                                })}
                            </p>
                        )}
                        <ul className="space-y-1">
                            {sorted.map((w) => (
                                <li key={w.code} className="flex flex-wrap gap-x-2">
                                    <span className={EXCLUDING_CODES.includes(w.code) ? "text-destructive font-medium" : "text-muted-foreground"}>
                                        {t(`pnl.warn.${w.code}`, {
                                            count: w.count,
                                            amount: w.amountThb !== undefined ? fmtThb(w.amountThb) : "—",
                                        })}
                                    </span>
                                    {w.refs.length > 0 && (
                                        <span className="text-xs text-muted-foreground truncate max-w-full">
                                            {w.refs.slice(0, 6).join(", ")}
                                            {w.refs.length > 6 ? ` +${w.refs.length - 6}` : ""}
                                        </span>
                                    )}
                                </li>
                            ))}
                        </ul>
                    </CardContent>
                </Card>
            )}
        </div>
    );
}

function Banner({ tone, children }: { tone: "info" | "muted"; children: React.ReactNode }) {
    return (
        <div
            className={
                tone === "info"
                    ? "flex items-start gap-2 rounded-md border border-blue-200 bg-blue-50 px-3 py-2 text-blue-800 dark:border-blue-900 dark:bg-blue-950 dark:text-blue-200"
                    : "flex items-start gap-2 rounded-md border px-3 py-2 text-muted-foreground"
            }
        >
            <Info className="w-4 h-4 mt-0.5 shrink-0" />
            <span>{children}</span>
        </div>
    );
}
