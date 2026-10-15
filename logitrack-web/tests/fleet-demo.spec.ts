import { randomBytes, randomInt } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { hasTestCredentials, login } from "./helpers/auth";
import trucks from "../context/locales/th/trucks";
import drivers from "../context/locales/th/drivers";
import partners from "../context/locales/th/subcontractors";
import assignments from "../context/locales/th/assignments";

/**
 * PO walkthrough of nav.fleetPartners, using the default config and an already-running local stack.
 * No mocks, API seeding, server startup, or destructive menu selections.
 * FLEET_DEMO_WRITE=1 additionally leaves one identifiable demo truck/driver and their assignment.
 * A single test owns both viewports and findings.json; writes are never retried automatically.
 */
type Finding = { route: string; width: number; kind: string; detail: string };
type RecordKind = "trucks" | "drivers" | "subcontractors";
const output = path.resolve(__dirname, "../test-results/fleet-demo");
const viewports = [{ width: 1280, height: 900 }, { width: 390, height: 844 }];
const label = (text: string) => new RegExp(`^${text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\*?\\s*$`);
const routeOf = (url: string) => {
    const parsed = new URL(url);
    return parsed.pathname + parsed.search;
};
const errorText = (error: unknown) => error instanceof Error ? error.message : String(error);

class Walkthrough {
    /** Requests started and not yet finished, excluding the app's long-lived SSE stream. */
    private readonly inFlightRequests = new Map<object, string>();

    constructor(readonly page: Page, readonly findings: Finding[]) {
        page.setDefaultTimeout(10_000);
        page.setDefaultNavigationTimeout(30_000);
        const isStream = (url: string) => new URL(url).pathname === "/api/go/v1/events";
        page.on("request", (request) => { if (!isStream(request.url())) this.inFlightRequests.set(request, request.url()); });
        page.on("requestfinished", (request) => this.inFlightRequests.delete(request));
        page.on("requestfailed", (request) => this.inFlightRequests.delete(request));
        page.on("console", (message) => {
            if (message.type() === "error") this.record("console-error", message.text());
        });
        page.on("pageerror", (error) => this.record("page-error", error.stack ?? error.message));
        page.on("response", (response) => {
            const url = new URL(response.url());
            if (/^\/api\/go(?:\/|$)/.test(url.pathname) && response.status() >= 400) {
                // Keep every failed response, including repeated failures/retries.
                const referer = response.request().headers().referer;
                this.record("api-error", `${response.request().method()} ${url.pathname}${url.search} ${response.status()}`,
                    referer?.startsWith("http") ? routeOf(referer) : undefined);
            }
        });
    }

    record(kind: string, detail: string, route?: string) {
        this.findings.push({
            route: route ?? (this.page.url().startsWith("http") ? routeOf(this.page.url()) : "/login"),
            width: this.page.viewportSize()?.width ?? 1280,
            kind,
            detail,
        });
    }

    async check(kind: string, detail: string, action: () => Promise<unknown>) {
        try {
            await action();
            return true;
        } catch (error) {
            this.record(kind, `${detail}: ${errorText(error)}`);
            return false;
        }
    }

