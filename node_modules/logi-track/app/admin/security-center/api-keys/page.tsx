"use client";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Key } from "lucide-react";
import { useLanguage } from "@/context/language";

export default function ApiKeysPage() {
    const { t } = useLanguage();
    return (
        <div className="p-6 space-y-6">
            <div>
                <h1 className="text-3xl font-bold tracking-tight">{t("securityCenter.apiKeys.title")}</h1>
                <p className="text-muted-foreground mt-1">
                    {t("securityCenter.apiKeys.subtitle")}
                </p>
            </div>
            <Card>
                <CardHeader>
                    <CardTitle className="flex items-center gap-2">
                        <Key className="h-5 w-5" />
                        {t("securityCenter.apiKeys.management")}
                    </CardTitle>
                    <CardDescription>{t("securityCenter.apiKeys.comingSoon")}</CardDescription>
                </CardHeader>
                <CardContent>
                    <p className="text-sm text-muted-foreground">
                        {t("securityCenter.apiKeys.description")}
                    </p>
                </CardContent>
            </Card>
        </div>
    );
}
