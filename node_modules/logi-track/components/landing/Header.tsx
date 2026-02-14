"use client";

import React, { useEffect, useState } from "react";
import { useLanguage } from "@/context/language";
import { LoginModal } from "./LoginModal";
import Link from "next/link";
import { cn } from "@/lib/utils";

export function Header() {
    const { language, setLanguage, t } = useLanguage();
    const [mounted, setMounted] = useState(false);
    const [isDark, setIsDark] = useState(false);

    // Handle hydration mismatch
    useEffect(() => {
        setMounted(true);
        // Check initial theme
        const isDarkMode = document.documentElement.classList.contains("dark");
        setIsDark(isDarkMode);
    }, []);

    const toggleTheme = () => {
        if (document.documentElement.classList.contains("dark")) {
            document.documentElement.classList.remove("dark");
            localStorage.setItem("theme", "light");
            setIsDark(false);
        } else {
            document.documentElement.classList.add("dark");
            localStorage.setItem("theme", "dark");
            setIsDark(true);
        }
    };

    if (!mounted) {
        return (
            <header className="sticky top-0 z-50 w-full border-b border-solid border-slate-200 dark:border-[#283039] bg-background-light/80 dark:bg-background-dark/80 backdrop-blur-md px-4 md:px-10 lg:px-40 py-3">
                <div className="mx-auto flex max-w-[1280px] items-center justify-between whitespace-nowrap">
                    <div className="flex items-center gap-3">
                        <h2 className="text-slate-900 dark:text-white text-xl font-bold leading-tight tracking-tight">Logi-Track</h2>
                    </div>
                </div>
            </header>
        )
    }

    return (
        <header className="sticky top-0 z-50 w-full border-b border-solid border-slate-200 dark:border-[#283039] bg-background-light/80 dark:bg-background-dark/80 backdrop-blur-md px-4 md:px-10 lg:px-40 py-3">
            <div className="mx-auto flex max-w-[1280px] items-center justify-between whitespace-nowrap">
                <div className="flex items-center gap-3">
                    <div className="text-primary size-8">
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
                    <h2 className="text-slate-900 dark:text-white text-xl font-bold leading-tight tracking-tight">
                        Logi-Track
                    </h2>
                </div>
                <div className="hidden md:flex flex-1 justify-center gap-8">
                    <Link
                        className="text-slate-600 dark:text-slate-300 hover:text-primary dark:hover:text-primary text-sm font-medium transition-colors"
                        href="#"
                    >
                        Solutions
                    </Link>
                    <Link
                        className="text-slate-600 dark:text-slate-300 hover:text-primary dark:hover:text-primary text-sm font-medium transition-colors"
                        href="#"
                    >
                        Pricing
                    </Link>
                    <Link
                        className="text-slate-600 dark:text-slate-300 hover:text-primary dark:hover:text-primary text-sm font-medium transition-colors"
                        href="#"
                    >
                        About Us
                    </Link>
                    <Link
                        className="text-slate-600 dark:text-slate-300 hover:text-primary dark:hover:text-primary text-sm font-medium transition-colors"
                        href="/join-network"
                    >
                        Join Network
                    </Link>
                </div>
                <div className="flex items-center gap-4">
                    <div className="flex items-center bg-slate-100 dark:bg-[#283039] rounded-full p-1 border border-slate-200 dark:border-[#3b4754]">
                        <button
                            onClick={() => setLanguage("en")}
                            className={cn(
                                "flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs font-bold transition-all",
                                language === "en"
                                    ? "bg-primary text-white"
                                    : "text-slate-500 dark:text-slate-400 hover:text-primary"
                            )}
                        >
                            <span className="text-sm">🇺🇸</span> EN
                        </button>
                        <button
                            onClick={() => setLanguage("th")}
                            className={cn(
                                "flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs font-bold transition-all",
                                language === "th"
                                    ? "bg-primary text-white"
                                    : "text-slate-500 dark:text-slate-400 hover:text-primary"
                            )}
                        >
                            <span className="text-sm">🇹🇭</span> TH
                        </button>
                    </div>
                    <button
                        className="flex items-center justify-center rounded-lg h-10 w-10 bg-slate-100 dark:bg-[#283039] text-slate-700 dark:text-white border border-slate-200 dark:border-[#3b4754]"
                        onClick={toggleTheme}
                    >
                        <span className="material-symbols-outlined text-[20px]">
                            {isDark ? "light_mode" : "dark_mode"}
                        </span>
                    </button>

                    <LoginModal>
                        <button className="hidden sm:flex min-w-[120px] cursor-pointer items-center justify-center rounded-lg h-10 px-4 bg-primary text-white text-sm font-bold shadow-lg shadow-primary/20 hover:bg-primary/90 transition-all">
                            {t("nav.login") || "Log In"}
                        </button>
                    </LoginModal>
                </div>
            </div>
        </header>
    );
}
