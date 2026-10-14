// UI3E: the public pages' tab title. It holds while mounted (Next's own writes are put back), and on unmount the
// title found on mount comes back, as the console's does, unless the next route has already written its own.
import React from "react";
import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { useDocumentTitle } from "./useDocumentTitle";

function Title({ title }: { title: string }) {
    useDocumentTitle(title);
    return null;
}

beforeEach(() => {
    document.title = "LogiTrack Pro by GORATECH";
});

describe("useDocumentTitle", () => {
    it("sets the title, puts it back after another write, and follows a new title", async () => {
        const { rerender } = render(<Title title="About · LogiTrack Pro" />);
        expect(document.title).toBe("About · LogiTrack Pro");
        document.title = "Something Next wrote";
        await waitFor(() => expect(document.title).toBe("About · LogiTrack Pro"));
        rerender(<Title title="เกี่ยวกับเรา · LogiTrack Pro" />);
        expect(document.title).toBe("เกี่ยวกับเรา · LogiTrack Pro");
    });

    it("restores the title found on mount when it unmounts", () => {
        const { unmount } = render(<Title title="Sign in · LogiTrack Pro" />);
        expect(document.title).toBe("Sign in · LogiTrack Pro");
        unmount();
        expect(document.title).toBe("LogiTrack Pro by GORATECH");
    });

    it("leaves the next route's title alone when that was written in the commit that unmounts it", async () => {
        const { unmount } = render(<Title title="Sign in · LogiTrack Pro" />);
        // Next writes the next route's metadata title, and the page unmounts, before the observer's callback runs.
        document.title = "Join the waitlist · LogiTrack Pro";
        unmount();
        expect(document.title).toBe("Join the waitlist · LogiTrack Pro");
        // The observer is gone: nothing puts the old title back later either.
        await new Promise((resolve) => setTimeout(resolve, 20));
        expect(document.title).toBe("Join the waitlist · LogiTrack Pro");
    });
});
