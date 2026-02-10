import * as admin from "firebase-admin";
import { setGlobalOptions } from "firebase-functions/v2/options";

// Initialize Firebase Admin
admin.initializeApp();

// Set global options for all functions
setGlobalOptions({
  region: "asia-southeast1", // Singapore region (closest to Thailand)
  maxInstances: 10,
});

// Export functions from separate files
export * from "./auth";
export * from "./users";
export * from "./triggers";
