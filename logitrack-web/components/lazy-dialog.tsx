"use client";

import { Component, useState, type ErrorInfo, type ReactElement, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
    Dialog,
    DialogClose,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    DialogTrigger,
} from "@/components/ui/dialog";
import { useLanguage } from "@/context/language";

/** What a lazily mounted dialog body receives from {@link LazyDialog}. */
export type LazyDialogControl = {
    open: boolean;
    setOpen: (open: boolean) => void;
};

/**
 * Owns a dialog's root and trigger so that the dialog body (a `DialogContent`) can live in a
 * module loaded with `next/dynamic(..., { ssr: false, loading: LazyDialogLoading })` and be mounted
 * on the first open only. Import dialogs use it to keep xlsx and their own code out of the page's
 * initial JS (developer-spec.md §10.11, Appendix E §E.7 row 2).
 *
 * The trigger stays a Radix `DialogTrigger`, so focus returns to it on close and it carries the
 * usual `aria-*` state. After the first open the body stays mounted, so its state survives
 * close / reopen as it did when each dialog rendered its own trigger. The body sits inside
 * {@link LazyDialogBoundary}, so a chunk that fails to load cannot take the page down.
 */
export function LazyDialog({
    trigger,
    children,
}: {
    /** The button that opens the dialog; rendered with the page. */
    trigger: ReactElement;
    /** The dialog body, e.g. `(c) => <ImportDialogBody {...c} />` with a `next/dynamic` component. */
    children: (control: LazyDialogControl) => ReactNode;
}) {
    const [open, setOpenState] = useState(false);
    const [mounted, setMounted] = useState(false);
    const setOpen = (next: boolean) => {
        if (next) setMounted(true);
        setOpenState(next);
    };
    return (
        <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>{trigger}</DialogTrigger>
            {mounted ? <LazyDialogBoundary>{children({ open, setOpen })}</LazyDialogBoundary> : null}
        </Dialog>
    );
}

/**
 * The `loading` component for every lazily loaded dialog body: pass it to `next/dynamic` next to
 * `ssr: false`. Without it `next/dynamic` renders nothing while the chunk loads, so the first click
 * shows no reaction and a second click on the trigger closes the dialog before it ever appears.
 * This busy dialog appears at once; an outside click does not dismiss it (Escape and the close
 * button still do), and the body replaces it when its chunk is in. Renders inside a Dialog root.
 */
export function LazyDialogLoading(): ReactElement {
    const { t } = useLanguage();
    return (
        <DialogContent
            aria-busy="true"
            aria-describedby={undefined}
            className="sm:max-w-sm"
            onInteractOutside={(event) => event.preventDefault()}
        >
            <DialogTitle className="sr-only">{t("common.loading")}</DialogTitle>
            <div className="flex items-center justify-center gap-3 py-6 text-muted-foreground">
                <Loader2 className="h-6 w-6 animate-spin text-primary" aria-hidden="true" />
                <span>{t("common.loading")}</span>
            </div>
        </DialogContent>
    );
}

/** Shown in place of a dialog body that failed to load or render. Renders inside a Dialog root. */
export function LazyDialogLoadError(): ReactElement {
    const { t } = useLanguage();
    return (
        <DialogContent className="sm:max-w-md">
            <DialogHeader>
                <DialogTitle>{t("common.lazyDialog.loadErrorTitle")}</DialogTitle>
                <DialogDescription>{t("common.lazyDialog.loadErrorBody")}</DialogDescription>
            </DialogHeader>
            <DialogFooter>
                <DialogClose asChild>
                    <Button type="button" variant="outline">
                        {t("common.close")}
                    </Button>
                </DialogClose>
                <Button type="button" onClick={() => window.location.reload()}>
                    {t("common.lazyDialog.reload")}
                </Button>
            </DialogFooter>
        </DialogContent>
    );
}

/**
 * Error boundary for a lazily loaded dialog body. A `next/dynamic` body whose chunk fails to load
 * (a redeploy replaced the chunks the open tab knows, or the network dropped) throws while
 * rendering; without a boundary the error reaches the route and Next's error screen replaces the
 * whole page. This keeps the page and shows {@link LazyDialogLoadError} in the dialog instead.
 *
 * It does not reset on reopen: `next/dynamic` keeps the rejected chunk promise, and after a
 * redeploy the old chunk is gone, so only a reload recovers.
 */
export class LazyDialogBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
    state = { failed: false };

    static getDerivedStateFromError(): { failed: boolean } {
        return { failed: true };
    }

    componentDidCatch(error: unknown, info: ErrorInfo) {
        console.error("[lazy-dialog] the dialog body failed to load or render", error, info.componentStack);
    }

    render() {
        return this.state.failed ? <LazyDialogLoadError /> : this.props.children;
    }
}
