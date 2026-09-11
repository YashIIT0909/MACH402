import * as React from "react";

import { cn } from "@/lib/utils";

/**
 * Every input on this site takes a machine value — an account id, a tinybar
 * amount, a URL — so the monospace face is the default rather than an override.
 */
function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        "h-11 w-full min-w-0 border border-input bg-foreground/[0.02] px-3 py-1 font-mono text-sm",
        "placeholder:text-muted-foreground/60 transition-colors outline-none",
        "hover:border-foreground/25 focus-visible:border-accent",
        "disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}

export { Input };