    async observeFormErrors() {
        await this.page.exposeFunction("fleetDemoReport", (kind: string, detail: string) => this.record(kind, detail));
        await this.page.addInitScript(() => {
            // Catch transient errors before a toast disappears, a wizard changes steps, or a save redirects.
            const seen = new WeakMap<Element, string>();
            const report = () => {
                const nodes = document.querySelectorAll(
                    '[data-sonner-toast][data-type="error"], [data-slot="form-message"], ' +
                    'main form [class*="bg-destructive/15"], [role="alert"][data-variant="destructive"]',
                );
                for (const node of nodes) {
                    if (!(node instanceof HTMLElement) || !node.getClientRects().length) continue;
                    const text = node.innerText.trim();
                    if (!text || seen.get(node) === text) continue;
                    seen.set(node, text);
                    const bridge = window as typeof window & { fleetDemoReport: (kind: string, detail: string) => Promise<void> };
                    void bridge.fleetDemoReport(node.matches("[data-sonner-toast]") ? "toast-error" : "field-error", text);
                }
            };
            const start = () => {
                new MutationObserver(report).observe(document.documentElement, {
                    childList: true, subtree: true, characterData: true, attributes: true,
                });
                report();
            };
            if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start, { once: true });
            else start();
        });
    }

    async idle() {
        // Playwright's "networkidle" never settles here: the app keeps one SSE stream (/api/go/v1/events) open by design.
        // Wait for every other request to finish and stay quiet for 500 ms instead, so a real hang is still reported.
        await this.check("network-idle", "Page did not settle", async () => {
            await this.page.waitForLoadState("load", { timeout: 20_000 });
            const deadline = Date.now() + 20_000;
            let quietSince = Date.now();
            while (Date.now() - quietSince < 500) {
                if (Date.now() > deadline) throw new Error(`Still loading: ${[...this.inFlightRequests.values()].map((url) => new URL(url).pathname).join(", ")}`);
                await this.page.waitForTimeout(100);
                if (this.inFlightRequests.size) quietSince = Date.now();
            }
        });
    }

    async pageChecks() {
        await this.check("page-state", "Error boundary or unavailable record", async () => {
            await expect(this.page.locator(
                '[data-testid="page-error"], [data-testid="record-not-found"], [data-testid="record-load-error"], ' +
                '[data-testid="record-forbidden"], [data-testid="page-not-found"]',
            )).toHaveCount(0);
            await expect(this.page.locator("body")).not.toContainText(
                /เกิดข้อผิดพลาด|\bnot found\b|something went wrong|หน้านี้ทำงานผิดพลาด|ไม่พบหน้านี้|LogiTrack Pro หยุดทำงาน/i,
            );
        });
        await this.check("thai-copy", "Expected the Thai interface", async () => {
            await expect(this.page.locator("html")).toHaveAttribute("lang", "th");
            await expect(this.page.locator("main h1").first()).toContainText(/[\u0e00-\u0e7f]/);
            await expect(this.page.locator("main")).not.toContainText(
                /\b(?:nav|trucks|drivers|assignments|renewals|maintenance|subcontractors|masterData|common)\.[a-zA-Z][\w.]+/,
            );
        });
    }

    async screenshot(name: string) {
        await this.check("screenshot", name, () => this.page.screenshot({
            path: path.join(output, `${this.page.viewportSize()!.width}-${name}.png`),
            fullPage: true,
            animations: "disabled",
        }));
    }

    async rowMenus() {
        // Only the action cell: opening the multi-driver badge would bubble to the truck row's navigation.
        const triggers = this.page.locator('main tbody tr td:last-child [aria-haspopup="menu"]');
        const count = await triggers.count();
        for (let index = 0; index < count; index++) {
            const before = this.page.url();
            await this.check("row-menu", `Open row ${index + 1} action menu`, async () => {
                await triggers.nth(index).click();
                await expect(this.page.getByRole("menu")).toBeVisible();
                await this.idle();
                await expect(this.page).toHaveURL(before);
                await this.pageChecks();
                // Inspect only; never select suspend/revoke/delete/other write actions.
            });
            await this.page.keyboard.press("Escape");
            if (this.page.url() !== before) {
                await this.check("navigation", "Restore list after action-menu navigation", () => this.page.goto(before));
                await this.idle();
            }
        }
    }

    async visit(route: string, name: string) {
        const loaded = await this.check("navigation", `Visit ${route}`, async () => {
            await this.page.goto(route);
            await this.idle();
            await expect(this.page.locator("main h1").first()).toBeVisible({ timeout: 30_000 });
            expect(routeOf(this.page.url()), "Unexpected redirect").toBe(route);
        });
        await this.pageChecks();
        if (loaded) {
            await this.rowMenus();
            const tabs = this.page.getByRole("tab");
            const count = await tabs.count();
            for (let index = 0; index < count; index++) {
                await this.check("tab", `Open tab ${index + 1}`, async () => {
                    await tabs.nth(index).click();
                    await expect(tabs.nth(index)).toHaveAttribute("aria-selected", "true");
                    await this.idle();
                    await this.pageChecks();
                    await this.rowMenus();
                    await this.screenshot(`${name}-tab-${index + 1}`);
                });
            }
        }
        await this.screenshot(name);
    }

    async firstRecord(kind: RecordKind, row?: Locator): Promise<string | undefined> {
        let id: string | undefined;
        await this.check("demo-data", `Derive ${kind} ID from the first list row (detail/edit coverage requires staging data)`, async () => {
            const firstRow = row ?? this.page.locator("main tbody tr").first();
            const trigger = firstRow.locator('td:last-child [aria-haspopup="menu"]');
            await expect(trigger).toBeVisible();
            await trigger.click();
            await expect(this.page.getByRole("menu")).toBeVisible();
            const detailPath = kind === "subcontractors" ? `/app/${kind}/` : `/app/${kind}/view`;
            // Trucks expose a real link. Drivers/partners use router.push in their view menu item;
            // follow that first-row action and read its URL instead of inventing/looking up an API ID.
            const link = this.page.getByRole("menu").locator(`a[href^="${detailPath}"]`).first();
            if (await link.count()) {
                const href = await link.getAttribute("href");
                id = new URL(href!, this.page.url()).searchParams.get("id") ?? undefined;
            } else {
                const name = kind === "drivers" ? drivers["drivers.action.viewProfile"] : partners["subcontractors.action.viewProfile"];
                await this.page.getByRole("menuitem", { name, exact: true }).click();
                await this.page.waitForURL((url) => kind === "subcontractors"
                    ? /^\/app\/subcontractors\/[^/]+$/.test(url.pathname)
                    : url.pathname === detailPath && !!url.searchParams.get("id"));
                await this.idle();
                const url = new URL(this.page.url());
                id = kind === "subcontractors" ? decodeURIComponent(url.pathname.split("/").pop()!) : url.searchParams.get("id") ?? undefined;
            }
            expect(id, `No ${kind} detail ID exposed by first row`).toBeTruthy();
        });
        await this.page.keyboard.press("Escape");
        return id;
    }
}

