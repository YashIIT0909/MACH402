"use client";

import { useEffect, useRef } from "react";

/**
 * Shared plumbing for the three ASCII figures.
 *
 * Each one is a `<canvas>` painting monospace glyphs, so they cost no
 * dependency and no WebGL context. The only thing that changes between them is
 * where the points come from, which is what `paint` supplies.
 *
 * `ink` is an "r, g, b" triple rather than a CSS colour because every figure
 * fades glyphs by depth, and interpolating alpha is cheaper than re-parsing a
 * colour per glyph per frame. On this site it is the near-white foreground; the
 * hero passes the accent to make the sphere read as live.
 */
export type Glyph = { x: number; y: number; z: number; char: string; alpha: number };

export type PaintFn = (frame: {
  width: number;
  height: number;
  time: number;
}) => { glyphs: Glyph[]; font: string };

export function AsciiCanvas({
  paint,
  ink = "237, 237, 236",
  speed,
  className,
}: {
  paint: PaintFn;
  ink?: string;
  /** Time advanced per frame. */
  speed: number;
  className?: string;
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const frameRef = useRef(0);

  // Held in refs so a re-render never restarts the animation mid-rotation.
  const paintRef = useRef(paint);
  paintRef.current = paint;
  const inkRef = useRef(ink);
  inkRef.current = ink;

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    let time = 0;

    const resize = () => {
      const dpr = window.devicePixelRatio || 1;
      const rect = canvas.getBoundingClientRect();
      canvas.width = rect.width * dpr;
      canvas.height = rect.height * dpr;
      // setTransform, not scale: resize fires repeatedly and scale compounds.
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    };

    resize();
    window.addEventListener("resize", resize);

    const draw = () => {
      const rect = canvas.getBoundingClientRect();
      ctx.clearRect(0, 0, rect.width, rect.height);

      const { glyphs, font } = paintRef.current({
        width: rect.width,
        height: rect.height,
        time,
      });

      ctx.font = font;
      ctx.textAlign = "center";
      ctx.textBaseline = "middle";

      // Painter's algorithm — far glyphs first, so near ones overlap them.
      glyphs.sort((a, b) => a.z - b.z);
      for (const glyph of glyphs) {
        ctx.fillStyle = `rgba(${inkRef.current}, ${glyph.alpha})`;
        ctx.fillText(glyph.char, glyph.x, glyph.y);
      }
    };

    // One static frame for anyone who asked the OS to stop things moving.
    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (still) {
      draw();
      return () => window.removeEventListener("resize", resize);
    }

    const loop = () => {
      draw();
      time += speed;
      frameRef.current = requestAnimationFrame(loop);
    };
    loop();

    return () => {
      window.removeEventListener("resize", resize);
      cancelAnimationFrame(frameRef.current);
    };
  }, [speed]);

  return <canvas ref={canvasRef} className={className ?? "w-full h-full"} style={{ display: "block" }} />;
}

const DEPTH_CHARS = "░▒▓█▀▄▌▐│─┤├┴┬╭╮╰╯";

type Point3 = { x: number; y: number; z: number };

const rotateY = (p: Point3, a: number): Point3 => ({
  x: p.x * Math.cos(a) - p.z * Math.sin(a),
  y: p.y,
  z: p.x * Math.sin(a) + p.z * Math.cos(a),
});

const rotateX = (p: Point3, a: number): Point3 => ({
  x: p.x,
  y: p.y * Math.cos(a) - p.z * Math.sin(a),
  z: p.y * Math.sin(a) + p.z * Math.cos(a),
});

const rotateZ = (p: Point3, a: number): Point3 => ({
  x: p.x * Math.cos(a) - p.y * Math.sin(a),
  y: p.x * Math.sin(a) + p.y * Math.cos(a),
  z: p.z,
});

/** A rotating glyph sphere. Sits behind the hero on every page. */
export function AnimatedSphere({ ink }: { ink?: string }) {
  return (
    <AsciiCanvas
      ink={ink}
      speed={0.02}
      paint={({ width, height, time }) => {
        const centerX = width / 2;
        const centerY = height / 2;
        const radius = Math.min(width, height) * 0.525;
        const glyphs: Glyph[] = [];

        for (let phi = 0; phi < Math.PI * 2; phi += 0.15) {
          for (let theta = 0; theta < Math.PI; theta += 0.15) {
            const x = Math.sin(theta) * Math.cos(phi + time * 0.5);
            const y = Math.sin(theta) * Math.sin(phi + time * 0.5);
            const z = Math.cos(theta);

            const rotY = time * 0.3;
            const newX = x * Math.cos(rotY) - z * Math.sin(rotY);
            const newZ = x * Math.sin(rotY) + z * Math.cos(rotY);

            const rotX = time * 0.2;
            const newY = y * Math.cos(rotX) - newZ * Math.sin(rotX);
            const finalZ = y * Math.sin(rotX) + newZ * Math.cos(rotX);

            const depth = (finalZ + 1) / 2;

            glyphs.push({
              x: centerX + newX * radius,
              y: centerY + newY * radius,
              z: finalZ,
              char: DEPTH_CHARS[Math.floor(depth * (DEPTH_CHARS.length - 1))],
              alpha: 0.12 + (finalZ + 1) * 0.26,
            });
          }
        }

        return { glyphs, font: "12px monospace" };
      }}
    />
  );
}

