import React, { useRef } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { LanguageProvider } from "@/context/language";
import { ConfirmDialog } from "./ConfirmDialog";

function Harness({ open, trigger }: { open: boolean; trigger: "enabled" | "disabled" | "detached" }) {
    const returnFocus = useRef<HTMLButtonElement>(null);
    const fallbackFocus = useRef<HTMLHeadingElement>(null);
    return <LanguageProvider>
        {trigger !== "detached" ? <button ref={returnFocus} disabled={trigger === "disabled"}>Trigger</button> : null}
        <h2 ref={fallbackFocus} tabIndex={-1}>Stable heading</h2>
        <ConfirmDialog open={open} busy={false} confirmDisabled={false} title="Confirm" description="Confirm this change"
            confirmLabel="Confirm" returnFocus={returnFocus} fallbackFocus={fallbackFocus} onCancel={() => {}} onConfirm={() => {}} />
    </LanguageProvider>;
}

describe("Confirmation focus restoration", () => {
    it.each(["enabled", "disabled", "detached"] as const)("restores focus safely when the trigger becomes %s", async (trigger) => {
        const view = render(<Harness open trigger="enabled" />);
        await screen.findByRole("alertdialog");
        view.rerender(<Harness open={false} trigger={trigger} />);
        await waitFor(() => expect(trigger === "enabled" ? screen.getByRole("button", { name: "Trigger" }) : screen.getByRole("heading", { name: "Stable heading" })).toHaveFocus());
    });
});
