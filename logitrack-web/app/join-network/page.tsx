"use client";

/**
 * `/join-network` (UI3C, #143): carriers apply to work with GORATECH as partners (รถร่วม). The page says only what is
 * true of the operation (PRODUCT.md): no promised volume, payment terms, AI routing or foreign licence rules. The form
 * posts to the anonymous BFF form `POST /api/forms/partner-interest` (R44, R77; lib/publicForms.ts) with the hidden
 * honeypot field `website`. `contractNumber` is the contact phone number (the field's name in Go since the Firestore
 * form; its placeholder was a phone number).
 *
 * UI3E: a sent application replaces the form with a success card that says what happens next, using only what the form
 * collected (the team contacts the applicant at the email address, and the phone number when one was given); no
 * timeline or outcome is promised. Focus moves to the card's heading so the change is announced. On a phone every
 * control is a 44 px target.
 */
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Footer } from "@/components/landing/Footer";
import { HoneypotField } from "@/components/landing/HoneypotField";
import Navigation from "@/components/navigation";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RequiredMark } from "@/components/ui/required-mark";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLanguage } from "@/context/language";
import { toastSaveError } from "@/lib/formServerErrors";
import { submitPartnerInterest } from "@/lib/publicForms";

const HOW = ["assign", "evidence", "view"] as const;
const FLEET_SIZES = ["1-5", "6-20", "21-50", "50+"] as const;
/** The task vehicle classes a partner fleet is made of (validate/taskSchema.ts), and "several types". */
const VEHICLE_TYPES = ["4W", "4WJ", "6WH", "10WH", "mixed"] as const;

const EMPTY = {
    companyName: "",
    contractNumber: "",
    contactName: "",
    email: "",
    serviceArea: "",
    fleetSize: "",
    vehicleType: "",
};
type Field = keyof typeof EMPTY;
type RequiredKey = "companyName" | "contactName" | "email";
const REQUIRED: readonly RequiredKey[] = ["companyName", "contactName", "email"];
const EMAIL_RX = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** The message key of each required field that fails, in form order; Go validates the post again. */
function validate(form: typeof EMPTY): Partial<Record<RequiredKey, string>> {
    const errors: Partial<Record<RequiredKey, string>> = {};
    if (!form.companyName.trim()) errors.companyName = "landing.join.form.companyNameRequired";
    if (!form.contactName.trim()) errors.contactName = "landing.join.form.contactNameRequired";
    if (!form.email.trim()) errors.email = "landing.join.form.emailRequired";
    else if (!EMAIL_RX.test(form.email.trim())) errors.email = "landing.join.form.emailInvalid";
    return errors;
}

/**
 * A required text field (DESIGN.md form style): the label with a Signal Red star (not read out), the input, and its
 * error under it, tied to the input by `aria-invalid` and `aria-describedby`.
 */
function RequiredField({
    name,
    label,
    error,
    ...input
}: { name: RequiredKey; label: string; error?: string } & Omit<React.ComponentProps<typeof Input>, "id" | "name">) {
    const id = `join-${name}`;
    const errorId = `${id}-error`;
    return (
        <div className="flex flex-col gap-2">
            <Label htmlFor={id}>
                <span>
                    {label}
                    <RequiredMark />
                </span>
            </Label>
            <Input
                id={id}
                name={name}
                aria-required
                aria-invalid={error ? true : undefined}
                aria-describedby={error ? errorId : undefined}
                className="max-md:h-11"
                {...input}
            />
            {error ? (
                <p id={errorId} className="text-sm text-destructive">
                    {error}
                </p>
            ) : null}
        </div>
    );
}

/**
 * What the applicant sees once the application is sent (UI3E): that GORATECH has it, and that the team will contact
 * them at the address (and the phone number, when given) they entered, shown back so a typo is visible. Nothing about
 * when or with what result: the page has no evidence for either.
 */
