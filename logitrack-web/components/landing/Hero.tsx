"use client";

import React, { useState } from "react";
import ContinueWithGoogleButton from "@/components/continue-with-google-button";
import { LoginModal } from "./LoginModal";
import { WaitlistModal } from "./WaitlistModal";
import { useLanguage } from "@/context/language";

export function Hero() {
    const { t } = useLanguage();
    const [loginOpen, setLoginOpen] = useState(false);
    const [waitlistOpen, setWaitlistOpen] = useState(false);

    return (
        <section className="flex-grow flex items-center justify-center py-12 md:py-20 lg:px-40">
            <div className="mx-auto max-w-[1280px] w-full px-4">
                <div className="grid grid-cols-1 lg:grid-cols-2 gap-12 items-center">
                    <div className="flex flex-col gap-6 lg:gap-8">
                        <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-primary/10 text-primary text-xs font-bold uppercase tracking-widest w-fit">
                            <span className="material-symbols-outlined text-sm">public</span>
                            {t("landing.hero.tagline")}
                        </div>
                        <div>
                            <h1 className="text-slate-900 dark:text-white text-4xl md:text-6xl font-black leading-[1.1] tracking-tight">
                                {t("landing.hero.title")}
                            </h1>
                        </div>
                        <p className="text-slate-600 dark:text-slate-400 text-lg md:text-xl leading-relaxed max-w-xl">
                            {t("landing.hero.description")}
                        </p>
                        <div className="flex flex-wrap gap-6 mt-4">
                            <div className="flex items-center gap-2 px-4 py-2 bg-white dark:bg-[#1c2127] border border-slate-200 dark:border-[#3b4754] rounded-lg shadow-sm">
                                <span className="material-symbols-outlined text-primary">
                                    speed
                                </span>
                                <div className="flex flex-col">
                                    <span className="text-xs font-bold uppercase tracking-wider text-slate-400">
                                        {t("landing.hero.stat.status")}
                                    </span>
                                    <span className="text-sm font-semibold">{t("landing.hero.stat.realtime")}</span>
                                </div>
                            </div>
                            <div className="flex items-center gap-2 px-4 py-2 bg-white dark:bg-[#1c2127] border border-slate-200 dark:border-[#3b4754] rounded-lg shadow-sm">
                                <span className="material-symbols-outlined text-primary">
                                    security
                                </span>
                                <div className="flex flex-col">
                                    <span className="text-xs font-bold uppercase tracking-wider text-slate-400">
                                        {t("landing.hero.stat.security")}
                                    </span>
                                    <span className="text-sm font-semibold">{t("landing.hero.stat.enterprise")}</span>
                                </div>
                            </div>
                        </div>
                    </div>
                    <div className="relative">
                        <div className="absolute -inset-4 bg-primary/10 blur-3xl rounded-full"></div>
                        <div className="relative bg-white dark:bg-[#1c2127] border border-slate-200 dark:border-[#3b4754] rounded-2xl p-8 shadow-2xl">
                            <div className="flex flex-col gap-8">
                                <div className="text-center flex flex-col gap-2">
                                    <h2 className="text-2xl font-bold dark:text-white">
                                        {t("landing.hero.login.title")}
                                    </h2>
                                    <p className="text-slate-500 dark:text-slate-400 text-sm">
                                        {t("landing.hero.login.subtitle")}
                                    </p>
                                </div>
                                <div className="flex flex-col gap-4">
                                    <ContinueWithGoogleButton />

                                    <div className="text-center">
                                        <WaitlistModal
                                            open={waitlistOpen}
                                            onOpenChange={setWaitlistOpen}
                                            onSwitchToLogin={() => {
                                                setWaitlistOpen(false);
                                                setLoginOpen(true);
                                            }}
                                        >
                                            <button className="text-sm text-primary hover:underline font-bold">
                                                {t("auth.register.title")}
                                            </button>
                                        </WaitlistModal>
                                    </div>

                                    <div className="relative flex items-center gap-4 py-2">
                                        <div className="h-px grow bg-slate-200 dark:bg-[#3b4754]"></div>
                                        <span className="text-xs text-slate-400 font-medium uppercase tracking-widest">
                                            {t("auth.or")}
                                        </span>
                                        <div className="h-px grow bg-slate-200 dark:bg-[#3b4754]"></div>
                                    </div>

                                    <LoginModal open={loginOpen} onOpenChange={setLoginOpen}>
                                        <button className="flex w-full items-center justify-center gap-2 rounded-lg h-12 px-5 bg-slate-100 dark:bg-white/5 text-slate-600 dark:text-slate-300 text-sm font-semibold hover:bg-slate-200 dark:hover:bg-white/10 transition-all">
                                            <span className="material-symbols-outlined text-[20px]">
                                                key
                                            </span>
                                            {t("auth.signIn")}
                                        </button>
                                    </LoginModal>
                                </div>
                                <p className="text-[11px] text-center text-slate-400 dark:text-slate-500 leading-relaxed">
                                    {t("auth.termsPrefix")}{" "}
                                    <a className="underline hover:text-primary" href="#">
                                        {t("auth.terms")}
                                    </a>{" "}
                                    {t("auth.and")}{" "}
                                    <a className="underline hover:text-primary" href="#">
                                        {t("auth.privacy")}
                                    </a>
                                </p>
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </section>
    );
}
