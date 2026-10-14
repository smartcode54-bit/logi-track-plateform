"use client";

import Link from "next/link";
import { Loader2 } from "lucide-react";

import { Card, CardContent, CardDescription, CardFooter, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { useLogin } from "../hooks/useLogin";
import { SignInPanel } from "./SignInPanel";

/**
 * The login page card: the shared sign-in panel; the page leaves once `['me']` is signed in. A solid card on the
 * paper token (UI3): no blur, no translucency, `shadow-lg` at most, so it reads the same over any background. The landing
 * page `/` shows the same card (UI3C) with `className="shadow-sm"`, as it sits on plain paper. The title is a real
 * heading: the page's h1 on `/login` (the default), an h2 under the landing's own h1.
 */
export default function LoginForm({ className, headingLevel: Heading = "h1" }: { className?: string; headingLevel?: "h1" | "h2" }) {
    const { t, notice, signedIn } = useLogin();

    return (
        <Card className={cn("w-full max-w-md shadow-lg", className)} data-testid="sign-in-card">
            <CardHeader className="gap-1">
                <Heading data-slot="card-title" className="text-2xl leading-none font-semibold tracking-tight">
                    {t("auth.login.title")}
                </Heading>
                <CardDescription>{t("auth.login.subtitle")}</CardDescription>
            </CardHeader>
            <CardContent>
                {signedIn ? (
                    <div className="flex items-center justify-center gap-2 py-8 text-sm text-muted-foreground" role="status">
                        <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
                        {t("auth.signingIn")}
                    </div>
                ) : (
                    <SignInPanel notice={notice} />
                )}
            </CardContent>
            <CardFooter className="justify-center">
                <p className="text-center text-sm text-muted-foreground">
                    {t("auth.dontHaveAccount")}{" "}
                    <Link href="/waitlist" className="font-medium text-primary underline-offset-4 hover:underline">
                        {t("auth.requestAccess")}
                    </Link>
                </p>
            </CardFooter>
        </Card>
    );
}
