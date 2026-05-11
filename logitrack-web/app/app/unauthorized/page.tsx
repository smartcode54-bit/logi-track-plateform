"use client"

import { useSearchParams, useRouter } from "next/navigation"
import { ShieldX, ArrowLeft, Home, Lock } from "lucide-react"
import { Button } from "@/components/ui/button"
import { useLanguage } from "@/context/language"
import { useAuth } from "@/context/auth"
import { getDefaultRouteForRole, getRole } from "@/lib/permissions"
import { Suspense } from "react"

function UnauthorizedContent() {
    const searchParams = useSearchParams()
    const router = useRouter()
    const { t } = useLanguage()
    const authContext = useAuth()

    const attemptedPath = searchParams.get("from") ?? ""
    const role = getRole(authContext?.customClaims ?? null)
    const homeRoute = getDefaultRouteForRole(authContext?.customClaims ?? null)

    return (
        <div className="flex min-h-[70vh] flex-col items-center justify-center gap-8 px-4 text-center">
            <div className="relative">
                <div className="flex items-center justify-center w-28 h-28 rounded-full bg-destructive/10 ring-1 ring-destructive/20">
                    <Lock className="h-12 w-12 text-destructive/70" />
                </div>
                <div className="absolute -top-2 -right-2 flex items-center justify-center w-9 h-9 rounded-full bg-destructive text-white text-xs font-bold shadow-md">
                    403
                </div>
            </div>

            <div className="space-y-3 max-w-md">
                <h1 className="text-2xl font-bold text-foreground">
                    {t("unauthorized.title")}
                </h1>
                <p className="text-muted-foreground">
                    {t("unauthorized.description")}
                </p>
                {attemptedPath && (
                    <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-md bg-muted text-sm text-muted-foreground font-mono border border-border">
                        <ShieldX className="h-3.5 w-3.5 shrink-0" />
                        <span className="truncate max-w-[280px]">{attemptedPath}</span>
                    </div>
                )}
            </div>

            <div className="flex items-center gap-3 flex-wrap justify-center">
                <Button
                    variant="outline"
                    onClick={() => router.back()}
                    className="gap-2"
                >
                    <ArrowLeft className="h-4 w-4" />
                    {t("unauthorized.goBack")}
                </Button>
                <Button
                    onClick={() => router.replace(homeRoute)}
                    className="gap-2"
                >
                    <Home className="h-4 w-4" />
                    {t("unauthorized.goHome")}
                </Button>
            </div>

            <p className="text-xs text-muted-foreground/60">
                {t("unauthorized.roleHint", { role: role.replace(/_/g, " ") })}
            </p>
        </div>
    )
}

export default function UnauthorizedPage() {
    return (
        <Suspense fallback={null}>
            <UnauthorizedContent />
        </Suspense>
    )
}
