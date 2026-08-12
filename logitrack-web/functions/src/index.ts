import * as admin from "firebase-admin";
import { setGlobalOptions } from "firebase-functions/v2/options";

// Initialize Firebase Admin
admin.initializeApp();

// Set global options for all functions
setGlobalOptions({
  region: "asia-southeast1", // Singapore region (closest to Thailand)
  maxInstances: 10,
  enforceAppCheck: true, // Reject requests without a valid App Check token
});

// Export functions from separate files
export * from "./auth";
export * from "./authSessions";
export * from "./users";
export * from "./triggers";
export * from "./distances";
export * from "./chat";
export * from "./cartrack";
export * from "./holidays";
export * from "./securityEvents";
export * from "./bangchakOilPrice";
export * from "./monthlyFuelPriceSnapshot";
export * from "./syncBangchakFuelMonthlySnapshot";
export * from "./tripBillingOnDelivered";
export * from "./backfillCustomerLinks";
export * from "./backfillTripJobCategory";
export * from "./billingRoundMigration";
export * from "./tasks";
export * from "./multiDeliveryTrips";
export * from "./standbyBilling";
export * from "./backfillTripTruckData";
export * from "./backfillTruckType";
export * from "./driverCompensation";