function JoinSuccess({
    application,
    headingRef,
    onAgain,
}: {
    application: typeof EMPTY;
    headingRef: React.RefObject<HTMLHeadingElement | null>;
    onAgain: () => void;
}) {
    const { t } = useLanguage();
    const phone = application.contractNumber;

    return (
        <Card data-testid="join-success">
            <CardHeader className="gap-1">
                <h2 ref={headingRef} id="join-form-title" tabIndex={-1} data-slot="card-title" className="scroll-mt-20 text-xl leading-tight font-semibold tracking-tight outline-none">
                    {t("landing.join.success.title")}
                </h2>
                <CardDescription>
                    {t("landing.join.success.description", { name: application.contactName.trim(), company: application.companyName.trim() })}
                </CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-6">
                <section aria-labelledby="join-next-title" className="flex flex-col gap-3">
                    <h3 id="join-next-title" className="text-base font-semibold">
                        {t("landing.join.success.nextTitle")}
                    </h3>
                    <p className="max-w-prose text-sm leading-relaxed text-muted-foreground">
                        {t(phone ? "landing.join.success.nextEmailPhone" : "landing.join.success.nextEmail")}
                    </p>
                    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2 text-sm">
                        <dt className="text-muted-foreground">{t("landing.join.form.email")}</dt>
                        <dd className="font-medium break-all">{application.email}</dd>
                        {phone ? (
                            <>
                                <dt className="text-muted-foreground">{t("landing.join.form.phone")}</dt>
                                <dd className="font-medium tabular-nums">{phone}</dd>
                            </>
                        ) : null}
                    </dl>
                </section>
                <div className="flex flex-col gap-3 sm:flex-row">
                    <Button type="button" variant="outline" onClick={onAgain} className="max-md:h-11">
                        {t("landing.join.success.again")}
                    </Button>
                    <Button asChild variant="ghost" className="max-md:h-11">
                        <Link href="/">{t("landing.join.success.home")}</Link>
                    </Button>
                </div>
            </CardContent>
        </Card>
    );
}

