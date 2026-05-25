import { z } from "zod";

/**
 * Zod schema for a company document stored in Firestore `companies` collection.
 * Supports both OWNER (the logistics operator itself) and SUBCONTRACTOR tenants.
 */
export const CompanySchema = z.object({
  /** Thai company name (required) */
  nameTh: z.string().min(1, "ต้องระบุชื่อบริษัท"),
  /** English company name (optional) */
  nameEn: z.string().optional(),
  /** Thai Revenue Dept. 13-digit Tax ID */
  taxId: z.string().length(13, "Tax ID ต้องมี 13 หลัก"),
  /** Whether this is the head office or a branch */
  branchType: z.enum(["headquarters", "branch"]),
  /** 5-digit branch number (e.g. "00001"), required when branchType = "branch" */
  branchNumber: z.string().optional(),
  /** Full registered address (Thai) */
  address: z.string().min(1, "ต้องระบุที่อยู่"),
  /** Contact phone number */
  phone: z.string().optional(),
  /** Contact email */
  email: z.string().email("รูปแบบอีเมลไม่ถูกต้อง").optional().or(z.literal("")),
  /** Firebase Storage download URL for company logo */
  logoUrl: z.string().url().optional().or(z.literal("")),
  /** Firebase Storage download URL for official company stamp / seal */
  stampUrl: z.string().url().optional().or(z.literal("")),
  /** Firebase Storage download URL for authorized signatory signature image */
  signatureUrl: z.string().url().optional().or(z.literal("")),
  /** Authorized signatory name (printed under signature in PDF) */
  signatoryName: z.string().optional(),
  /** Bank name for payment details on invoices */
  bankName: z.string().optional(),
  /** Bank account number */
  accountNumber: z.string().optional(),
  /** Bank account holder name */
  accountName: z.string().optional(),
  /** Withholding tax % (e.g. 3 = 3%). Default 3. */
  withholdingTaxRate: z.number().min(0).max(100).default(3),
  /**
   * Company type:
   * - "owner"         = the logistics operator who owns/operates the system
   * - "subcontractor" = a partner company that uses the system under the owner
   */
  companyType: z.enum(["owner", "subcontractor"]),
  /** Whether this company is active. Inactive companies cannot log in. */
  isActive: z.boolean().default(true),
  /** Max trucks allowed for subcontractor (undefined = unlimited for owner) */
  maxTrucks: z.number().int().positive().optional(),
  /** Max drivers allowed for subcontractor */
  maxDrivers: z.number().int().positive().optional(),
  createdAt: z.any().optional(),
  updatedAt: z.any().optional(),
});

export type Company = z.infer<typeof CompanySchema>;

/** Zod schema for form validation (strips server-timestamp fields) */
export const CompanyFormSchema = CompanySchema.omit({ createdAt: true, updatedAt: true });

/** Form values (without server timestamps) */
export type CompanyFormValues = z.infer<typeof CompanyFormSchema>;
