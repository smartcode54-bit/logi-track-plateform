"use client";

import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import Link from "next/link";
import { useState } from "react";
import { useLanguage } from "@/context/language";
import { toast } from "sonner";
import { Loader2, Mail } from "lucide-react";
import { collection, addDoc, serverTimestamp } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";

export default function WaitlistPage() {
  const [email, setEmail] = useState("");
  const [loading, setLoading] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const { t } = useLanguage();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);

    try {
      if (!email) {
        toast.error("Please enter your email address");
        setLoading(false);
        return;
      }

      await addDoc(collection(db, COLLECTIONS.WAITLIST), {
        email,
        createdAt: serverTimestamp(),
      });

      setSubmitted(true);
      toast.success("Successfully joined the waitlist!");
    } catch (error: any) {
      console.error("Error joining waitlist:", error);
      toast.error(error.message || "Failed to join waitlist. Please try again.");
    } finally {
      setLoading(false);
    }
  };

  if (submitted) {
    return (
      <Card className="w-full max-w-md text-center">
        <CardHeader>
          <div className="mx-auto w-12 h-12 bg-green-100 dark:bg-green-900 rounded-full flex items-center justify-center mb-4">
            <Mail className="w-6 h-6 text-green-600 dark:text-green-300" />
          </div>
          <CardTitle className="text-2xl">You're on the list!</CardTitle>
          <CardDescription>
            Thank you for your interest. We'll verify your information and contact you at <strong>{email}</strong> when your account is ready.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button asChild className="w-full">
            <Link href="/login">Back to Login</Link>
          </Button>
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className="w-full max-w-md shadow-lg border-muted/20">
      <CardHeader className="space-y-1">
        <CardTitle className="text-2xl font-bold">Join Waitlist</CardTitle>
        <CardDescription>
          Registration is currently by invitation only. Join the waitlist to request access.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="email">{t("auth.email") || "Email"}</Label>
            <Input
              id="email"
              type="email"
              placeholder="name@company.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              className="bg-muted/5 border-muted-foreground/20 focus-visible:ring-primary"
            />
          </div>

          <Button type="submit" className="w-full bg-foreground text-background hover:bg-foreground/90 font-semibold" disabled={loading}>
            {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            Request Access
          </Button>
        </form>
      </CardContent>
      <CardAction>
        <div className="flex justify-center p-6 pt-0">
          <Button variant="link" asChild>
            <Link className="text-sm hover:underline text-muted-foreground" href="/login">
              Already have an account? Login
            </Link>
          </Button>
        </div>
      </CardAction>
    </Card>
  );
}
