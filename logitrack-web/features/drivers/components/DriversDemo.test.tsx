import React from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { useLanguage } from "@/context/language";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { ApiError } from "@/lib/apiError";
import { resetQueryClientForTests } from "@/lib/queryClient";
import { toast } from "sonner";
import { driverBodyFromForm, driverFormValues, type DriverDetailDTO } from "../api/drivers";
import DriversList from "./DriversList";
import DriverPreview from "./DriverPreview";
import EditDriverForm from "./EditDriverForm";
import NewDriverForm from "./NewDriverForm";

const push = vi.hoisted(() => vi.fn());
vi.mock("next/navigation", () => ({
    useRouter: () => ({ push, replace: vi.fn(), prefetch: vi.fn(), back: vi.fn() }),
    usePathname: () => "/app/drivers/view",
    useSearchParams: () => new URLSearchParams("id=driver-1"),
}));

const driver = {
    id: "driver-1", tenantId: "own-1", displayName: "Somchai Test", firstName: "Somchai", lastName: "Test", fullNameTh: "สมชาย ใจดี",
    mobile: "0812345678", email: "driver@example.test", status: "active", employmentType: "full_time", userId: null,
    birthDate: "1990-01-01", idCard: "1234567890123", truckLicenseId: "12345678",
    customerDriverIds: [], statusHistory: [], currentAssignment: { truckId: "truck-1", truckPlate: "70-1234", truckModel: "Hino" },
} as unknown as DriverDetailDTO;

function serve(records = [driver], manageCustomers = false) {
    return fakeWeb()
        .on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({
            steward: manageCustomers,
            tenant: { id: "carrier-1", nameTh: "รถร่วม", nameEn: "Carrier", kind: "carrier", role: "tenant_admin" },
            capabilities: ["drivers:view", "drivers:edit", ...(manageCustomers ? ["fleet:manage_customers"] : [])],
        }) }))
        .on("GET", "/api/go/v1/drivers", () => json(200, { data: records, meta: { statusCounts: { active: records.length } } }))
        .on("GET", "/api/go/v1/drivers/driver-1", () => json(200, { data: records[0] }))
        .on("GET", "/api/go/v1/subcontractors", () => json(200, { data: [] }))
        .on("GET", "/api/go/v1/truck-assignments", () => json(200, { data: [{ id: "a1", truckId: "truck-1", truckPlate: "70-1234", truckModel: "Hino", status: "active" }] }));
}

function SwitchLanguage() {
    const { setLanguage } = useLanguage();
    return <button onClick={() => setLanguage("th")}>Thai</button>;
}

afterEach(() => {
    resetQueryClientForTests();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
    push.mockClear();
});