const TETRA_VERTICES: Point3[] = [
  { x: 0, y: 1, z: 0 },
  { x: -0.943, y: -0.333, z: -0.5 },
  { x: 0.943, y: -0.333, z: -0.5 },
  { x: 0, y: -0.333, z: 1 },
];

const TETRA_EDGES = [
  [0, 1],
  [0, 2],
  [0, 3],
  [1, 2],
  [2, 3],
  [3, 1],
];

const TETRA_FACES = [
  [0, 1, 2],
  [0, 2, 3],
  [0, 3, 1],
  [1, 3, 2],
];

/** A rotating glyph tetrahedron. Rides alongside the closing call to action. */
export function AnimatedTetrahedron({ ink }: { ink?: string }) {
  return (
    <AsciiCanvas
      ink={ink}
      speed={0.015}
      paint={({ width, height, time }) => {
        const centerX = width / 2;
        const centerY = height / 2;
        const scale = Math.min(width, height) * 0.7;
        const glyphs: Glyph[] = [];

        const spin = (p: Point3): Point3 =>
          rotateZ(rotateX(rotateY(p, time * 0.4), time * 0.3), time * 0.2);

        const push = (p: Point3) => {
          const spun = spin(p);
          const depth = (spun.z + 1.5) / 3;
          glyphs.push({
            x: centerX + spun.x * scale,
            y: centerY - spun.y * scale,
            z: spun.z,
            char: DEPTH_CHARS[Math.min(Math.floor(depth * (DEPTH_CHARS.length - 1)), DEPTH_CHARS.length - 1)],
            alpha: Math.min(0.12 + (spun.z + 1.5) * 0.22, 0.85),
          });
        };

        for (const [i, j] of TETRA_EDGES) {
          const v1 = TETRA_VERTICES[i];
          const v2 = TETRA_VERTICES[j];
          for (let t = 0; t <= 1; t += 0.05) {
            push({
              x: v1.x + (v2.x - v1.x) * t,
              y: v1.y + (v2.y - v1.y) * t,
              z: v1.z + (v2.z - v1.z) * t,
            });
          }
        }

        for (const [i, j, k] of TETRA_FACES) {
          const v1 = TETRA_VERTICES[i];
          const v2 = TETRA_VERTICES[j];
          const v3 = TETRA_VERTICES[k];
          for (let u = 0; u <= 1; u += 0.12) {
            for (let v = 0; v <= 1 - u; v += 0.12) {
              const w = 1 - u - v;
              push({
                x: v1.x * u + v2.x * v + v3.x * w,
                y: v1.y * u + v2.y * v + v3.y * w,
                z: v1.z * u + v2.z * v + v3.z * w,
              });
            }
          }
        }

        return { glyphs, font: "18px monospace" };
      }}
    />
  );
}

const WAVE_CHARS = "·∘○◯◌●◉";

/** Interfering waves. Sits behind the footer. */
export function AnimatedWave({ ink }: { ink?: string }) {
  return (
    <AsciiCanvas
      ink={ink}
      speed={0.03}
      paint={({ width, height, time }) => {
        const cols = Math.floor(width / 20);
        const rows = Math.floor(height / 20);
        const glyphs: Glyph[] = [];

        for (let y = 0; y < rows; y++) {
          for (let x = 0; x < cols; x++) {
            const wave1 = Math.sin(x * 0.2 + time * 2) * Math.cos(y * 0.15 + time);
            const wave2 = Math.sin((x + y) * 0.1 + time * 1.5);
            const wave3 = Math.cos(x * 0.1 - y * 0.1 + time * 0.8);

            const normalized = ((wave1 + wave2 + wave3) / 3 + 1) / 2;

            glyphs.push({
              x: (x + 0.5) * (width / cols),
              y: (y + 0.5) * (height / rows),
              z: 0,
              char: WAVE_CHARS[Math.floor(normalized * (WAVE_CHARS.length - 1))],
              alpha: 0.1 + normalized * 0.35,
            });
          }
        }

        return { glyphs, font: "14px monospace" };
      }}
    />
  );
}
