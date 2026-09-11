/**
 * The page's one horizontal measure.
 *
 * Deliberately in its own module with no "use client" directive. A server
 * component that imports a plain value from a client module gets a client
 * reference back, not the value — the string interpolates to nothing and the
 * section silently loses its gutter and max width. Components are fine to
 * import across that boundary; constants are not.
 */
export const CONTAINER = "max-w-[1400px] mx-auto px-6 lg:px-12";
