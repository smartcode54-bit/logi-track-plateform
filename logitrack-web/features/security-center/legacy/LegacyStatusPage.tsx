"use client";

// The placeholder page (no data) of /app/security-center/status, unchanged (moved from its page.tsx by T54S):
// the route renders it while the `security` web flag is off and the Go page once it is on (developer-spec.md
// §10.13, §12.2). Deleted with the Firebase SDK in TW7.

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Server } from "lucide-react";
import { useLanguage } from "@/context/language";

export default function SystemStatusPage() {
    const { t } = useLanguage();
    return (
        <div className="p-6 space-y-6">
            <div>
                <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.status.title")}</h1>
                <p className="text-muted-foreground mt-1">
                    {t("securityCenter.status.subtitle")}
                </p>
            </div>
            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <Server className="h-5 w-5" />
                        {t("securityCenter.status.health")}
                    </CardTitle>
                    <CardDescription>{t("securityCenter.status.comingSoon")}</CardDescription>
                </CardHeader>
                <CardContent>
                    <p className="text-sm text-muted-foreground">
                        {t("securityCenter.status.description")}
                    </p>
                </CardContent>
            </Card>
        </div>
    );
}
