import type { ReactNode } from "react";

/** Native disclosure affordance with a 44px phone target. */
export function DisclosureSummary({ children }: { children: ReactNode }) {
    return <summary className="cursor-pointer max-md:min-h-11 max-md:py-3">{children}</summary>;
}
