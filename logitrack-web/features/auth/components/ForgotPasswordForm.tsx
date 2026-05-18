"use client";

import { useForgotPassword } from "../hooks/useForgotPassword";
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
import { ArrowLeftIcon } from "lucide-react";

export default function ForgotPasswordForm() {
  const { email, setEmail, loading, message, handleSubmit } = useForgotPassword();

  return (
    <Card className="w-full max-w-md">
      <CardHeader>
        <CardTitle>Forgot Password</CardTitle>
        <CardDescription>
          Enter your email address and we&apos;ll send you a link to reset your password
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit}>
          <div className="flex flex-col gap-6">
            <div className="grid gap-2">
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                placeholder="m@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
              />
            </div>
            {message && (
              <div className={`text-sm ${message.includes("error") || message.includes("not registered") ? "text-red-500" : "text-green-500"}`}>
                {message}
              </div>
            )}
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? "Sending..." : "Send Reset Link"}
            </Button>
          </div>
        </form>
      </CardContent>
      <CardAction>
        <div className="flex justify-center">
          <Button variant="link" asChild>
            <Link className="text-sm hover:underline flex items-center gap-1" href="/login">
              <ArrowLeftIcon className="w-4 h-4" /> Back to Login
            </Link>
          </Button>
        </div>
      </CardAction>
    </Card>
  );
}
