import { z } from "zod";

export const HOLIDAY_TYPE_ENUM = ["PUBLIC", "COMPANY", "OTHER"] as const;
export const HOLIDAY_STATUS_ENUM = ["DRAFT", "PUBLISHED", "ARCHIVED"] as const;

export const holidaySchema = z.object({
    id: z.string().optional(),
    name: z.string().min(1, "Holiday name is required"),
    date: z.coerce.date(),
    type: z.enum(HOLIDAY_TYPE_ENUM).default("PUBLIC"),
    status: z.enum(HOLIDAY_STATUS_ENUM).default("DRAFT"),
    description: z.string().optional(),
    isRecurring: z.boolean().default(false),
    createdAt: z.coerce.date().optional(),
    updatedAt: z.coerce.date().optional(),
});

export type Holiday = z.infer<typeof holidaySchema>;
export type HolidayStatus = (typeof HOLIDAY_STATUS_ENUM)[number];
