import { zodResolver } from "@hookform/resolvers/zod";
import type { Resolver, UseFormReturn } from "react-hook-form";
import { createInvalidHandler } from "@/lib/formInvalidHandler";
import { driverSchema, type Driver } from "@/validate/driverSchema";

/** Keep locale keys in RHF so visible validation messages follow language changes. */
export function driverFormResolver(): Resolver<Driver> {
    const resolve = zodResolver(driverSchema) as Resolver<Driver>;
    return async (values, context, options) => {
        const result = await resolve(values, context, options);
        const normalize = (errors: Record<string, unknown>) => {
            if (typeof errors.message === "string") {
                if (!errors.message.startsWith("drivers.validation.")) errors.message = "drivers.validation.invalid";
                return;
            }
            for (const [key, error] of Object.entries(errors)) {
                if (key !== "ref" && error && typeof error === "object") normalize(error as Record<string, unknown>);
            }
        };
        normalize(result.errors);
        return result;
    };
}

/** Translate the first field's key in the shared invalid-form toast as well as inline. */
export function driverInvalidHandler(form: UseFormReturn<Driver>, t: (key: string, params?: Record<string, string | number>) => string) {
    return createInvalidHandler(form, (key, params) => t(key,
        params && typeof params.field === "string" ? { ...params, field: t(params.field) } : params,
    ));
}
