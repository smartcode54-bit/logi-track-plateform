"use client";

import { useState, type ReactElement, type ReactNode } from "react";
import { Dialog, DialogTrigger } from "@/components/ui/dialog";

/** What a lazily mounted dialog body receives from {@link LazyDialog}. */
export type LazyDialogControl = {
    open: boolean;
    setOpen: (open: boolean) => void;
};

/**
 * Owns a dialog's root and trigger so that the dialog body (a `DialogContent`) can live in a
 * module loaded with `next/dynamic(..., { ssr: false })` and be mounted on the first open only.
 * Import dialogs use it to keep xlsx and their own code out of the page's initial JS
 * (developer-spec.md §10.11, Appendix E §E.7 row 2).
 *
 * The trigger stays a Radix `DialogTrigger`, so focus returns to it on close and it carries the
 * usual `aria-*` state. After the first open the body stays mounted, so its state survives
 * close / reopen as it did when each dialog rendered its own trigger.
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
            {mounted ? children({ open, setOpen }) : null}
        </Dialog>
    );
}
