import { useLanguage } from "@/context/language";

/**
 * What LogiTrack Pro does (UI3C): the four positioning points of PRODUCT.md, as a plain list under hairlines rather
 * than icon cards. The navigation's "Features" link lands here (`/#solutions`).
 */
const POINTS = ["endToEnd", "evidence", "thai", "scoped"] as const;

export function Features() {
    const { t } = useLanguage();

    return (
        <section id="solutions" aria-labelledby="solutions-title" className="scroll-mt-14 border-t border-border">
            <div className="mx-auto max-w-6xl px-4 py-16 sm:px-6 md:py-20 lg:px-8">
                <div className="flex max-w-3xl flex-col gap-3">
                    <h2 id="solutions-title" className="text-2xl font-semibold tracking-tight text-balance md:text-3xl">
                        {t("landing.features.title")}
                    </h2>
                    <p className="max-w-prose leading-relaxed text-muted-foreground">{t("landing.features.description")}</p>
                </div>
                <dl className="mt-10 grid gap-x-12 gap-y-8 sm:grid-cols-2">
                    {POINTS.map((point) => (
                        <div key={point} className="border-t border-border pt-4">
                            <dt className="text-base font-semibold">{t(`landing.feature.${point}.title`)}</dt>
                            <dd className="mt-2 max-w-prose text-sm leading-relaxed text-muted-foreground">{t(`landing.feature.${point}.desc`)}</dd>
                        </div>
                    ))}
                </dl>
            </div>
        </section>
    );
}
