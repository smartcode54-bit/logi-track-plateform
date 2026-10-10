"use client";

import Link from "next/link";
import { Loader2 } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useLogin } from "../hooks/useLogin";
import { SignInPanel } from "./SignInPanel";

/** The login page card: the shared sign-in panel; the page leaves once `['me']` is signed in. */
export default function LoginForm() {
    const { t, notice, signedIn } = useLogin();

    return (
        <Card className="w-full max-w-md shadow-xl border-border/60 bg-background/95 backdrop-blur-sm">
            <CardHeader className="space-y-1">
                <CardTitle className="text-2xl font-bold">{t("auth.login.title")}</CardTitle>
                <CardDescription>{t("auth.login.subtitle")}</CardDescription>
            </CardHeader>
            <CardContent>
                {signedIn ? (
                    <div className="flex items-center justify-center gap-2 py-8 text-sm text-muted-foreground" role="status">
                        <Loader2 className="h-4 w-4 animate-spin" />
                        {t("auth.signingIn")}
                    </div>
                ) : (
                    <SignInPanel notice={notice} />
                )}
            </CardContent>
            <div className="p-6 pt-0 text-center">
                <p className="text-sm text-muted-foreground">
                    {t("auth.dontHaveAccount")}{" "}
                    <Link href="/waitlist" className="font-semibold text-foreground hover:underline">
                        {t("nav.register")}
                    </Link>
                </p>
            </div>
        </Card>
    );
}
