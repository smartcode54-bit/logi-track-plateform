"use client";

import Link from "next/link";
import { Loader2 } from "lucide-react";

import { Card, CardContent, CardDescription, CardFooter, CardHeader } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { useLogin } from "../hooks/useLogin";
import { AuthCardTitle } from "./AuthCardTitle";
import { SignInPanel } from "./SignInPanel";

/**
 * The login page card: the shared sign-in panel; the page leaves once `['me']` is signed in. A solid card on the
 * paper token (UI3): no blur, no translucency, `shadow-lg` at most, so it reads the same over any background. The landing
 * page `/` shows the same card (UI3C) with `className="shadow-sm"`, as it sits on plain paper. The title is a real
 * heading: the page's h1 on `/login` (the default), an h2 under the landing's own h1.
 *
 * The footer (UI3E) tells a visitor without an account to ask the transport company or administrator who invited
 * them, and offers the LogiTrack Pro waitlist separately, to transport companies.
 */
export default function LoginForm({ className, headingLevel = "h1" }: { className?: string; headingLevel?: "h1" | "h2" }) {
    const { t, notice, signedIn } = useLogin();

    return (
        <Card className={cn("w-full max-w-md shadow-lg", className)} data-testid="sign-in-card">
            <CardHeader className="gap-1">
                <AuthCardTitle as={headingLevel}>{t("auth.login.title")}</AuthCardTitle>
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
            {/* UI3E: who to ask for an account. Staff, customers and partners get theirs from the transport company that
                invites them; GORATECH's waitlist is only for transport companies that want LogiTrack Pro, so it is its
                own, labelled line. */}
            <CardFooter className="flex-col gap-2 text-center text-sm text-muted-foreground">
                <p data-testid="sign-in-no-account">{t("auth.login.noAccount")}</p>
                <p>
                    {t("auth.login.companiesLead")}{" "}
                    <Link
                        href="/waitlist"
                        className="font-medium text-primary underline-offset-4 hover:underline max-md:inline-flex max-md:min-h-11 max-md:items-center"
                    >
                        {t("auth.login.companiesWaitlist")}
                    </Link>
                </p>
            </CardFooter>
        </Card>
    );
}
