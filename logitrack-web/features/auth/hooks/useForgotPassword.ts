import { useState } from "react";
import { auth } from "@/firebase/client";
import { sendPasswordResetEmail } from "firebase/auth";

export function useForgotPassword() {
  const [email, setEmail] = useState("");
  const [loading, setLoading] = useState(false);
  const [message, setMessage] = useState("");

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setMessage("");

    try {
      await sendPasswordResetEmail(auth, email);
      setMessage("Password reset email sent! Please check your inbox.");
    } catch (error: any) {
      console.error("Error sending password reset email:", error);
      if (error.code === "auth/user-not-found") {
         setMessage("This email address is not registered.");
      } else {
         setMessage("An error occurred. Please try again.");
      }
    } finally {
      setLoading(false);
    }
  };

  return { email, setEmail, loading, message, handleSubmit };
}
