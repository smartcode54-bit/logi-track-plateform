import React, { useState } from "react";
import { useLanguage } from "@/context/language";
import { WaitlistModal } from "./WaitlistModal";
import { LoginModal } from "./LoginModal";

export function CTA() {
    const { t } = useLanguage();
    const [loginOpen, setLoginOpen] = useState(false);
    const [waitlistOpen, setWaitlistOpen] = useState(false);

    return (
        <section className="py-20 lg:px-40 relative overflow-hidden">
            <div className="absolute inset-0 bg-primary/5 pointer-events-none"></div>
            <div className="mx-auto max-w-[1280px] px-4 relative">
                <div className="flex flex-col items-center justify-center gap-8 text-center bg-primary rounded-3xl p-12 md:p-20 shadow-2xl shadow-primary/20">
                    <div className="flex flex-col gap-4 max-w-2xl">
                        <h2 className="text-white text-3xl md:text-5xl font-black leading-tight">
                            {t("landing.cta.title")}
                        </h2>
                        <p className="text-white/80 text-lg">
                            {t("landing.cta.description")}
                        </p>
                    </div>
                    <div className="flex flex-wrap justify-center gap-4">
                        <WaitlistModal
                            open={waitlistOpen}
                            onOpenChange={setWaitlistOpen}
                            onSwitchToLogin={() => {
                                setWaitlistOpen(false);
                                setLoginOpen(true);
                            }}
                        >
                            <button className="flex items-center justify-center rounded-xl min-w-[220px] h-16 px-8 bg-white text-primary text-lg font-bold shadow-xl hover:bg-slate-100 hover:scale-105 active:scale-95 transition-all duration-300">
                                {t("landing.cta.requestDemo")}
                            </button>
                        </WaitlistModal>
                        <LoginModal open={loginOpen} onOpenChange={setLoginOpen} />
                        <button className="flex items-center justify-center rounded-xl min-w-[220px] h-16 px-8 border-2 border-white/30 bg-transparent text-white text-lg font-bold hover:bg-white/10 transition-all">
                            {t("landing.cta.viewApi")}
                        </button>
                    </div>
                </div>
            </div>
        </section>
    );
}
