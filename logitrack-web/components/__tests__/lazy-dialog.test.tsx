import { render as plainRender, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ComponentType } from "react";
// The App Router resolves `next/dynamic` to this module (Vitest would resolve the Pages Router one).
import dynamic from "next/dist/shared/lib/app-dynamic";
import { afterEach, describe, expect, it, vi } from "vitest";
import { render } from "./test-utils";
import { LazyDialog, LazyDialogLoading, type LazyDialogControl } from "../lazy-dialog";
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

/** A dialog body behind a chunk that loads when the test says so, as the pages declare it. */
function deferredBody() {
    let settle!: { resolve: (body: ComponentType<LazyDialogControl>) => void; reject: (error: Error) => void };
    const chunk = new Promise<{ default: ComponentType<LazyDialogControl> }>((resolve, reject) => {
        settle = { resolve: (body) => resolve({ default: body }), reject };
    });
    const LazyBody = dynamic<LazyDialogControl>(() => chunk, { ssr: false, loading: LazyDialogLoading });
    return { LazyBody, settle };
}

function Page({ LazyBody }: { LazyBody: ComponentType<LazyDialogControl> }) {
    return (
        <>
            <p>First Mile board</p>
            <LazyDialog trigger={<button type="button">Open import</button>}>
                {(control) => <LazyBody {...control} />}
            </LazyDialog>
        </>
    );
}

afterEach(() => {
    vi.restoreAllMocks();
});

describe("LazyDialog", () => {
    it("mounts the body on the first open only and keeps it mounted after closing", async () => {
        const user = userEvent.setup();
        const renderBody = vi.fn((control: LazyDialogControl) => <Body {...control} />);
        plainRender(<LazyDialog trigger={<button type="button">Open import</button>}>{renderBody}</LazyDialog>);

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

    it("shows a busy dialog at once while the chunk loads, and a second click does not close it", async () => {
        // pointerEventsCheck off: the modal overlay makes the rest of the page inert, and the
        // second click is exactly a click that lands on it.
        const user = userEvent.setup({ pointerEventsCheck: 0 });
        const { LazyBody, settle } = deferredBody();
        render(<Page LazyBody={LazyBody} />);

        await user.click(await screen.findByRole("button", { name: "Open import" }));
        const shell = await screen.findByRole("dialog", { name: "Loading..." });
        expect(shell).toHaveAttribute("aria-busy", "true");

        await user.click(document.body);
        expect(screen.getByRole("dialog", { name: "Loading..." })).toBeInTheDocument();

        settle.resolve(Body);
        expect(await screen.findByRole("dialog", { name: "Import" })).toHaveTextContent("open");
        expect(screen.queryByRole("dialog", { name: "Loading..." })).toBeNull();
    });

    it("keeps the page and offers a reload when the chunk fails to load", async () => {
        vi.spyOn(console, "error").mockImplementation(() => {});
        const user = userEvent.setup();
        const { LazyBody, settle } = deferredBody();
        render(<Page LazyBody={LazyBody} />);

        await user.click(await screen.findByRole("button", { name: "Open import" }));
        await screen.findByRole("dialog", { name: "Loading..." });
        settle.reject(Object.assign(new Error("Loading chunk 6423 failed."), { name: "ChunkLoadError" }));

        const failed = await screen.findByRole("dialog", { name: "This dialog could not be opened" });
        expect(failed).toHaveTextContent("Reload the page and try again.");
        expect(screen.getByRole("button", { name: "Reload page" })).toBeInTheDocument();
        expect(screen.getByText("First Mile board")).toBeInTheDocument();

        // Closing leaves the page usable; reopening shows the same explanation, not a crash.
        await user.click(screen.getAllByRole("button", { name: "Close" })[0]);
        expect(screen.queryByRole("dialog")).toBeNull();
        expect(screen.getByText("First Mile board")).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: "Open import" }));
        expect(await screen.findByRole("dialog", { name: "This dialog could not be opened" })).toBeInTheDocument();
    });
});
