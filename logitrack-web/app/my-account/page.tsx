"use client";

import { useEffect } from "react";
import dynamic from "next/dynamic";
import { useRouter } from "next/navigation";

import { Button } from "@/components/ui/button";
import Navigation from "@/components/navigation";
import { ScopeLine } from "@/components/scope-line";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { useLanguage } from "@/context/language";
import { meDisplayName } from "@/features/auth/api/me";
import { useMe } from "@/features/auth/api/useMe";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import { initialOf } from "@/lib/initials";

// The interactive form loads separately so account details do not wait for the form library.
const AccountPasswordPanel = dynamic(() => import("@/features/auth/components/AccountPasswordPanel").then((module) => module.AccountPasswordPanel), {
    ssr: false,
    loading: AccountPasswordLoading,
});

function AccountPasswordLoading() {
    const { t } = useLanguage();
    return <p role="status" className="text-sm text-muted-foreground">{t("common.loading")}</p>;
}

export default function MyAccountPage() {
    const { t } = useLanguage();
    const router = useRouter();
    const { data: me, isPending, isError, isSuccess, refetch, isFetching } = useMe();
    useDocumentTitle(`${t("nav.myAccount")} · ${t("brand.name")}`);

    useEffect(() => {
        if (isSuccess && me === null) router.replace("/login");
    }, [isSuccess, me, router]);

    return (
        <div className="flex min-h-screen flex-col bg-background text-foreground">
            <Navigation />
            <main className="mx-auto w-full max-w-2xl flex-1 space-y-section p-gutter py-section">
                <h1 className="text-2xl font-semibold tracking-tight">{t("nav.myAccount")}</h1>
                {isPending ? <p role="status" className="text-sm text-muted-foreground">{t("common.loading")}</p> :
                    !me && isError ? <div className="space-y-3"><p role="alert" className="text-sm text-destructive">{t("auth.account.loadError")}</p><Button type="button" onClick={() => void refetch()} disabled={isFetching}>{t("pageState.retry")}</Button></div> :
                    !me ? <p role="status" className="text-sm text-muted-foreground">{t("auth.account.redirecting")}</p> : (
                        <>
                            <section aria-labelledby="account-profile-title" className="space-y-stack">
                                <h2 id="account-profile-title" className="text-base font-semibold">{t("auth.account.profileTitle")}</h2>
                                <div className="flex items-center gap-4">
                                    <Avatar className="size-12 shrink-0">
                                        <AvatarImage src={me.photoUrl ?? undefined} alt="" />
                                        <AvatarFallback className="text-base font-medium">{initialOf(meDisplayName(me))}</AvatarFallback>
                                    </Avatar>
                                    <ScopeLine className="min-w-0 flex-1 text-sm text-muted-foreground" compact />
                                </div>
                                <dl className="grid gap-4 text-sm sm:grid-cols-2">
                                    <div className="min-w-0 space-y-1">
                                        <dt className="font-medium">{t("auth.account.name")}</dt>
                                        <dd className="break-words text-muted-foreground">{me.displayName?.trim() ? meDisplayName(me) : t("auth.account.notSet")}</dd>
                                    </div>
                                    <div className="min-w-0 space-y-1">
                                        <dt className="font-medium">{t("auth.email")}</dt>
                                        <dd className="break-words text-muted-foreground">{me.email || t("auth.account.notSet")}</dd>
                                    </div>
                                </dl>
                            </section>
                            <AccountPasswordPanel />
                        </>
                    )}
            </main>
        </div>
    );
}
