"use client";

import { Button } from "@/components/ui/button";
import { Printer } from "lucide-react";
import { looksLikeImageUrl } from "@/features/maintenance/utils/looksLikeImageUrl";
import { printImageUrl } from "@/lib/print-image-url";

/** Print action for the image currently shown in accounting detail previews. */
export function AccountingPreviewPrintButton({
    url,
    printLabel,
}: {
    url: string;
    printLabel: string;
}) {
    if (!url || !looksLikeImageUrl(url)) return null;

    return (
        <div className="shrink-0 flex justify-center border-t border-border bg-muted/15 px-3 py-2">
            <Button type="button" variant="outline" size="sm" onClick={() => printImageUrl(url)}>
                <Printer className="mr-2 h-4 w-4" />
                {printLabel}
            </Button>
        </div>
    );
}
