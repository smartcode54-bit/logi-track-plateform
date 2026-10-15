import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Alert } from "@/components/ui/alert";
import { badgeVariants, type BadgeProps } from "@/components/ui/badge";
import type { TruckStatus } from "@/features/trucks/api/trucks";
import type { DriverStatus } from "@/features/drivers/api/drivers";
import type { TaskStatus } from "@/features/tasks/api/tasks.go";
import type { CarrierStatus } from "@/features/subcontractors/api/subcontractors";
import { DRIVER_STATUS_ENUM } from "@/validate/driverSchema";
import {
    carrierStatusTone, driverStatusTone, taskStatusTone, truckStatusTone,
    toneBadgeVariant, toneTextClass, type StatusTone,
} from "./statusPresentation";

const trucks: Record<TruckStatus, StatusTone> = {
    active: "success", inactive: "neutral", maintenance: "warning", insurance_claim: "destructive", sold: "neutral",
};
const drivers: Record<DriverStatus, StatusTone> = { active: "success", inactive: "neutral", on_duty: "success" };
const tasks: Record<TaskStatus, StatusTone> = {
    pending: "neutral", assigned: "primary", checked_in: "primary", in_transit: "warning", completed: "success", cancelled: "neutral",
};
const carriers: Record<CarrierStatus, StatusTone> = { active: "success", pending: "warning", suspended: "neutral" };

describe("shared status presentation", () => {
    for (const [name, mapper, statuses] of [
        ["truck", truckStatusTone, trucks], ["driver", driverStatusTone, drivers],
        ["task", taskStatusTone, tasks], ["carrier", carrierStatusTone, carriers],
    ] as const) {
        it.each(Object.entries(statuses))(`${name} %s maps to %s`, (status, tone) => {
            expect(mapper(status)).toBe(tone);
        });
        it.each(["unknown", "constructor", "toString", "", null, undefined])(`${name} unknown %s is neutral`, (status) => {
            expect(mapper(status)).toBe("neutral");
        });
    }

    it.each([
        ["insurance-claim", "destructive"], ["insurance claim", "destructive"],
        ["in-transit", "warning"], ["Available", "success"], ["Active", "success"],
    ] as const)("keeps the truck form/import/legacy spelling %s", (status, tone) => {
        expect(truckStatusTone(status)).toBe(tone);
    });

    it.each(DRIVER_STATUS_ENUM)("keeps the driver form spelling %s", (status) => {
        expect(driverStatusTone(status)).toBe(status === "Inactive" ? "neutral" : "success");
    });

    it.each([
        ["success", "success", "text-success"], ["warning", "warning", "text-warning"],
        ["destructive", "destructive", "text-destructive"], ["primary", "default", "text-primary"],
        ["neutral", "secondary", "text-muted-foreground"],
    ] as const)("%s exposes a Badge variant and text class", (tone, variant, textClass) => {
        const actual: BadgeProps["variant"] = toneBadgeVariant(tone);
        expect(actual).toBe(variant);
        expect(toneTextClass(tone)).toBe(textClass);
    });

    it.each(["success", "warning"] as const)("%s primitives use semantic tokens", (tone) => {
        const badge = badgeVariants({ variant: tone });
        expect(badge).toContain(`bg-${tone} text-${tone}-foreground`);
        expect(badge).toContain("rounded-md");
        expect(badge).toContain("focus-visible:ring-[3px]");
        const alert = renderToStaticMarkup(createElement(Alert, { variant: tone }));
        expect(alert).toContain('role="alert"');
        expect(alert).toContain(`bg-card border-${tone}/50 text-${tone}`);
        expect(alert).toContain(`[&amp;&gt;svg]:text-${tone}`);
    });
});
