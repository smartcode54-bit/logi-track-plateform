"use client";

/** `/app/chat` over Go (T50): the console's empty room, or the broadcast composer (`?view=broadcast`). */
import { useSearchParams } from "next/navigation";
import { PageHeader } from "@/components/page-header";
import { useLanguage } from "@/context/language";
import { BroadcastComposerGo } from "./BroadcastComposerGo";

export function ChatHomeGo() {
    const { t } = useLanguage();
    const view = useSearchParams()?.get("view");
    if (view === "broadcast") return <BroadcastComposerGo />;
    return (
        <div className="flex flex-1 flex-col p-4">
            {/* The page's name is the menu entry's (UI3B: it read "Command Center" under the crumb "Chat"). */}
            <PageHeader title={t("nav.chat")} description={t("chat.home.hint")} />
        </div>
    );
}
