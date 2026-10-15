import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { ApiError } from "@/lib/apiError";
import { resetQueryClientForTests } from "@/lib/queryClient";
import { QueryError, QueryLoading } from "./page-states";

afterEach(() => { resetQueryClientForTests(); vi.unstubAllGlobals(); });

function serve() {
    fakeWeb().on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }));
}

describe("shared query states compatibility", () => {
    it("uses a localized default loading label", async () => {
        serve();
        renderWithProviders(<QueryLoading />);
        expect(await screen.findByRole("status")).toHaveTextContent("Loading");
    });

    it("accepts a label without an error and retries", async () => {
        serve();
        const retry = vi.fn();
        renderWithProviders(<QueryError label="Load failed" onRetry={retry} />);
        expect(await screen.findByRole("alert")).toHaveTextContent("Load failed");
        const button = screen.getByRole("button", { name: "Try again" });
        expect(button).toHaveClass("max-md:min-h-11", "max-md:min-w-11");
        await userEvent.setup().click(button);
        expect(retry).toHaveBeenCalledOnce();
    });

    it("accepts an error without a label and disables retry while retrying", async () => {
        serve();
        const error = new ApiError({ status: 503, code: "unavailable", message: "" });
        renderWithProviders(<QueryError error={error} onRetry={vi.fn()} retrying />);
        expect(await screen.findByRole("alert")).not.toBeEmptyDOMElement();
        expect(screen.getByRole("button", { name: "Try again" })).toBeDisabled();
    });
});
