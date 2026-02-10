import { initializeApp, getApps } from "firebase/app";
import { getAuth, Auth } from "firebase/auth";
import { getStorage, FirebaseStorage } from "firebase/storage";
import { getFirestore, Firestore } from "firebase/firestore";

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

if (missingKeys.length > 0) {
  const envVarNames = missingKeys.map(key => {
    const envKey = key
      .replace(/([A-Z])/g, '_$1')
      .toUpperCase()
      .replace(/^_/, '');
    return `NEXT_PUBLIC_${envKey}`;
  }).join(', ');

  throw new Error(
    `❌ Firebase Client Configuration Error:\n` +
    `Missing required environment variables: ${envVarNames}\n\n` +
    `Please check your .env.local file and ensure all Firebase client configuration values are set.\n` +
    `After updating .env.local, restart your Next.js dev server.`
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
if (process.env.NODE_ENV === 'development') {
  console.log('Firebase client config:', "OK");
}

// Initialize Firebase
const currentApps = getApps();
let auth: Auth;
let storage: FirebaseStorage;
let db: Firestore;

if (!currentApps.length) {
  try {
    const app = initializeApp(firebaseConfig);
    auth = getAuth(app);
    storage = getStorage(app);
    db = getFirestore(app);
    
    if (process.env.NODE_ENV === 'development') {
      console.log('✅ Firebase initialized successfully');
    }
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

export { auth, storage, db };