async function select(page: Page, fieldLabel: string, option: string) {
    await page.getByLabel(label(fieldLabel)).click();
    // SearchSelect virtualises long lists (provinces, years); search before selecting.
    const search = page.locator('[data-slot="search-select-panel"] input');
    if (await search.count()) await search.fill(option);
    await page.getByRole("option", { name: option, exact: true }).click();
}

async function date(page: Page, fieldLabel: string, value: Date) {
    // The shared DatePicker does not forward FormControl's id; scope its button by the real label.
    const field = page.locator("label").filter({ hasText: label(fieldLabel) }).locator("..");
    // The driver's birth-date trigger is a button with role="combobox".
    await field.locator('button[aria-haspopup="dialog"]').first().click();
    const calendar = page.locator('[data-slot="calendar"]');
    await expect(calendar).toBeVisible();
    await calendar.locator("select.rdp-years_dropdown").selectOption(String(value.getFullYear()));
    await calendar.locator("select.rdp-months_dropdown").selectOption(String(value.getMonth()));
    await calendar.locator("button[data-day]").filter({ hasText: new RegExp(`^${value.getDate()}$`) }).click();
    await expect(calendar).toBeHidden();
}

async function upload(page: Page, fieldLabel: string, buffer: Buffer) {
    // TruckFileUploader's visible label is adjacent to its hidden file input, not associated by id.
    await page.locator("label").filter({ hasText: label(fieldLabel) }).locator("..")
        .locator('input[type="file"]').setInputFiles({ name: "fleet-demo.png", mimeType: "image/png", buffer });
}

async function save(page: Page, method: string, apiPath: string, button: Locator) {
    const [response] = await Promise.all([
        page.waitForResponse((response) => response.request().method() === method && new URL(response.url()).pathname === apiPath, { timeout: 30_000 }),
        button.click(),
    ]);
    expect(response.status(), `${method} ${apiPath}`).toBeLessThan(400);
}

