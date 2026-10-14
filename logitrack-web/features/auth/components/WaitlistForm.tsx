"use client";

/**
 * The `/waitlist` card (UI3C, #143): the secondary path of the sign-in pages ("No account yet?"), in both languages,
 * with the primary button in the primary token and no flags in the country picker (DESIGN.md). It posts through
 * `useWaitlist` to the anonymous BFF form `POST /api/forms/waitlist`.
 *
 * UI3E: the waitlist is for transport companies interested in LogiTrack Pro (the sign-in card says so; staff,
 * customers and partners get their accounts from the company that invites them). The card's title is the page's h1,
 * the required labels carry the Signal Red star, and on a phone every control is a 44 px target.
 */
import Link from "next/link";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RequiredMark } from "@/components/ui/required-mark";
import { SearchSelect, type SearchSelectOption } from "@/components/ui/search-select";
import { HoneypotField } from "@/components/landing/HoneypotField";
import { useWaitlist } from "../hooks/useWaitlist";
import { AuthCardTitle } from "./AuthCardTitle";

/** Calling codes offered, Thailand first; the label is the country in the active language. */
const COUNTRIES = [
    ["+66", "TH"],
    ["+1", "US"],
    ["+65", "SG"],
    ["+60", "MY"],
    ["+84", "VN"],
    ["+62", "ID"],
    ["+63", "PH"],
    ["+856", "LA"],
    ["+855", "KH"],
    ["+95", "MM"],
] as const;

/** A label with the required star (DESIGN.md form style). */
function RequiredLabel({ htmlFor, children }: { htmlFor: string; children: React.ReactNode }) {
    return (
        <Label htmlFor={htmlFor}>
            <span>
                {children}
                <RequiredMark />
            </span>
        </Label>
    );
}

export default function WaitlistForm() {
    const { email, setEmail, fullName, setFullName, countryCode, setCountryCode, phone, setPhone, website, setWebsite, loading, submitted, handleSubmit, t } =
        useWaitlist();

    const countryOptions: SearchSelectOption[] = COUNTRIES.map(([value, code]) => ({
        value,
        label: `${t(`auth.waitlist.country.${code}`)} (${value})`,
    }));

    if (submitted) {
        return (
            <Card className="w-full max-w-md shadow-lg" data-testid="waitlist-card">
                <CardHeader className="gap-1">
                    <AuthCardTitle>{t("auth.waitlist.successTitle")}</AuthCardTitle>
                    <CardDescription>{t("auth.waitlist.successDesc")}</CardDescription>
                </CardHeader>
                <CardContent>
                    <Button asChild className="w-full max-md:h-11">
                        <Link href="/login">{t("auth.backToLogin")}</Link>
                    </Button>
                </CardContent>
            </Card>
        );
    }

    return (
        <Card className="w-full max-w-md shadow-lg" data-testid="waitlist-card">
            <CardHeader className="gap-1">
                <AuthCardTitle>{t("auth.register.title")}</AuthCardTitle>
                <CardDescription>{t("auth.waitlist.description")}</CardDescription>
            </CardHeader>
            <CardContent>
                <form onSubmit={handleSubmit} className="relative space-y-4">
                    <HoneypotField id="waitlist-page-website" value={website} onChange={setWebsite} />
                    <div className="space-y-2">
                        <RequiredLabel htmlFor="fullName">{t("auth.waitlist.fullName")}</RequiredLabel>
                        <Input
                            id="fullName"
                            type="text"
                            autoComplete="name"
                            placeholder={t("auth.waitlist.fullNamePlaceholder")}
                            value={fullName}
                            onChange={(e) => setFullName(e.target.value)}
                            required
                            className="max-md:h-11"
                        />
                    </div>
                    <div className="space-y-2">
                        <RequiredLabel htmlFor="email">{t("auth.email")}</RequiredLabel>
                        <Input
                            id="email"
                            type="email"
                            autoComplete="email"
                            placeholder="name@company.co.th"
                            value={email}
                            onChange={(e) => setEmail(e.target.value)}
                            required
                            className="max-md:h-11"
                        />
                    </div>
                    <div className="space-y-2">
                        <RequiredLabel htmlFor="phone">{t("auth.waitlist.phone")}</RequiredLabel>
                        <div className="flex gap-2">
                            <SearchSelect
                                options={countryOptions}
                                value={countryCode}
                                onValueChange={(v) => setCountryCode(v)}
                                aria-label={t("auth.waitlist.countryCode")}
                                searchPlaceholder={t("auth.waitlist.searchCountry")}
                                className="w-40 shrink-0 max-md:min-h-11"
                            />
                            <Input
                                id="phone"
                                type="tel"
                                autoComplete="tel-national"
                                placeholder="812345678"
                                value={phone}
                                onChange={(e) => setPhone(e.target.value)}
                                required
                                className="min-w-0 flex-1 max-md:h-11"
                            />
                        </div>
                    </div>
                    <Button type="submit" className="w-full max-md:h-11" disabled={loading}>
                        {loading ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
                        {t("auth.waitlist.submit")}
                    </Button>
                </form>
            </CardContent>
            <CardFooter className="justify-center">
                <Link
                    href="/login"
                    className="text-sm font-medium text-primary underline-offset-4 hover:underline max-md:inline-flex max-md:min-h-11 max-md:items-center"
                >
                    {t("auth.alreadyHaveAccount")}
                </Link>
            </CardFooter>
        </Card>
    );
}
