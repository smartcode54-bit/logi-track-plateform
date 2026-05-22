import { z } from "zod";

/** ID type config for a customer's driver identification (e.g. SPX: appId, workId) */
export const customerSchema = z.object({
    id: z.string().optional(),

    /** Unique code used in driver.customerDriverIds key (e.g. "SPX") */
    code: z.string().min(1, "Customer code is required"),
    /** Display name */
    name: z.string().min(1, "Customer name is required"),
    /** Optional description */
    description: z.string().optional(),
    logoUrl: z.string().optional(),

    /** Driver ID types this customer requires – defines keys in driver.customerDriverIds[customerCode] */
    driverIdTypes: z.array(z.object({
        key: z.string().min(1),
        label: z.string().min(1),
    })).default([]),

    // Billing / Invoice fields (added for invoice generation)
    address: z.string().optional(),
    taxId: z.string().optional(),
    branchType: z.enum(["สำนักงานใหญ่", "สาขา"]).optional(),
    branchNumber: z.string().optional(),

    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
});

export type Customer = z.infer<typeof customerSchema>;
