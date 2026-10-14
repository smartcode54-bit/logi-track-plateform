import { cn } from "@/lib/utils";

/**
 * The title of a sign-in page's card, as a real heading (UI3E): the page's one h1 on `/login`, `/waitlist`,
 * `/forgot-password` and `/reset-password`; an h2 where the card sits under another page's h1 (the landing `/`).
 * `leading-tight`, not `leading-none`, so Thai marks above and below the line are not clipped. Other heading props
 * (a `ref` and `tabIndex={-1}` for a heading that takes the focus) pass through.
 */
export function AuthCardTitle({ as: Heading = "h1", className, children, ...props }: { as?: "h1" | "h2" } & React.ComponentProps<"h1">) {
    return (
        <Heading data-slot="card-title" className={cn("text-2xl leading-tight font-semibold tracking-tight outline-none", className)} {...props}>
            {children}
        </Heading>
    );
}
