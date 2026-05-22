import { z } from "zod";

export const invoiceLineItemSchema = z.object({
  /** Sequence number (1, 2, 3...) */
  no: z.number(),
  vehicleClass: z.string(),
  /** Route description e.g. "CBI-KKN ขาเดียว" */
  description: z.string(),
  /** Trip ID for reference (multi-drop shares same tripId) */
  tripId: z.string().optional(),
  quantity: z.number(),
  unitPriceThb: z.number(),
  totalThb: z.number(),
});

export const invoiceSchema = z.object({
  /** INV-YYYYMM-XXXX */
  invoiceNumber: z.string(),
  /** YYYY-MM-01 (first day of billing period) */
  periodYearMonth: z.string(),
  issuedAt: z.coerce.date(),

  provider: z.object({
    name: z.string(),
    address: z.string(),
    taxId: z.string(),
    bankName: z.string().optional(),
    accountNumber: z.string().optional(),
    accountName: z.string().optional(),
  }),

  customer: z.object({
    id: z.string(),
    name: z.string(),
    address: z.string().optional(),
    taxId: z.string().optional(),
    branchType: z.enum(["สำนักงานใหญ่", "สาขา"]).optional(),
    branchNumber: z.string().optional(),
  }),

  lineItems: z.array(invoiceLineItemSchema),

  grandTotalThb: z.number(),
  withholdingTaxThb: z.number(),
  totalNetThb: z.number(),
});

export type InvoiceLineItem = z.infer<typeof invoiceLineItemSchema>;
export type Invoice = z.infer<typeof invoiceSchema>;