async function writeWalk(walk: Walkthrough) {
    const { page } = walk;
    await page.setViewportSize(viewports[0]);
    const token = randomBytes(5).toString("hex");
    const thaiLetters = "กขคฆงจฉชซฌญฎฏฐฑฒณดตถทธนบปผฝพฟภมยรลวศษสหฬอฮ";
    const plate = `${thaiLetters[randomInt(thaiLetters.length)]}${thaiLetters[randomInt(thaiLetters.length)]}-${randomInt(1000, 10000)}`;
    const nameSuffix = String(Date.now()).slice(-9).replace(/\d/g, (digit) => "๐๑๒๓๔๕๖๗๘๙"[Number(digit)]);
    const firstName = `สาธิต${nameSuffix}`;
    const lastName = `ทดสอบ${token}`;
    const fullName = `${firstName} ${lastName}`;
    const model = `รุ่นสาธิต ${token}`;
    const editedModel = `${model} ปรับปรุง`;
    let truckId: string | undefined;
    let driverId: string | undefined;
    // Valid tiny PNG supplied through the UI; Go handles the real upload/presign and files PATCH.
    const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII=", "base64");

    await walk.visit("/app/trucks/new", "write-truck-new");
    await walk.check("write-truck", `Create truck ${plate}`, async () => {
        await page.getByLabel(label(trucks["trucks.section.licensePlate"])).fill(plate);
        await select(page, trucks["trucks.section.province"], "Bangkok / กรุงเทพมหานคร");
        await page.getByLabel(label(trucks["trucks.section.vin"])).fill(`DEMO${randomBytes(7).toString("hex").slice(0, 13).toUpperCase()}`);
        await page.getByLabel(label(trucks["trucks.section.engineNumber"])).fill(token.toUpperCase());
        await select(page, trucks["trucks.section.truckStatus"], trucks["trucks.detail.status.active"]);
        await page.getByLabel(label(trucks["trucks.section.brand"])).fill("Isuzu");
        await page.getByLabel(label(trucks["trucks.section.model"])).fill(model);
        await select(page, trucks["trucks.section.year"], String(new Date().getFullYear()));
        await page.getByLabel(label(trucks["trucks.section.color"])).fill("ขาว");
        await select(page, trucks["trucks.section.truckType"], trucks["trucks.type.6wheels"]);
        await page.getByLabel(label(trucks["trucks.section.seats"])).fill("2");
        for (const key of ["trucks.photos.frontRight", "trucks.photos.frontLeft", "trucks.photos.backRight", "trucks.photos.backLeft", "trucks.documents.tax", "trucks.documents.register"] as const) {
            await upload(page, trucks[key], png);
        }
        await page.getByRole("button", { name: trucks["trucks.wizard.next"], exact: true }).click();
        await date(page, trucks["trucks.section.taxExpiry"], new Date(new Date().getFullYear() + 1, 0, 15));
        await date(page, trucks["trucks.section.regDate"], new Date(new Date().getFullYear(), 0, 15));
        await page.getByRole("button", { name: trucks["trucks.wizard.next"], exact: true }).click();
        await select(page, trucks["trucks.section.fuelType"], trucks["trucks.fuel.diesel"]);
        await page.getByLabel(label(trucks["trucks.maintenance.currentMileage"])).fill("1000");
        await page.getByLabel(label(trucks["trucks.step3.nextServiceMileage"])).fill("11000");
        await page.getByLabel(label(trucks["trucks.step3.pmIntervalKm"])).fill("10000");
        await page.getByRole("button", { name: trucks["trucks.wizard.next"], exact: true }).click();
        await save(page, "POST", "/api/go/v1/trucks", page.getByRole("button", { name: trucks["trucks.wizard.save"], exact: true }));
        await page.waitForURL((url) => url.pathname === "/app/trucks", { timeout: 60_000 });
        await walk.idle();
        await page.getByPlaceholder(trucks["trucks.filter.search"], { exact: true }).fill(plate);
        const row = page.locator("main tbody tr").filter({ hasText: plate });
        await expect(row).toHaveCount(1, { timeout: 20_000 });
        await expect(row).toContainText(model);
        await walk.screenshot("write-truck-created");
        truckId = await walk.firstRecord("trucks", row);
    });
    await walk.screenshot("write-truck-create-result");

    if (truckId) {
        const route = `/app/trucks/edit?id=${encodeURIComponent(truckId)}`;
        await walk.visit(route, "write-truck-edit");
        await walk.check("write-truck", `Edit truck ${plate}`, async () => {
            await page.getByLabel(label(trucks["trucks.section.model"])).fill(editedModel);
            await save(page, "PATCH", `/api/go/v1/trucks/${truckId}`, page.getByRole("button", { name: trucks["trucks.edit.saveChanges"], exact: true }));
            await page.waitForURL((url) => url.pathname === "/app/trucks/view");
            await walk.visit(route, "write-truck-saved");
            await expect(page.getByLabel(label(trucks["trucks.section.model"]))).toHaveValue(editedModel);
        });
    }

    await walk.visit("/app/drivers/new", "write-driver-new");
    await walk.check("write-driver", `Create driver ${fullName}`, async () => {
        await page.getByLabel(label(drivers["drivers.form.firstName"])).fill(firstName);
        await page.getByLabel(label(drivers["drivers.form.lastName"])).fill(lastName);
        await page.getByLabel(label(drivers["drivers.form.fullNameTh"])).fill(fullName);
        await page.getByLabel(label(drivers["drivers.form.mobile"])).fill(`09${randomInt(10000000, 100000000)}`);
        await date(page, drivers["drivers.form.birthDate"], new Date(new Date().getFullYear() - 30, 0, 15));
        await page.getByRole("button", { name: drivers["drivers.form.nextStep"], exact: true }).click();
        // Synthetic demo identifier with a valid Thai check digit (never use a real person's ID).
        const prefix = `1${String(randomInt(0, 100000000000)).padStart(11, "0")}`;
        const sum = [...prefix].reduce((total, digit, index) => total + Number(digit) * (13 - index), 0);
        await page.getByLabel(label(drivers["drivers.form.idNumber"])).fill(`${prefix}${(11 - sum % 11) % 10}`);
        await page.getByLabel(label(drivers["drivers.form.licenseNumber"])).fill(String(randomInt(10000000, 100000000)));
        await page.getByRole("button", { name: drivers["drivers.form.nextStep"], exact: true }).click();
        await page.getByLabel(label(drivers["drivers.form.contractDuration"])).fill("1");
        // This walkthrough creates a driver record only. The unlabelled switch is scoped by its real card title.
        const loginCard = page.locator('[data-slot="card"]').filter({ has: page.locator('[data-slot="card-title"]').filter({ hasText: drivers["drivers.form.loginAccount"] }) });
        const createLogin = loginCard.getByRole("switch");
        if (await createLogin.getAttribute("aria-checked") === "true") await createLogin.click();
        await expect(createLogin).toHaveAttribute("aria-checked", "false");
        await page.getByRole("button", { name: drivers["drivers.form.nextStep"], exact: true }).click();
        await save(page, "POST", "/api/go/v1/drivers", page.getByRole("button", { name: drivers["drivers.form.completeRegistration"], exact: true }));
        await page.waitForURL((url) => url.pathname === "/app/drivers", { timeout: 60_000 });
        await walk.idle();
        await page.getByPlaceholder(drivers["drivers.search"], { exact: true }).fill(firstName);
        const row = page.locator("main tbody tr").filter({ hasText: fullName });
        await expect(row).toHaveCount(1, { timeout: 20_000 });
        await walk.screenshot("write-driver-created");
        driverId = await walk.firstRecord("drivers", row);
    });
    await walk.screenshot("write-driver-create-result");

    if (driverId) {
        const route = `/app/drivers/edit?id=${encodeURIComponent(driverId)}`;
        await walk.visit(route, "write-driver-edit");
        await walk.check("write-driver", `Edit driver ${fullName}`, async () => {
            const mobile = `08${randomInt(10000000, 100000000)}`;
            await page.getByLabel(label(drivers["drivers.form.mobile"])).fill(mobile);
            await save(page, "PATCH", `/api/go/v1/drivers/${driverId}`, page.getByRole("button", { name: drivers["drivers.edit.saveChanges"], exact: true }));
            await page.waitForURL((url) => url.pathname === "/app/drivers/view");
            await walk.visit(route, "write-driver-saved");
            await expect(page.getByLabel(label(drivers["drivers.form.mobile"]))).toHaveValue(mobile);
        });
    }

    if (truckId && driverId) {
        await walk.visit("/app/truck-assignment", "write-assignment");
        await walk.check("write-assignment", `Assign ${fullName} to ${plate}`, async () => {
            for (const [field, placeholder, search] of [
                [assignments["assignments.selectDriver"], assignments["assignments.searchDriverPlaceholder"], firstName],
                [assignments["assignments.selectTruck"], assignments["assignments.searchTruckPlaceholder"], plate],
            ]) {
                await page.getByRole("combobox", { name: field, exact: true }).click();
                await page.getByPlaceholder(placeholder, { exact: true }).fill(search);
                const option = page.getByRole("option").filter({ hasText: search }).first();
                await expect(option).not.toHaveAttribute("aria-disabled", "true");
                await option.click();
            }
            await save(page, "POST", "/api/go/v1/truck-assignments", page.getByRole("button", { name: assignments["assignments.deploy"], exact: true }));
            await walk.visit("/app/truck-assignment", "write-assignment-saved");
            await page.getByPlaceholder(assignments["assignments.filter.search"], { exact: true }).fill(firstName);
            await expect(page.locator("main tbody tr").filter({ hasText: firstName }).filter({ hasText: plate }).first()).toBeVisible({ timeout: 20_000 });
            await walk.screenshot("write-assignment-verified");
        });
    }
}

