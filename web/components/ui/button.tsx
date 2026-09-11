import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";

/*
 * Every button on the site goes through this.
 *
 * Eight of them were hand-rolled before, each re-declaring its own padding,
 * radius and hover, and none of them picking up a focus ring — so the site was
 * unusable by keyboard in exactly the places that mattered most, like copying
 * the install command.
 *
 * Shape follows one rule: pills for actions, square for surfaces.
 */
const buttonVariants = cva(
  [
    "inline-flex shrink-0 cursor-pointer items-center justify-center gap-2 whitespace-nowrap",
    "font-medium transition-all ease-brand dur-base outline-none",
    "disabled:pointer-events-none disabled:opacity-50",
    "focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-background",
    "[&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  ].join(" "),
  {
    variants: {
      variant: {
        /** The primary action. White pill, as the palette intends. */
        default: "bg-primary text-primary-foreground hover:bg-primary/90",
        /** Reserved for actions that are themselves about something live. */
        accent: "bg-accent text-accent-foreground hover:bg-accent/90",
        outline: "border border-foreground/20 text-foreground hover:border-accent/50 hover:bg-accent/5",
        secondary: "bg-secondary text-secondary-foreground hover:bg-secondary/80",
        /** Icon buttons and tab strips — no chrome until you touch it. */
        quiet: "text-muted-foreground hover:bg-foreground/5 hover:text-foreground",
        ghost: "hover:bg-foreground/5",
        link: "text-foreground underline-offset-4 hover:text-accent hover:underline",
      },
      size: {
        sm: "h-8 gap-1.5 px-3 text-sm",
        default: "h-10 px-4 text-sm",
        lg: "h-14 px-8 text-base",
        icon: "size-9",
        "icon-sm": "size-8",
      },
      shape: {
        pill: "rounded-full",
        square: "rounded-none",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
      shape: "pill",
    },
  },
);

function Button({
  className,
  variant,
  size,
  shape,
  asChild = false,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean;
  }) {
  const Comp = asChild ? Slot : "button";

  return (
    <Comp
      data-slot="button"
      className={cn(buttonVariants({ variant, size, shape, className }))}
      {...props}
    />
  );
}

export { Button, buttonVariants };