export default function JoinNetworkPage() {
    const { t } = useLanguage();
    const [loading, setLoading] = useState(false);
    const [website, setWebsite] = useState("");
    const [form, setForm] = useState(EMPTY);
    const [errors, setErrors] = useState<Partial<Record<RequiredKey, string>>>({});
    /** The application as sent, for the success card; null while the form shows. */
    const [sent, setSent] = useState<typeof EMPTY | null>(null);
    const formRef = useRef<HTMLFormElement>(null);
    const successRef = useRef<HTMLHeadingElement>(null);

    // The success card replaces the form: its heading takes the focus, so the change is announced and Tab starts there.
    useEffect(() => {
        if (sent) successRef.current?.focus();
    }, [sent]);

    const again = () => {
        setSent(null);
        // The form is back on the next render; its first field takes the focus.
        requestAnimationFrame(() => formRef.current?.querySelector<HTMLInputElement>("#join-companyName")?.focus());
    };

    const set = (key: Field, value: string) => {
        setForm((prev) => ({ ...prev, [key]: value }));
        // A field's error goes once it is edited; the next submit checks again.
        setErrors((prev) => (key in prev ? { ...prev, [key]: undefined } : prev));
    };

    const submit = async (e: React.FormEvent) => {
        e.preventDefault();
        const found = validate(form);
        setErrors(found);
        const first = REQUIRED.find((key) => found[key]);
        if (first) {
            toast.error(t("landing.join.form.required"));
            formRef.current?.querySelector<HTMLInputElement>(`#join-${first}`)?.focus();
            return;
        }
        setLoading(true);
        try {
            const application = { ...form, email: form.email.trim(), contractNumber: form.contractNumber.trim() };
            await submitPartnerInterest({ ...application, website });
            setSent(application);
            setForm(EMPTY);
            setErrors({});
        } catch (error) {
            toastSaveError(error, t);
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="flex min-h-screen flex-col bg-background text-foreground">
            <Navigation />
            <main className="flex-1">
                <section aria-labelledby="join-title" className="mx-auto flex max-w-6xl flex-col gap-6 px-4 py-12 sm:px-6 md:py-20 lg:px-8">
                    <h1
                        id="join-title"
                        className="max-w-4xl text-4xl leading-[1.1] font-bold tracking-tight text-balance md:text-5xl lg:text-6xl [:lang(th)_&]:leading-[1.25]"
                    >
                        {t("landing.join.title")}
                    </h1>
                    <p className="max-w-prose text-lg leading-relaxed text-muted-foreground">{t("landing.join.description")}</p>
                    <div>
                        <Button asChild variant="outline" className="max-md:h-11">
                            <Link href="#apply">{t("landing.join.apply")}</Link>
                        </Button>
                    </div>
                </section>

                <section aria-labelledby="join-how-title" className="border-t border-border">
                    <div className="mx-auto max-w-6xl px-4 py-16 sm:px-6 lg:px-8">
                        <h2 id="join-how-title" className="text-2xl font-semibold tracking-tight md:text-3xl">
                            {t("landing.join.how.title")}
                        </h2>
                        <dl className="mt-10 grid gap-x-12 gap-y-8 md:grid-cols-3">
                            {HOW.map((point) => (
                                <div key={point} className="border-t border-border pt-4">
                                    <dt className="text-base font-semibold">{t(`landing.join.how.${point}.title`)}</dt>
                                    <dd className="mt-2 text-sm leading-relaxed text-muted-foreground">{t(`landing.join.how.${point}.desc`)}</dd>
                                </div>
                            ))}
                        </dl>
                    </div>
                </section>

                <section id="apply" aria-labelledby="join-form-title" className="scroll-mt-14 border-t border-border">
                    <div className="mx-auto max-w-3xl px-4 py-16 sm:px-6 lg:px-8">
                        {sent ? (
                            <JoinSuccess application={sent} headingRef={successRef} onAgain={again} />
                        ) : (
                            <Card>
                                <CardHeader className="gap-1">
                                    <h2 id="join-form-title" data-slot="card-title" className="text-xl leading-tight font-semibold tracking-tight">
                                        {t("landing.join.form.title")}
                                    </h2>
                                    <CardDescription>{t("landing.join.form.description")}</CardDescription>
                                </CardHeader>
                                <CardContent>
                                    <form ref={formRef} onSubmit={submit} noValidate className="relative flex flex-col gap-4">
                                        <HoneypotField id="partner-website" value={website} onChange={setWebsite} />
                                        <div className="grid gap-4 md:grid-cols-2">
                                            <RequiredField
                                                name="companyName"
                                                label={t("landing.join.form.companyName")}
                                                error={errors.companyName ? t(errors.companyName) : undefined}
                                                autoComplete="organization"
                                                value={form.companyName}
                                                onChange={(e) => set("companyName", e.target.value)}
                                            />
                                            <RequiredField
                                                name="contactName"
                                                label={t("landing.join.form.contactName")}
                                                error={errors.contactName ? t(errors.contactName) : undefined}
                                                autoComplete="name"
                                                value={form.contactName}
                                                onChange={(e) => set("contactName", e.target.value)}
                                            />
                                            <RequiredField
                                                name="email"
                                                label={t("landing.join.form.email")}
                                                error={errors.email ? t(errors.email) : undefined}
                                                type="email"
                                                autoComplete="email"
                                                placeholder="name@company.co.th"
                                                value={form.email}
                                                onChange={(e) => set("email", e.target.value)}
                                            />
                                            <div className="flex flex-col gap-2">
                                                <Label htmlFor="join-contractNumber">{t("landing.join.form.phone")}</Label>
                                                <Input
                                                    id="join-contractNumber"
                                                    type="tel"
                                                    autoComplete="tel"
                                                    placeholder="0812345678"
                                                    value={form.contractNumber}
                                                    onChange={(e) => set("contractNumber", e.target.value)}
                                                    className="max-md:h-11"
                                                />
                                            </div>
                                        </div>
                                        <div className="flex flex-col gap-2">
                                            <Label htmlFor="join-serviceArea">{t("landing.join.form.serviceArea")}</Label>
                                            <Input
                                                id="join-serviceArea"
                                                placeholder={t("landing.join.form.serviceAreaPlaceholder")}
                                                value={form.serviceArea}
                                                onChange={(e) => set("serviceArea", e.target.value)}
                                                className="max-md:h-11"
                                            />
                                        </div>
                                        <div className="grid gap-4 md:grid-cols-2">
                                            <div className="flex flex-col gap-2">
                                                <Label htmlFor="join-fleetSize">{t("landing.join.form.fleetSize")}</Label>
                                                <Select value={form.fleetSize} onValueChange={(v) => set("fleetSize", v)}>
                                                    <SelectTrigger id="join-fleetSize" className="w-full max-md:h-11">
                                                        <SelectValue placeholder={t("landing.join.form.fleetSizePlaceholder")} />
                                                    </SelectTrigger>
                                                    <SelectContent>
                                                        {FLEET_SIZES.map((size) => (
                                                            <SelectItem key={size} value={size}>
                                                                {t(`landing.join.form.fleet.${size}`)}
                                                            </SelectItem>
                                                        ))}
                                                    </SelectContent>
                                                </Select>
                                            </div>
                                            <div className="flex flex-col gap-2">
                                                <Label htmlFor="join-vehicleType">{t("landing.join.form.vehicleType")}</Label>
                                                <Select value={form.vehicleType} onValueChange={(v) => set("vehicleType", v)}>
                                                    <SelectTrigger id="join-vehicleType" className="w-full max-md:h-11">
                                                        <SelectValue placeholder={t("landing.join.form.vehicleTypePlaceholder")} />
                                                    </SelectTrigger>
                                                    <SelectContent>
                                                        {VEHICLE_TYPES.map((type) => (
                                                            <SelectItem key={type} value={type}>
                                                                {t(`landing.join.form.vehicle.${type}`)}
                                                            </SelectItem>
                                                        ))}
                                                    </SelectContent>
                                                </Select>
                                            </div>
                                        </div>
                                        <Button type="submit" className="w-full sm:w-auto sm:self-end max-md:h-11" disabled={loading}>
                                            {loading ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
                                            {loading ? t("landing.join.form.submitting") : t("landing.join.form.submit")}
                                        </Button>
                                    </form>
                                </CardContent>
                            </Card>
                        )}
                    </div>
                </section>
            </main>
            <Footer />
        </div>
    );
}
