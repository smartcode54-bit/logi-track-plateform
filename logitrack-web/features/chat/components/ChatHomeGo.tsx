"use client";

/** `/app/chat` over Go (T50): the console's empty room, or the broadcast composer (`?view=broadcast`). */
import { useSearchParams } from "next/navigation";
import { MessageCircle } from "lucide-react";

import { useLanguage } from "@/context/language";
import { BroadcastComposerGo } from "./BroadcastComposerGo";

export function ChatHomeGo() {
    const { t } = useLanguage();
    const view = useSearchParams()?.get("view");
    if (view === "broadcast") return <BroadcastComposerGo />;
    return (
        <div className="flex flex-1 flex-col items-center justify-center p-8 text-center">
            <MessageCircle className="mb-4 h-16 w-16 text-muted-foreground/50" />
            <h2 className="mb-1 text-lg font-semibold text-foreground">{t("chat.home.title")}</h2>
            <p className="max-w-sm text-sm text-muted-foreground">{t("chat.home.hint")}</p>
        </div>
    );
}
