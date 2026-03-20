import { useState, useEffect } from "react";
import { useLanguage } from "@/context/language";
import { useAuth } from "@/context/auth";
import { useRouter } from "next/navigation";
import { toast } from "sonner";

export function useLogin() {
  const { t } = useLanguage();
  const auth = useAuth();
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!auth?.loading && auth?.currentUser) {
      const isAdmin = auth.customClaims?.admin === true;
      router.replace(isAdmin ? "/admin/dashboard" : "/");
    }
  }, [auth?.loading, auth?.currentUser, auth?.customClaims, router]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);

    try {
      if (!auth) {
        throw new Error("Auth context not initialized");
      }
      await auth.login(email, password);
      router.push("/admin/dashboard");
      toast.success("Logged in successfully");
    } catch (error: any) {
      console.error("Login error:", error);
      toast.error(error.message || "Failed to login");
    } finally {
      setLoading(false);
    }
  };

  return {
    email,
    setEmail,
    password,
    setPassword,
    loading,
    handleSubmit,
    t
  };
}
