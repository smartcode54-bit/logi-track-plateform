import * as z from "zod";

// Helper for optional numeric fields to handle empty strings
const optionalNumber = (min: number, max: number, label: string) =>
    z.union([z.string(), z.number(), z.undefined()])
        .transform((val) => {
            if (val === "" || val === null || val === undefined) return undefined;
            if (typeof val === "string") {
                const num = parseFloat(val);
                return isNaN(num) ? undefined : num;
            }
            return typeof val === "number" ? val : undefined;
        })
        .pipe(
            z.number()
                .min(min, `${label} must be at least ${min}`)
                .max(max, `${label} cannot exceed ${max}`)
                .refine((val) => val >= 0, `${label} cannot be negative`)
                .optional()
        );

export const maintenanceSchema = z.object({
    truckId: z.string().min(1, "Truck ID is required"),

    // Type: PM (Preventive) or CM (Corrective)
    type: z.enum(["PM", "CM"]),

    // Service Type: 
    // For PM: "Oil Change", "Tire Rotation", etc.
    // For CM: Free text description of the issue
    serviceType: z.string().min(1, "Service type or issue description is required"),

    // Dates
    startDate: z.string().min(1, "Start date is required"), // ISO Date
    endDate: z.string().optional(), // ISO Date, optional if in progress

    // Status
    status: z.enum(["in_progress", "completed", "cancelled"]).default("in_progress"),

    // Costs (Split Labor/Parts)
    costLabor: optionalNumber(0, 1000000, "Labor Cost"),
    costParts: optionalNumber(0, 1000000, "Parts Cost"),
    totalCost: optionalNumber(0, 2000000, "Total Cost"), // Calculated or explicit

    provider: z.string().optional(), // Garage name
    paymentMethod: z.enum(["cash", "credit_card", "billing", "transfer", "insurance_claim"]).optional(),

    currentMileage: optionalNumber(0, 2000000, "Current Mileage"),
    nextServiceMileage: optionalNumber(0, 2000000, "Next Service Mileage"), // For PM

    // Media
    images: z.array(z.string()).optional(), // URLs to receipts/photos
    receipts: z.array(z.string()).optional(), // Specific receipt URLs

    notes: z.string().optional(),

    // Metadata
    createdAt: z.string().optional(),
    updatedAt: z.string().optional(),
    createdBy: z.string().optional(),
});

export type MaintenanceFormValues = z.input<typeof maintenanceSchema>;
export type MaintenanceData = z.infer<typeof maintenanceSchema> & { id: string };

export const maintenanceDefaultValues: MaintenanceFormValues = {
    truckId: "",
    type: "PM",
    serviceType: "",
    startDate: new Date().toISOString().split('T')[0], // Today
    endDate: "",
    status: "in_progress",
    totalCost: undefined,
    provider: "",
    paymentMethod: "cash",
    currentMileage: undefined,
    nextServiceMileage: undefined,
    images: [],
    receipts: [],
    notes: "",
};
