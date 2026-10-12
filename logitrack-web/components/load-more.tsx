"use client";

import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";

interface LoadMoreProps {
    /** Rows loaded so far. */
    loaded: number;
    hasNextPage: boolean;
    isFetchingNextPage: boolean;
    /** The last next-page request failed (`isFetchNextPageError`): said in the footer; the button retries it. */
    error?: boolean;
    onLoadMore: () => void;
}

/**
 * The footer of a keyset list (T25, W9): how many rows are loaded and a button for the next page,
 * so no list stops silently at its first page (the caps of Appendix E §E.10).
 */
export function LoadMore({ loaded, hasNextPage, isFetchingNextPage, error = false, onLoadMore }: LoadMoreProps) {
    const { t } = useLanguage();
    return (
        <div className="flex items-center justify-between gap-2 text-sm text-muted-foreground">
            <span>{t("masterData.list.loaded", { count: loaded })}</span>
            {hasNextPage && error && (
                <span role="alert" className="ml-auto text-destructive">
                    {t("masterData.list.loadMoreError")}
                </span>
            )}
            {hasNextPage ? (
                <Button type="button" variant="outline" size="sm" onClick={onLoadMore} disabled={isFetchingNextPage}>
                    {isFetchingNextPage && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
                    {t("masterData.list.loadMore")}
                </Button>
            ) : (
                loaded > 0 && <span>{t("masterData.list.allLoaded")}</span>
            )}
        </div>
    );
}
