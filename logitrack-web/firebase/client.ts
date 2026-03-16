import { initializeApp, getApps } from "firebase/app";
import {
  initializeAppCheck,
  ReCaptchaV3Provider,
  ReCaptchaEnterpriseProvider,
} from "firebase/app-check";
import { getAuth, Auth } from "firebase/auth";
import { getStorage, FirebaseStorage } from "firebase/storage";
import { getFirestore, Firestore } from "firebase/firestore";
import { getFunctions } from "firebase/functions";

// Validate required environment variables
const requiredConfig = {
  apiKey: process.env.NEXT_PUBLIC_FIREBASE_API_KEY,
  authDomain: process.env.NEXT_PUBLIC_FIREBASE_AUTH_DOMAIN,
  projectId: process.env.NEXT_PUBLIC_FIREBASE_PROJECT_ID,
  storageBucket: process.env.NEXT_PUBLIC_FIREBASE_STORAGE_BUCKET,
  messagingSenderId: process.env.NEXT_PUBLIC_FIREBASE_MESSAGING_SENDER_ID,
  appId: process.env.NEXT_PUBLIC_FIREBASE_APP_ID,
};

// Check for missing values
const missingKeys = Object.entries(requiredConfig)
  .filter(([_, value]) => !value)
  .map(([key]) => key);

const keyToEnvVar: Record<string, string> = {
  apiKey: "NEXT_PUBLIC_FIREBASE_API_KEY",
  authDomain: "NEXT_PUBLIC_FIREBASE_AUTH_DOMAIN",
  projectId: "NEXT_PUBLIC_FIREBASE_PROJECT_ID",
  storageBucket: "NEXT_PUBLIC_FIREBASE_STORAGE_BUCKET",
  messagingSenderId: "NEXT_PUBLIC_FIREBASE_MESSAGING_SENDER_ID",
  appId: "NEXT_PUBLIC_FIREBASE_APP_ID",
};

if (missingKeys.length > 0) {
  const envVarNames = missingKeys.map(key => keyToEnvVar[key] ?? `NEXT_PUBLIC_FIREBASE_${key.toUpperCase()}`).join(", ");

  throw new Error(
    `❌ Firebase Client Configuration Error:\n` +
    `Missing required environment variables: ${envVarNames}\n\n` +
    `Please check your .env.local (development) or .env.production / .env.prod (production build) and ensure all Firebase client configuration values are set.\n` +
    `After updating, restart your Next.js dev server or re-run the build.`
  );
}

export const firebaseConfig = {
  apiKey: requiredConfig.apiKey!,
  authDomain: requiredConfig.authDomain!,
  projectId: requiredConfig.projectId!,
  storageBucket: requiredConfig.storageBucket!,
  messagingSenderId: requiredConfig.messagingSenderId!,
  appId: requiredConfig.appId!,
  measurementId: process.env.NEXT_PUBLIC_FIREBASE_MEASUREMENT_ID,
};

// Debug: Log config in development (without sensitive data)


// Initialize Firebase
const currentApps = getApps();
let auth: Auth;
let storage: FirebaseStorage;
let db: Firestore;

if (!currentApps.length) {
  try {
    const app = initializeApp(firebaseConfig);
    // App Check (reCAPTCHA) runs only in the browser; skip during SSR to avoid crash.
    if (typeof window !== "undefined") {
      const rawKey = process.env.NEXT_PUBLIC_APP_CHECK_RECAPTCHA_SITE_KEY?.trim() ?? "";
      // Sanitize: reCAPTCHA site keys only use A-Za-z0-9_-; strip any stray chars (spaces, │, etc.)
      const siteKey = rawKey.replace(/[^A-Za-z0-9_-]/g, "") || undefined;
      const useEnterprise =
        process.env.NEXT_PUBLIC_APP_CHECK_USE_ENTERPRISE === "true";
      
      const isLocalhost =
        window.location.hostname === "localhost" ||
        window.location.hostname === "127.0.0.1";
      const debugEnv = process.env.NEXT_PUBLIC_APP_CHECK_DEBUG_TOKEN;

      // Initialize App Check if we have a siteKey OR if we are in a debug environment (localhost/debug token)
      if (siteKey || isLocalhost || debugEnv) {
        if (isLocalhost || debugEnv) {
          (self as unknown as { FIREBASE_APPCHECK_DEBUG_TOKEN?: boolean | string })
            .FIREBASE_APPCHECK_DEBUG_TOKEN = debugEnv || true;
        }

        // ReCaptcha providers require a string siteKey. 
        // If we're in debug mode but siteKey is missing, we use a placeholder.
        const effectiveSiteKey = siteKey || "debug-placeholder-key";

        initializeAppCheck(app, {
          provider: useEnterprise
            ? new ReCaptchaEnterpriseProvider(effectiveSiteKey)
            : new ReCaptchaV3Provider(effectiveSiteKey),
          isTokenAutoRefreshEnabled: true,
        });
      }
    }
    auth = getAuth(app);
    storage = getStorage(app);
    db = getFirestore(app);


  } catch (error: any) {
    console.error('❌ Firebase Initialization Error:', error);
    throw new Error(
      `❌ Firebase Client Initialization Failed:\n${error.message}\n\n` +
      `Common causes:\n` +
      `1. Invalid API key - Check Firebase Console > Project Settings > General\n` +
      `2. Authentication not enabled - Enable in Firebase Console > Authentication\n` +
      `3. Wrong project ID - Verify in Firebase Console\n` +
      `4. Environment variables not loaded - Restart Next.js dev server\n\n` +
      `Current config:\n` +
      `- Project ID: ${firebaseConfig.projectId}\n` +
      `- Auth Domain: ${firebaseConfig.authDomain}\n` +
      `- API Key: ${firebaseConfig.apiKey ? 'Set' : 'Missing'}`
    );
  }
} else {
  const app = currentApps[0];
  auth = getAuth(app);
  storage = getStorage(app);
  db = getFirestore(app);
}

const functions = getFunctions(getApps()[0], 'asia-southeast1');
// Connect to emulator if in dev mode? 
// if (process.env.NODE_ENV === 'development') {
//   connectFunctionsEmulator(functions, "localhost", 5001);
// }

export { auth, storage, db, functions };