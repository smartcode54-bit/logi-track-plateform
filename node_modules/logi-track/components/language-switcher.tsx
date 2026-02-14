"use client";

import Image from "next/image";
import { useLanguage } from "@/context/language";
import { Button } from "@/components/ui/button";

export function LanguageSwitcher() {
    const { language, setLanguage } = useLanguage();

    return (
        <div className="flex items-center p-1 bg-muted/50 rounded-lg border border-border">
            <Button
                variant={language === "en" ? "default" : "ghost"}
                size="sm"
                onClick={() => setLanguage("en")}
                className={`h-8 px-3 rounded-md transition-all ${language === "en" ? "shadow-sm" : "hover:bg-transparent"}`}
            >
                <div className="flex items-center gap-2">
                    <Image
                        src="/england_round_icon_64.png"
                        alt="English"
                        width={20}
                        height={20}
                        className="w-5 h-5 rounded-full object-cover"
                    />
                    <span className="text-xs font-medium">EN</span>
                </div>
            </Button>
            <Button
                variant={language === "th" ? "default" : "ghost"}
                size="sm"
                onClick={() => setLanguage("th")}
                className={`h-8 px-3 rounded-md transition-all ${language === "th" ? "shadow-sm" : "hover:bg-transparent"}`}
            >
                <div className="flex items-center gap-2">
                    <Image
                        src="/thailand_round_icon_64.png"
                        alt="Thai"
                        width={20}
                        height={20}
                        className="w-5 h-5 rounded-full object-cover"
                    />
                    <span className="text-xs font-medium">TH</span>
                </div>
            </Button>
        </div>
    );
}
