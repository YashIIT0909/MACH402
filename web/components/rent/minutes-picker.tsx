"use client";

import { Button } from "@/components/ui/button";

/** How many minutes of credit to open a session with, within the node's bounds. */
export function MinutesPicker({
  minutes,
  min,
  max,
  onChange,
}: {
  minutes: number;
  min: number;
  max: number;
  onChange: (value: number) => void;
}) {
  // Presets a renter would actually pick, filtered to what this node sells.
  const presets = [15, 30, 60, 120].filter((value) => value >= min && value <= max);

  return (
    <div>
      <div className="mb-4 flex items-baseline justify-between">
        <span className="type-label text-muted-foreground">Minutes</span>
        <span className="type-heading">{minutes}</span>
      </div>

      <input
        type="range"
        min={min}
        max={max}
        step={1}
        value={minutes}
        onChange={(event) => onChange(Number(event.target.value))}
        className="w-full accent-accent"
      />

      <div className="mt-2 flex justify-between font-mono text-xs text-muted-foreground">
        <span>{min} min</span>
        <span>{max} min</span>
      </div>

      {presets.length > 0 ? (
        <div className="mt-4 flex flex-wrap gap-2">
          {presets.map((preset) => (
            <Button
              key={preset}
              variant={minutes === preset ? "default" : "outline"}
              size="sm"
              onClick={() => onChange(preset)}
            >
              {preset}m
            </Button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