describe("fleet driver demo regressions", () => {
    it.each([["view", DriverPreview], ["edit", EditDriverForm]] as const)("uses the Thai display name in the %s heading", async (_name, Component) => {
        serve();
        renderWithProviders(<Component />);
        expect(await screen.findByRole("heading", { level: 1, name: driver.fullNameTh! })).toBeInTheDocument();
    });

    it.each([null, "user-1"])("offers chat only with a Go user (%s)", async (userId) => {
        serve([{ ...driver, userId, legacyAuthUid: "legacy-only" }]);
        renderWithProviders(<DriversList />);
        const row = await screen.findByRole("row", { name: /Somchai|สมชาย/ });
        await userEvent.setup().click(within(row).getByRole("button"));
        expect(Boolean(screen.queryByRole("menuitem", { name: "Chat" }))).toBe(Boolean(userId));
    });

    it("keeps a second row trigger click out of the row after Escape", async () => {
        serve([driver, { ...driver, id: "driver-2", fullNameTh: "วิชัย ใจดี" }]);
        renderWithProviders(<DriversList />);
        const user = userEvent.setup();
        const rows = await screen.findAllByRole("button", { name: /Actions for/ });
        expect(rows[0]).toHaveClass("max-md:h-11", "max-md:w-11");
        await user.click(rows[0]);
        await user.keyboard("{Escape}");
        fireEvent.click(rows[1]);
        expect(push).not.toHaveBeenCalled();
    });

    it.each([["list", DriversList], ["view", DriverPreview]] as const)("links truck plate and model to its detail in %s", async (_name, Component) => {
        serve();
        renderWithProviders(<Component />);
        const link = await screen.findByRole("link", { name: /70-1234.*Hino/ });
        expect(link).toHaveAttribute("href", "/app/trucks/view?id=truck-1");
        expect(link).toHaveClass("max-md:min-h-11");
        link.addEventListener("click", (event) => event.preventDefault());
        fireEvent.click(link);
        expect(push).not.toHaveBeenCalled();
    });

    it("keeps the saved driver ID in the wizard when an upload fails and retries without another POST", async () => {
        vi.stubGlobal("scrollTo", vi.fn());
        let failing = true;
        const web = serve()
            .on("POST", "/api/go/v1/drivers", () => json(201, { data: driver }))
            .on("POST", "/api/go/v1/uploads/presign", () => failing ? json(503, { error: { code: "unavailable" } })
                : json(200, { data: { key: "drivers/driver-1/id.pdf", url: "/v1/uploads/local/id", method: "PUT" } }))
            .on("PUT", "/api/go/v1/uploads/local/id", () => json(200, {}))
            .on("PATCH", "/api/go/v1/drivers/driver-1", () => json(200, { data: driver }));
        renderWithProviders(<NewDriverForm />);
        const user = userEvent.setup();
        for (const [name, value] of [["First Name", "Somchai"], ["Last Name", "Test"], ["Full Name (Thai)", "สมชาย ใจดี"], ["Mobile", "0812345678"]]) {
            await user.type(await screen.findByRole("textbox", { name }), value);
        }
        await user.click(screen.getByRole("combobox", { name: "Birth Date (Age 20-55)" }));
        const day = within(await screen.findByRole("grid")).getAllByRole("button").find((button) => !button.hasAttribute("disabled"))!;
        await user.click(day);
        await user.click(screen.getByRole("button", { name: "Next Step" }));
        await user.type(screen.getByRole("textbox", { name: "ID Number" }), "1234567890123");
        await user.type(screen.getByRole("textbox", { name: "License Number" }), "12345678");
        await user.upload(screen.getByLabelText("ID Card Image"), new File(["pdf"], "id.pdf", { type: "application/pdf" }));
        await user.click(screen.getByRole("button", { name: "Next Step" }));
        await user.click(screen.getByRole("switch"));
        await user.click(screen.getByRole("button", { name: "Next Step" }));
        await user.click(screen.getByRole("button", { name: "Complete Registration" }));
        expect(await screen.findByText(/Driver saved, but some files/)).toBeInTheDocument();
        expect(screen.getByRole("link", { name: "View Profile" })).toHaveAttribute("href", "/app/drivers/view?id=driver-1");
        failing = false;
        await user.click(screen.getByRole("button", { name: "Complete Registration" }));
        await waitFor(() => expect(push).toHaveBeenCalledWith("/app/drivers"));
        expect(web.calls.filter((call) => call.method === "POST" && call.url === "/api/go/v1/drivers")).toHaveLength(1);
        expect(web.calls.find((call) => call.method === "PATCH")?.url).toBe("/api/go/v1/drivers/driver-1");
    });

    it("renders real customer ID types and puts an unresolved type error under the entered field", async () => {
        serve([{ ...driver, customerDriverIds: [{ customerId: "customer-1", customerCode: "CJSF", customerName: "Customer One", idTypeKey: "badge", value: "old" }] }], true)
            .on("GET", "/api/go/v1/customers/customer-1", () => json(200, { data: { id: "customer-1", code: "CJSF", name: "Customer One", driverIdTypes: [{ key: "badge", label: "Driver badge" }] } }))
            .on("PATCH", "/api/go/v1/drivers/driver-1", () => json(422, { error: { code: "invalid_argument", details: { fields: [{ field: "customerDriverIds[0].idTypeKey", reason: "not_found" }] } } }));
        renderWithProviders(<EditDriverForm />);
        const input = await screen.findByRole("textbox", { name: "Driver badge" });
        const user = userEvent.setup();
        await user.clear(input);
        await user.type(input, "entered-value");
        await user.click(screen.getByRole("button", { name: "Save Changes" }));
        await waitFor(() => expect(input).toHaveAttribute("aria-invalid", "true"));
        expect(input).toHaveAccessibleDescription(/not found/i);
        expect(input).toHaveValue("entered-value");
        expect(push).not.toHaveBeenCalled();
    });

    it("lets a carrier admin edit held customer IDs without requesting protected customer details", async () => {
        const web = serve([{ ...driver, customerDriverIds: [{ customerId: "customer-1", customerCode: "SPX", customerName: "Shopee", idTypeKey: "appId", value: "old" }] }])
            .on("GET", "/api/go/v1/customers/customer-1", () => json(403, { error: { code: "permission_denied" } }))
            .on("PATCH", "/api/go/v1/drivers/driver-1", () => json(200, { data: driver }));
        renderWithProviders(<EditDriverForm />);
        const user = userEvent.setup();
        const input = await screen.findByRole("textbox", { name: "App ID" });
        await user.clear(input);
        await user.type(input, "carrier-id");
        await user.click(screen.getByRole("button", { name: "Save Changes" }));
        await waitFor(() => expect(web.calls.find((call) => call.method === "PATCH")?.body).toMatchObject({
            customerDriverIds: [{ customerId: "customer-1", idTypeKey: "appId", value: "carrier-id" }],
        }));
        expect(web.count("GET", "/api/go/v1/customers/customer-1")).toBe(0);
        expect(screen.queryByRole("combobox", { name: "Add customer IDs" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
    });

    it("keeps held IDs editable after a customer detail 403, with a note and no retry", async () => {
        const web = serve([{ ...driver, customerDriverIds: [{ customerId: "customer-1", customerCode: "SPX", customerName: "Shopee", idTypeKey: "workId", value: "held" }] }], true)
            .on("GET", "/api/go/v1/customers/customer-1", () => json(403, { error: { code: "permission_denied" } }));
        renderWithProviders(<EditDriverForm />);
        expect(await screen.findByText("Customer ID settings are unavailable for your role. You can edit existing IDs.")).toBeInTheDocument();
        expect(screen.getByRole("textbox", { name: "Work ID" })).toHaveValue("held");
        expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
        expect(web.count("GET", "/api/go/v1/customers/customer-1")).toBe(1);
    });

    it("saves when an additional optional customer ID type is left untouched", async () => {
        const web = serve([{ ...driver, customerDriverIds: [{ customerId: "customer-1", customerCode: "CJSF", customerName: "Customer One", idTypeKey: "badge", value: "held" }] }], true)
            .on("GET", "/api/go/v1/customers/customer-1", () => json(200, { data: { id: "customer-1", code: "CJSF", name: "Customer One", driverIdTypes: [{ key: "badge", label: "Badge" }, { key: "optional", label: "Optional ID" }] } }))
            .on("PATCH", "/api/go/v1/drivers/driver-1", () => json(200, { data: driver }));
        renderWithProviders(<EditDriverForm />);
        expect(await screen.findByRole("textbox", { name: "Optional ID" })).toHaveValue("");
        const user = userEvent.setup();
        await user.clear(screen.getByRole("textbox", { name: "Badge" }));
        await user.type(screen.getByRole("textbox", { name: "Badge" }), "updated");
        await user.click(screen.getByRole("button", { name: "Save Changes" }));
        await waitFor(() => expect(web.calls.find((call) => call.method === "PATCH")?.body).toMatchObject({
            customerDriverIds: [{ customerId: "customer-1", idTypeKey: "badge", value: "updated" }],
        }));
    });

    it.each([["edit", EditDriverForm], ["new", NewDriverForm]] as const)("updates visible %s validation errors when the language changes", async (_name, Component) => {
        serve();
        renderWithProviders(<><SwitchLanguage /><Component /></>);
        const input = await screen.findByRole("textbox", { name: "First Name" });
        const user = userEvent.setup();
        await user.type(input, "x");
        await user.clear(input);
        expect(await screen.findByText("First name is required")).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: "Thai" }));
        expect(await screen.findByText("กรุณากรอกชื่อ")).toBeInTheDocument();
        expect(screen.queryByText("First name is required")).not.toBeInTheDocument();
    });

    it("translates schema keys in the shared invalid-form summary", async () => {
        serve();
        const errorToast = vi.spyOn(toast, "error");
        renderWithProviders(<EditDriverForm />);
        const input = await screen.findByRole("textbox", { name: "First Name" });
        const user = userEvent.setup();
        await user.clear(input);
        await user.click(screen.getByRole("button", { name: "Save Changes" }));
        await waitFor(() => expect(errorToast).toHaveBeenCalledWith("Check the highlighted fields (1). First: First name is required"));
    });

    it("leaves next-page failures to LoadMore while preserving loaded drivers", async () => {
        const web = fakeWeb()
            .on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }))
            .on("GET", "/api/go/v1/drivers", (call) => call.url.includes("cursor=")
                ? json(503, { error: { code: "unavailable" } })
                : json(200, { data: [driver], nextCursor: "next" }));
        const { client } = renderWithProviders(<DriversList />);
        client.setDefaultOptions({ queries: { retry: false } });
        await screen.findByText(driver.fullNameTh!);
        await userEvent.setup().click(screen.getByRole("button", { name: "Load more" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("The next rows could not be loaded.");
        expect(screen.queryByText("Could not load drivers. Try again.")).not.toBeInTheDocument();
        expect(screen.getByText(driver.fullNameTh!)).toBeInTheDocument();
        expect(web.count("GET", "/api/go/v1/drivers")).toBe(2);
    });

    it("shows a failed driver list with retry instead of no drivers", async () => {
        let failed = true;
        fakeWeb().on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }))
            .on("GET", "/api/go/v1/drivers", () => failed ? json(503, { error: { code: "unavailable" } }) : json(200, { data: [driver] }));
        const { client } = renderWithProviders(<DriversList />);
        client.setDefaultOptions({ queries: { retry: false } });
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not load drivers");
        expect(screen.queryByText("No drivers found")).toBeNull();
        failed = false;
        await userEvent.setup().click(screen.getByRole("button", { name: "Try again" }));
        expect(await screen.findByText(driver.fullNameTh!)).toBeInTheDocument();
    });

    it("refuses an unresolved customer at its input path instead of omitting its value", () => {
        const values = { ...driverFormValues(driver), customerDriverIds: { SPX: { appId: "entered-id" } } };
        expect(() => driverBodyFromForm(values, new Map())).toThrow();
        try {
            driverBodyFromForm(values, new Map());
        } catch (error) {
            expect((error as ApiError).details.fields).toContainEqual(expect.objectContaining({ field: "customerDriverIds.SPX.appId" }));
        }
        expect(driverBodyFromForm(values, new Map([["SPX", "customer-1"]])).customerDriverIds).toEqual([
            { customerId: "customer-1", idTypeKey: "appId", value: "entered-id" },
        ]);
    });
});
