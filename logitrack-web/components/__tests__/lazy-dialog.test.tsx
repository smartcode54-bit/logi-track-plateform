import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { LazyDialog, type LazyDialogControl } from "../lazy-dialog";
import { DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";

function Body({ open, setOpen }: LazyDialogControl) {
    return (
        <DialogContent>
            <DialogTitle>Import</DialogTitle>
            <DialogDescription>{open ? "open" : "closed"}</DialogDescription>
            <button type="button" onClick={() => setOpen(false)}>Done</button>
        </DialogContent>
    );
}

describe("LazyDialog", () => {
    it("mounts the body on the first open only and keeps it mounted after closing", async () => {
        const user = userEvent.setup();
        const renderBody = vi.fn((control: LazyDialogControl) => <Body {...control} />);
        render(<LazyDialog trigger={<button type="button">Open import</button>}>{renderBody}</LazyDialog>);

        expect(renderBody).not.toHaveBeenCalled();
        const trigger = screen.getByRole("button", { name: "Open import" });
        expect(trigger).toHaveAttribute("aria-expanded", "false");

        await user.click(trigger);
        expect(await screen.findByRole("dialog")).toHaveTextContent("open");
        expect(trigger).toHaveAttribute("aria-expanded", "true");

        await user.click(screen.getByRole("button", { name: "Done" }));
        expect(screen.queryByRole("dialog")).toBeNull();
        expect(renderBody).toHaveBeenLastCalledWith({ open: false, setOpen: expect.any(Function) });
        expect(trigger).toHaveFocus();

        await user.click(trigger);
        expect(await screen.findByRole("dialog")).toHaveTextContent("open");
    });
});
