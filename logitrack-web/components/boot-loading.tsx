import { Loader2 } from "lucide-react";

/**
 * The full-screen loader of a page load: shown while the language's dictionary loads (context/language.tsx) and
 * then while `['me']` answers (the `/app` layout), so the screen and its words stay the same from the first paint
 * to the shell (UI3). The text is in both languages because it shows before any dictionary is in.
 */
export const BOOT_LOADING_TEXT = "Loading / กำลังโหลด...";

export function BootLoading() {
    return (
        <div className="flex min-h-screen flex-col items-center justify-center gap-4 bg-background" role="status" aria-busy="true">
            <Loader2 className="h-8 w-8 animate-spin text-primary" aria-hidden />
            <p className="text-center text-muted-foreground">{BOOT_LOADING_TEXT}</p>
        </div>
    );
}
