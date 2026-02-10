// Import the functions you need from the SDKs you need
import admin from "firebase-admin";
import { getFirestore, Firestore } from "firebase-admin/firestore";
import { getApps} from "firebase-admin/app";
import { getAuth, Auth } from "firebase-admin/auth";
import { getStorage, Storage } from "firebase-admin/storage";

// Validate required environment variables for server-side Firebase Admin
if (!process.env.FIREBASE_PRIVATE_KEY) {
  console.error(
    "❌ Firebase Admin SDK Configuration Error:\n" +
    "Missing required environment variable: FIREBASE_PRIVATE_KEY\n\n" +
    "Please add the following to your .env.local file:\n" +
    "- FIREBASE_PRIVATE_KEY (from Firebase service account JSON)\n" +
    "- FIREBASE_CLIENT_EMAIL\n" +
    "- FIREBASE_PRIVATE_KEY_ID\n" +
    "- FIREBASE_CLIENT_ID\n\n" +
    "Get these values from: Firebase Console > Project Settings > Service Accounts"
  );
}

const serviceAccount = {
  type: "service_account",
  project_id: process.env.FIREBASE_PROJECT_ID || "logi-track-wrt-dev",
  private_key_id: process.env.FIREBASE_PRIVATE_KEY_ID,
  private_key: process.env.FIREBASE_PRIVATE_KEY?.replace(/\\n/g, '\n'), // Handle escaped newlines
  client_email: process.env.FIREBASE_CLIENT_EMAIL,
  client_id: process.env.FIREBASE_CLIENT_ID,
  auth_uri: "https://accounts.google.com/o/oauth2/auth",
  token_uri: "https://oauth2.googleapis.com/token",
  auth_provider_x509_cert_url: "https://www.googleapis.com/oauth2/v1/certs",
  client_x509_cert_url: process.env.FIREBASE_CLIENT_X509_CERT_URL || 
    `https://www.googleapis.com/robot/v1/metadata/x509/firebase-adminsdk-fbsvc%40${process.env.FIREBASE_PROJECT_ID || "logi-track-wrt-dev"}.iam.gserviceaccount.com`,
  universe_domain: "googleapis.com",
};

let firestore: Firestore;
let firestoreTrucks: Firestore; // Separate instance for "trucks" database
let auth: Auth;
let storage: Storage;
const currentApps = getApps();

if (!currentApps.length) {
  try {
    const app = admin.initializeApp({
      credential: admin.credential.cert(serviceAccount as admin.ServiceAccount),
      projectId: serviceAccount.project_id,
      storageBucket: `${serviceAccount.project_id}.firebasestorage.app`,
    });
    // Default database
    firestore = getFirestore(app);
    // "trucks" database
    firestoreTrucks = getFirestore(app, "trucks");
    auth = getAuth(app);
    storage = getStorage(app);
    
    if (process.env.NODE_ENV === 'development') {
      console.log('✅ Firebase Admin SDK initialized successfully');
      console.log('✅ Firestore databases initialized: (default) and (trucks)');
    }
  } catch (error: any) {
    console.error('❌ Firebase Admin SDK Initialization Error:', error);
    throw new Error(
      `❌ Firebase Admin SDK Initialization Failed:\n${error.message}\n\n` +
      "Please check your .env.local file and ensure all Firebase Admin credentials are correct.\n" +
      "Make sure FIREBASE_PRIVATE_KEY is properly formatted with \\n for newlines."
    );
  }
} else {
  const app = currentApps[0];
  firestore = getFirestore(app);
  firestoreTrucks = getFirestore(app, "trucks");
  auth = getAuth(app);
  storage = getStorage(app);
}

export { firestore, firestoreTrucks, auth, storage };
