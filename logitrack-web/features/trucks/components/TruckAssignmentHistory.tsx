"use client";

import Link from "next/link";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { TruckQueryLoading } from "@/features/trucks/components/TruckQueryLoading";
import { TruckQueryError } from "@/features/trucks/components/TruckQueryError";
import { useLanguage } from "@/context/language";
import { useAssignmentsOf } from "../api/useTrucks";

export function TruckAssignmentHistory({ truckId }: { truckId: string }) {
    const query = useAssignmentsOf({ truckId });
    const { t, language } = useLanguage();
    return <Card>
        <CardHeader><CardTitle className="text-base">{t("assignments.history")}</CardTitle></CardHeader>
        <CardContent>
            {query.isError && <TruckQueryError onRetry={() => void query.refetch()} retrying={query.isFetching} />}
            {query.isPending ? <TruckQueryLoading label={t("common.loading")} /> : query.data && (query.assignments.length === 0
                ? <p className="text-sm text-muted-foreground">{t("assignments.table.noHistory")}</p>
                : <ul className="divide-y">{query.assignments.map((assignment) => <li key={assignment.id} className="flex flex-wrap items-center justify-between gap-2 py-3 text-sm">
                    <div><Link className="text-primary underline underline-offset-4" href={`/app/drivers/view?id=${assignment.driverId}`} prefetch={false}>{assignment.driverName || t("trucks.ui4.notAvailable")}</Link>
                        <p className="text-xs text-muted-foreground">{assignment.createdAt ? new Date(assignment.createdAt).toLocaleDateString(language === "th" ? "th-TH" : "en-US") : "-"}</p></div>
                    <Badge variant="secondary">{t(assignment.status === "active" ? "assignments.status.active" : "assignments.status.revoked")}</Badge>
                </li>)}</ul>)}
        </CardContent>
    </Card>;
}
