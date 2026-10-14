"use client";

/**
 * `/about` (UI3E): the origin story of LogiTrack Pro, on the public-page pattern of DESIGN.md (the landing and
 * `/join-network`): one Display h1, sections on paper separated by hairlines, each with a Headline, the points as a
 * list under hairlines. The stock photographs (one of them shown as "the platform") and the claims the product has no
 * evidence for are gone (PRODUCT.md, Evidence on Hand: built in-house from January 2026 instead of AppSheet). The page
 * ends with the shared footer, which also says where to go next.
 */
import { Footer } from "@/components/landing/Footer";
import Navigation from "@/components/navigation";
import { useLanguage } from "@/context/language";

const POINTS = ["precision", "security", "scale"] as const;

export default function AboutPage() {
    const { t } = useLanguage();

    return (
        <div className="flex min-h-screen flex-col bg-background text-foreground">
            <Navigation />
            <main className="flex-1">
                <section aria-labelledby="about-title" className="mx-auto flex max-w-6xl flex-col gap-6 px-4 py-12 sm:px-6 md:py-20 lg:px-8">
                    <h1
                        id="about-title"
                        className="max-w-4xl text-4xl leading-[1.1] font-bold tracking-tight text-balance md:text-5xl lg:text-6xl [:lang(th)_&]:leading-[1.25]"
                    >
                        {t("about.hero.title")}
                    </h1>
                    <p className="max-w-prose text-lg leading-relaxed text-muted-foreground">{t("about.hero.quote")}</p>
                </section>

                <section aria-labelledby="about-story-title" className="border-t border-border">
                    <div className="mx-auto flex max-w-6xl flex-col gap-4 px-4 py-16 sm:px-6 lg:px-8">
                        <h2 id="about-story-title" className="text-2xl font-semibold tracking-tight text-balance md:text-3xl">
                            {t("about.story.date")}
                        </h2>
                        <p className="max-w-prose leading-relaxed text-muted-foreground">{t("about.story.p1")}</p>
                        <p className="max-w-prose leading-relaxed text-muted-foreground">{t("about.story.answer")}</p>
                    </div>
                </section>

                <section aria-labelledby="about-why-title" className="border-t border-border">
                    <div className="mx-auto max-w-6xl px-4 py-16 sm:px-6 lg:px-8">
                        <div className="flex max-w-3xl flex-col gap-3">
                            <h2 id="about-why-title" className="text-2xl font-semibold tracking-tight text-balance md:text-3xl">
                                {t("about.why.title")}
                            </h2>
                            <p className="max-w-prose leading-relaxed text-muted-foreground">{t("about.why.origin")}</p>
                        </div>
                        <dl className="mt-10 grid gap-x-12 gap-y-8 md:grid-cols-3">
                            {POINTS.map((point) => (
                                <div key={point} className="border-t border-border pt-4">
                                    <dt className="text-base font-semibold">{t(`about.why.${point}.title`)}</dt>
                                    <dd className="mt-2 text-sm leading-relaxed text-muted-foreground">{t(`about.why.${point}.desc`)}</dd>
                                </div>
                            ))}
                        </dl>
                    </div>
                </section>

                <section className="border-t border-border">
                    <div className="mx-auto max-w-6xl px-4 py-16 sm:px-6 lg:px-8">
                        <p className="max-w-prose text-lg leading-relaxed">{t("about.closing")}</p>
                    </div>
                </section>
            </main>
            <Footer />
        </div>
    );
}
