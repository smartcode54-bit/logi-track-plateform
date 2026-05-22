/** Provider (our company) info printed on invoices and receipts. */
export const BILLING_PROVIDER = {
  name: "บริษัท โลจิแทรค จำกัด",
  address: "กรุณาอัปเดตที่อยู่บริษัท",
  taxId: "กรุณาอัปเดต Tax ID",
  bankName: "",
  accountNumber: "",
  accountName: "",
} as const;

export const WITHHOLDING_TAX_RATE = 0.01; // 1%
