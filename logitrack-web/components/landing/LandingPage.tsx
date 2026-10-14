"use client";

/**
 * The public front door `/` (UI3C, #143). A signed-out visitor gets the landing: what LogiTrack Pro is, with the
 * sign-in form one step away (the same card as `/login`) and the waitlist as a secondary link. A signed-in visitor
 * goes to their console: the role's home (`homeRouteOf`, the rule of the `proxy.ts` gate and the shell's Home crumb),
 * with an "Open console" link while the navigation runs.
 *
 * `sessionHint` is true when the request carried the `lt_at` cookie (app/page.tsx): until `['me']` answers, such a
 * visitor sees the "opening" state instead of a sign-in form they do not need. Without it the landing shows at once.
 */
import { Suspense, useEffect } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Loader2 } from "lucide-react";

import Navigation from "@/components/navigation";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/context/auth";
import { useLanguage } from "@/context/language";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import LoginForm from "@/features/auth/components/LoginForm";
import { APP_PATH } from "@/lib/sessionEnd";
import { CTA } from "./CTA";
import { Features } from "./Features";
import { Footer } from "./Footer";

export function LandingPage({ sessionHint = false }: { sessionHint?: boolean }) {
    const { t } = useLanguage();
    const auth = useAuth();
    const router = useRouter();
    const loading = !auth || auth.loading;
    const me = loading ? null : auth.me;
    const home = me ? homeRouteOf(me) : null;

    useEffect(() => {
        if (home) router.replace(home);
    }, [home, router]);

    if (home || (sessionHint && loading)) {
        return (
            <main className="flex min-h-screen flex-col items-center justify-center gap-4 bg-background p-6 text-center" data-testid="opening-console">
                <div role="status" className="flex flex-col items-center gap-4">
                    <Loader2 className="size-8 animate-spin text-primary" aria-hidden />
                    <p className="text-muted-foreground">{t("landing.openingConsole")}</p>
                </div>
                <Button asChild variant="outline">
                    <Link href={home ?? APP_PATH} prefetch={false}>
                        {t("landing.openConsole")}
                    </Link>
                </Button>
            </main>
        );
    }

    return (
        <div className="flex min-h-screen flex-col bg-background text-foreground">
            <Navigation />
            <main className="flex-1">
                <section aria-labelledby="landing-title" className="mx-auto grid max-w-6xl items-center gap-10 px-4 py-12 sm:px-6 md:py-20 lg:grid-cols-[minmax(0,1fr)_minmax(0,28rem)] lg:gap-16 lg:px-8">
                    <div className="flex min-w-0 flex-col gap-6">
                        <h1
                            id="landing-title"
                            className="text-4xl leading-[1.1] font-bold tracking-tight text-balance md:text-5xl lg:text-6xl [:lang(th)_&]:leading-[1.25]"
                        >
                            {t("landing.hero.title")}
                        </h1>
                        <p className="max-w-prose text-lg leading-relaxed text-muted-foreground">{t("landing.hero.description")}</p>
                    </div>
                    <div className="flex min-w-0 justify-center lg:justify-end">
                        {/* The sign-in card of /login (sign-in primary, waitlist a text link under it); useSearchParams
                            inside needs a Suspense boundary. */}
                        <Suspense fallback={null}>
                            <LoginForm className="shadow-sm" />
                        </Suspense>
                    </div>
                </section>
                <Features />
                <CTA />
            </main>
            <Footer />
        </div>
    );
}
