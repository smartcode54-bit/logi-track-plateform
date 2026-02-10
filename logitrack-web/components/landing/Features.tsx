import React from "react";
import { useLanguage } from "@/context/language";

export function Features() {
    const { t } = useLanguage();

    return (
        <section className="py-20 lg:px-40 bg-slate-100 dark:bg-black/20">
            <div className="mx-auto max-w-[1280px] px-4">
                <div className="flex flex-col gap-12">
                    <div className="flex flex-col gap-4">
                        <h2 className="text-slate-900 dark:text-white text-3xl md:text-4xl font-black leading-tight max-w-[720px]">
                            {t("landing.features.title")}
                        </h2>
                        <p className="text-slate-600 dark:text-slate-400 text-base font-normal leading-normal max-w-[720px]">
                            {t("landing.features.description")}
                        </p>
                    </div>
                    <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                        <div className="flex flex-1 gap-6 rounded-xl border border-slate-200 dark:border-[#3b4754] bg-white dark:bg-[#1c2127] p-8 flex-col hover:border-primary dark:hover:border-primary transition-colors group">
                            <div className="text-primary bg-primary/10 w-12 h-12 flex items-center justify-center rounded-lg group-hover:scale-110 transition-transform">
                                <span className="material-symbols-outlined text-[32px]">
                                    schedule
                                </span>
                            </div>
                            <div className="flex flex-col gap-2">
                                <h3 className="text-slate-900 dark:text-white text-xl font-bold">
                                    {t("landing.feature.tracking.title")}
                                </h3>
                                <p className="text-slate-500 dark:text-[#9dabb9] text-sm leading-relaxed">
                                    {t("landing.feature.tracking.desc")}
                                </p>
                            </div>
                        </div>
                        <div className="flex flex-1 gap-6 rounded-xl border border-slate-200 dark:border-[#3b4754] bg-white dark:bg-[#1c2127] p-8 flex-col hover:border-primary dark:hover:border-primary transition-colors group">
                            <div className="text-primary bg-primary/10 w-12 h-12 flex items-center justify-center rounded-lg group-hover:scale-110 transition-transform">
                                <span className="material-symbols-outlined text-[32px]">
                                    dashboard_customize
                                </span>
                            </div>
                            <div className="flex flex-col gap-2">
                                <h3 className="text-slate-900 dark:text-white text-xl font-bold">
                                    {t("landing.feature.admin.title")}
                                </h3>
                                <p className="text-slate-500 dark:text-[#9dabb9] text-sm leading-relaxed">
                                    {t("landing.feature.admin.desc")}
                                </p>
                            </div>
                        </div>
                        <div className="flex flex-1 gap-6 rounded-xl border border-slate-200 dark:border-[#3b4754] bg-white dark:bg-[#1c2127] p-8 flex-col hover:border-primary dark:hover:border-primary transition-colors group">
                            <div className="text-primary bg-primary/10 w-12 h-12 flex items-center justify-center rounded-lg group-hover:scale-110 transition-transform">
                                <span className="material-symbols-outlined text-[32px]">
                                    translate
                                </span>
                            </div>
                            <div className="flex flex-col gap-2">
                                <h3 className="text-slate-900 dark:text-white text-xl font-bold">
                                    {t("landing.feature.language.title")}
                                </h3>
                                <p className="text-slate-500 dark:text-[#9dabb9] text-sm leading-relaxed">
                                    {t("landing.feature.language.desc")}
                                </p>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </section>
    );
}