test.describe("Fleet & partners — Thai PO demo", () => {
    test.skip(!hasTestCredentials(), "Set PLAYWRIGHT_TEST_USER_EMAIL and PLAYWRIGHT_TEST_USER_PASSWORD");
    test.describe.configure({ retries: 0 });

    test("read every fleet route on desktop/mobile; optionally create, edit and assign demo records", async ({ page: loginPage, context }, testInfo) => {
        test.setTimeout(20 * 60_000);
        const findings: Finding[] = [];
        await mkdir(output, { recursive: true });
        await loginPage.setViewportSize(viewports[0]);
        let page: Page | undefined;
        let walk: Walkthrough | undefined;
        try {
            await login(loginPage);
            // auth.ts installs a PAGE-scoped init script forcing English. Use a new authenticated
            // page so that script cannot race our Thai script on subsequent full navigations.
            await loginPage.evaluate(() => localStorage.setItem("language", "th"));
            page = await context.newPage();
            await page.addInitScript(() => localStorage.setItem("language", "th"));
            walk = new Walkthrough(page, findings);
            await walk.observeFormErrors();
            await loginPage.close();

            for (const viewport of viewports) {
                await page.setViewportSize(viewport);
                await walk.visit("/app/trucks", "trucks-list");
                const truckId = await walk.firstRecord("trucks");
                await walk.visit("/app/trucks/new", "trucks-new");
                if (truckId) {
                    const id = encodeURIComponent(truckId);
                    for (const [route, name] of [
                        [`/app/trucks/view?id=${id}`, "trucks-view"],
                        [`/app/trucks/edit?id=${id}`, "trucks-edit"],
                        [`/app/trucks/renew?id=${id}&type=tax`, "trucks-renew-tax"],
                        [`/app/trucks/renew?id=${id}&type=insurance`, "trucks-renew-insurance"],
                        [`/app/trucks/maintenance?id=${id}`, "trucks-maintenance"],
                    ]) await walk.visit(route, name);
                } else {
                    walk.record("coverage", "No first truck row: view, edit, both renewal types and truck maintenance could not be visited", "/app/trucks");
                }
                await walk.visit("/app/truck-assignment", "truck-assignment");
                await walk.visit("/app/drivers", "drivers-list");
                const driverId = await walk.firstRecord("drivers");
                await walk.visit("/app/drivers/new", "drivers-new");
                if (driverId) {
                    await walk.visit(`/app/drivers/view?id=${encodeURIComponent(driverId)}`, "drivers-view");
                    await walk.visit(`/app/drivers/edit?id=${encodeURIComponent(driverId)}`, "drivers-edit");
                } else {
                    walk.record("coverage", "No first driver row: view and edit could not be visited", "/app/drivers");
                }
                await walk.visit("/app/renewals", "renewals");
                await walk.visit("/app/maintenance", "maintenance");
                await walk.visit("/app/subcontractors", "subcontractors-list");
                const partnerId = await walk.firstRecord("subcontractors");
                await walk.visit("/app/subcontractors/new", "subcontractors-new");
                if (partnerId) {
                    await walk.visit(`/app/subcontractors/${encodeURIComponent(partnerId)}`, "subcontractors-detail");
                } else {
                    walk.record("coverage", "No first partner row: detail could not be visited", "/app/subcontractors");
                }
            }
            if (process.env.FLEET_DEMO_WRITE === "1") await writeWalk(walk);
        } catch (error) {
            if (walk) walk.record("walkthrough", errorText(error));
            else findings.push({ route: "/login", width: 1280, kind: "authentication", detail: errorText(error) });
        } finally {
            if (page) await page.close();
            const file = path.join(output, "findings.json");
            await writeFile(file, JSON.stringify(findings, null, 2) + "\n");
            await testInfo.attach("fleet-demo-findings", { path: file, contentType: "application/json" });
        }
        expect(findings, `Fleet demo findings:\n${findings.map((finding) =>
            `[${finding.width}px] ${finding.route} ${finding.kind}: ${finding.detail}`).join("\n")}`).toEqual([]);
    });
});
