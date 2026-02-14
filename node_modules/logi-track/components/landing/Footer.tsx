import React from "react";
import { useLanguage } from "@/context/language";

export function Footer() {
    const { t } = useLanguage();

    return (
        <footer className="border-t border-slate-200 dark:border-[#283039] py-12 lg:px-40">
            <div className="mx-auto max-w-[1280px] px-4 flex flex-col gap-10">
                <div className="flex flex-col md:flex-row justify-between items-center gap-8">
                    <div className="flex items-center gap-3">
                        <div className="text-primary size-6">
                            <svg
                                fill="none"
                                viewBox="0 0 48 48"
                                xmlns="http://www.w3.org/2000/svg"
                            >
                                <path
                                    clipRule="evenodd"
                                    d="M24 18.4228L42 11.475V34.3663C42 34.7796 41.7457 35.1504 41.3601 35.2992L24 42V18.4228Z"
                                    fill="currentColor"
                                    fillRule="evenodd"
                                ></path>
                                <path
                                    clipRule="evenodd"
                                    d="M24 8.18819L33.4123 11.574L24 15.2071L14.5877 11.574L24 8.18819ZM9 15.8487L21 20.4805V37.6263L9 32.9945V15.8487ZM27 37.6263V20.4805L39 15.8487V32.9945L27 37.6263ZM25.354 2.29885C24.4788 1.98402 23.5212 1.98402 22.646 2.29885L4.98454 8.65208C3.7939 9.08038 3 10.2097 3 11.475V34.3663C3 36.0196 4.01719 37.5026 5.55962 38.098L22.9197 44.7987C23.6149 45.0671 24.3851 45.0671 25.0803 44.7987L42.4404 38.098C43.9828 37.5026 45 36.0196 45 34.3663V11.475C45 10.2097 44.2061 9.08038 43.0155 8.65208L25.354 2.29885Z"
                                    fill="currentColor"
                                    fillRule="evenodd"
                                ></path>
                            </svg>
                        </div>
                        <h2 className="text-slate-900 dark:text-white text-lg font-bold">
                            Logi-Track
                        </h2>
                    </div>
                    <div className="flex flex-wrap justify-center gap-x-8 gap-y-4">
                        <a
                            className="text-slate-500 dark:text-[#9dabb9] text-sm hover:text-primary transition-colors"
                            href="#"
                        >
                            {t("auth.privacy")}
                        </a>
                        <a
                            className="text-slate-500 dark:text-[#9dabb9] text-sm hover:text-primary transition-colors"
                            href="#"
                        >
                            {t("landing.footer.terms")}
                        </a>
                        <a
                            className="text-slate-500 dark:text-[#9dabb9] text-sm hover:text-primary transition-colors"
                            href="#"
                        >
                            {t("landing.footer.contact")}
                        </a>
                    </div>
                    <div className="flex gap-4">
                        <div className="flex items-center gap-2 text-xs font-bold text-slate-400 bg-slate-100 dark:bg-white/5 px-3 py-1.5 rounded-full">
                            <span className="w-2 h-2 rounded-full bg-emerald-500"></span>
                            {t("landing.footer.systemOnline")}
                        </div>
                    </div>
                </div>
                <div className="flex flex-col md:flex-row justify-between items-center gap-4 text-slate-400 text-xs border-t border-slate-200 dark:border-[#283039] pt-8">
                    <p>© 2025 Wanpen-Radchada Transport co.,ltd. {t("landing.footer.rights")}</p>
                    <div className="flex items-center gap-4">
                        <span className="flex items-center gap-1.5">
                            <span className="material-symbols-outlined text-xs">
                                location_on
                            </span>{" "}
                            {t("landing.footer.location")}
                        </span>
                        <span className="w-1 h-1 rounded-full bg-slate-600"></span>
                        <span className="flex items-center gap-1.5">
                            <span className="material-symbols-outlined text-xs">public</span>{" "}
                            {t("landing.footer.global")}
                        </span>
                    </div>
                </div>
            </div>
        </footer>
    );
}
