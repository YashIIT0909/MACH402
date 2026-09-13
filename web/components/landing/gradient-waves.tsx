"use client";

import { useEffect, useRef } from "react";
import { Mesh, Program, Renderer, Triangle } from "ogl";

/**
 * React Bits "Gradient Waves" (reactbits.dev/backgrounds/gradient-waves), the
 * landing page's backdrop.
 *
 * Vendored from the registry's TS + Tailwind build rather than installed,
 * because React Bits ships source, not a package. The shader is untouched.
 * The component around it changed in four places, each because the upstream
 * version assumes it is a panel you can point at, and here it is a backdrop:
 *
 *   - **Parallax listens on the window.** Upstream listens on its own canvas.
 *     Behind the page, every pointer event lands on the content in front, so
 *     the canvas never receives one and cursor parallax silently did nothing.
 *   - **No WebGL2, no backdrop — never an error.** ogl falls back to WebGL1,
 *     which cannot compile this `#version 300 es` shader, and throws outright
 *     with no WebGL at all. A decorative layer must not be able to take the
 *     landing page down with it, so it quietly renders nothing instead.
 *   - **Uniforms are seeded from props at creation.** Upstream drew its first
 *     frame with placeholder values (white colours) before a second effect
 *     applied the real ones — a one-frame flash on load.
 *   - **Reduced motion gets one still frame**, the same treatment the hero's
 *     graphics card gets.
 */

export type GradientWavesDetail = "low" | "medium" | "high";

export interface GradientWavesProps {
  horizonColor?: string;
  waveColor?: string;
  crestColor?: string;
  speed?: number;
  amplitude?: number;
  waveScale?: number;
  waveRatio?: number;
  swell?: number;
  turbulence?: number;
  tilt?: number;
  zoom?: number;
  height?: number;
  fogDepth?: number;
  detail?: GradientWavesDetail;
  brightness?: number;
  opacity?: number;
  mouseInteraction?: boolean;
  parallaxStrength?: number;
  grain?: boolean;
  grainIntensity?: number;
  className?: string;
}

type Settings = Required<Omit<GradientWavesProps, "className">>;

const hexToRgb = (hex: string): [number, number, number] => {
  const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
  if (!result) return [1, 1, 1];
  return [parseInt(result[1], 16) / 255, parseInt(result[2], 16) / 255, parseInt(result[3], 16) / 255];
};

const detailToSteps = (detail: GradientWavesDetail): number => {
  if (detail === "low") return 40.0;
  if (detail === "high") return 110.0;
  return 70.0;
};

const vertex = `#version 300 es
in vec2 position;
void main() {
  gl_Position = vec4(position, 0.0, 1.0);
}
`;

const fragment = `#version 300 es
precision highp float;
uniform vec2 iResolution;
uniform float iTime;
uniform float uSpeed;
uniform float uAmplitude;
uniform float uWaveScale;
uniform float uWaveRatio;
uniform float uSwell;
uniform float uTurbulence;
uniform float uTilt;
uniform float uZoom;
uniform float uHeight;
uniform float uFogDepth;
uniform float uSteps;
uniform float uBrightness;
uniform float uOpacity;
uniform float uGrain;
uniform float uGrainIntensity;
uniform vec2 uMouse;
uniform float uParallax;
uniform bool uEnableMouse;
uniform vec3 uHorizonColor;
uniform vec3 uWaveColor;
uniform vec3 uCrestColor;
out vec4 fragColor;

const float MAX_DIST = 20000.0;

float hash21(vec2 p) {
  vec3 p3 = fract(vec3(p.xyx) * 0.1031);
  p3 += dot(p3, p3.yzx + 33.33);
  return fract((p3.x + p3.y) * p3.z);
}

float plasma(vec3 r, vec2 freq, vec4 tc) {
  float mx = r.x + tc.x;
  mx += uSwell * sin((r.y + mx) / 20.0 + tc.y);
  float my = r.y - tc.z;
  my += uTurbulence * cos(r.x / 23.0 + tc.w);
  return r.z - (sin(mx * freq.x) * uAmplitude + sin(my * freq.y) * uAmplitude + uHeight);
}

float raymarch(vec3 pos, vec3 dir, vec2 freq, vec4 tc) {
  float dist = 0.0;
  for (int i = 0; i < 128; i++) {
    if (float(i) >= uSteps) break;
    float dscene = plasma(pos + dist * dir, freq, tc);
    if (abs(dscene) < 0.1) break;
    dist += 0.9 * dscene;
    if (!(abs(dist) < MAX_DIST)) return MAX_DIST;
  }
  return dist;
}

void main() {
  float T = iTime * uSpeed;
  vec2 freq = vec2(uWaveScale / 7.0, (uWaveScale * uWaveRatio) / 3.0);
  vec4 tc = vec4(T / 0.130, T / 0.810, T / 0.200, T / 0.710);
  float c, s;
  float vfov = (3.14159 / 2.3) / max(uZoom, 0.05);
  vec3 cam = vec3(0.0, 0.0, 30.0);
  vec2 uv = (gl_FragCoord.xy / iResolution.xy) - 0.5;
  uv.x *= iResolution.x / iResolution.y;
  uv.y *= -1.0;

  vec3 dir = vec3(0.0, 0.0, -1.0);
  float ulen = length(uv);
  float xrot = vfov * ulen;
  c = cos(xrot); s = sin(xrot);
  dir = mat3(1.0, 0.0, 0.0, 0.0, c, -s, 0.0, s, c) * dir;
  vec2 nuv = ulen > 1e-5 ? uv / ulen : vec2(1.0, 0.0);
  c = nuv.x; s = nuv.y;
  dir = mat3(c, -s, 0.0, s, c, 0.0, 0.0, 0.0, 1.0) * dir;
  c = cos(uTilt); s = sin(uTilt);
  dir = mat3(c, 0.0, s, 0.0, 1.0, 0.0, -s, 0.0, c) * dir;

  if (uEnableMouse) {
    float yaw = (uMouse.x - 0.5) * uParallax * 0.4;
    float pitch = (uMouse.y - 0.5) * uParallax * 0.4;
    c = cos(yaw); s = sin(yaw);
    dir = mat3(c, 0.0, s, 0.0, 1.0, 0.0, -s, 0.0, c) * dir;
    c = cos(pitch); s = sin(pitch);
    dir = mat3(1.0, 0.0, 0.0, 0.0, c, -s, 0.0, s, c) * dir;
  }

  float dist = raymarch(cam, dir, freq, tc);
  vec3 pos = cam + dist * dir;

  float t = clamp(uFogDepth / max(dist, 0.001), 0.0, 1.0);
  vec3 body = mix(uWaveColor, uCrestColor, clamp(pos.z * 0.08 + 0.5, 0.0, 1.0));
  vec3 col = mix(uHorizonColor, body, t);
  col *= uBrightness;
  col = clamp(col, 0.0, 1.0);

  float alpha = clamp(t, 0.0, 1.0) * uOpacity;
  if (uGrain > 0.5) {
    float g = hash21(gl_FragCoord.xy + mod(iTime, 64.0) * 11.0);
    alpha += (g - 0.5) * uGrainIntensity;
  }
  alpha = clamp(alpha, 0.0, 1.0);
  fragColor = vec4(col * alpha, alpha);
}
`;

/** The one prop-to-uniform mapping, used at creation and on every change. */
function applySettings(uniforms: Program["uniforms"], s: Settings) {
  uniforms.uSpeed.value = s.speed;
  uniforms.uAmplitude.value = s.amplitude;
  uniforms.uWaveScale.value = s.waveScale;
  uniforms.uWaveRatio.value = s.waveRatio;
  uniforms.uSwell.value = s.swell;
  uniforms.uTurbulence.value = s.turbulence;
  uniforms.uTilt.value = s.tilt;
  uniforms.uZoom.value = s.zoom;
  uniforms.uHeight.value = s.height;
  uniforms.uFogDepth.value = s.fogDepth;
  uniforms.uSteps.value = detailToSteps(s.detail);
  uniforms.uBrightness.value = s.brightness;
  uniforms.uOpacity.value = s.opacity;
  uniforms.uGrain.value = s.grain ? 1.0 : 0.0;
  uniforms.uGrainIntensity.value = s.grainIntensity;
  uniforms.uParallax.value = s.parallaxStrength;
  uniforms.uEnableMouse.value = s.mouseInteraction;
  (uniforms.uHorizonColor.value as Float32Array).set(hexToRgb(s.horizonColor));
  (uniforms.uWaveColor.value as Float32Array).set(hexToRgb(s.waveColor));
  (uniforms.uCrestColor.value as Float32Array).set(hexToRgb(s.crestColor));
}

type Context = { renderer: Renderer; program: Program; mesh: Mesh };

export function GradientWaves({
  horizonColor = "#5227FF",
  waveColor = "#FF9FFC",
  crestColor = "#FFFFFF",
  speed = 0.4,
  amplitude = 2.5,
  waveScale = 0.6,
  waveRatio = 0.9,
  swell = 35,
  turbulence = 20,
  tilt = 1.11,
  zoom = 1.0,
  height = 5.5,
  fogDepth = 15,
  detail = "medium",
  brightness = 1.0,
  opacity = 1.0,
  mouseInteraction = true,
  parallaxStrength = 0.5,
  grain = true,
  grainIntensity = 0.05,
  className = "",
}: GradientWavesProps) {
  const settings: Settings = {
    horizonColor, waveColor, crestColor, speed, amplitude, waveScale, waveRatio, swell,
    turbulence, tilt, zoom, height, fogDepth, detail, brightness, opacity,
    mouseInteraction, parallaxStrength, grain, grainIntensity,
  };

  const containerRef = useRef<HTMLDivElement | null>(null);
  const contextRef = useRef<Context | null>(null);
  // Read by the mount effect, which deliberately runs once; kept current below.
  const settingsRef = useRef(settings);
  settingsRef.current = settings;

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    let renderer: Renderer;
    try {
      renderer = new Renderer({
        webgl: 2,
        alpha: true,
        premultipliedAlpha: true,
        antialias: false,
        dpr: Math.min(window.devicePixelRatio || 1, 2),
      });
    } catch {
      return; // No WebGL at all.
    }
    // WebGL1 cannot compile a `#version 300 es` shader; render nothing rather than throw.
    if (!renderer.isWebgl2) return;

    const gl = renderer.gl;
    gl.clearColor(0, 0, 0, 0);
    const canvas = gl.canvas as HTMLCanvasElement;
    canvas.style.width = "100%";
    canvas.style.height = "100%";
    canvas.style.display = "block";
    container.appendChild(canvas);

    let program: Program;
    try {
      program = new Program(gl, {
        vertex,
        fragment,
        uniforms: {
          iTime: { value: 0 },
          iResolution: { value: new Float32Array([1, 1]) },
          uSpeed: { value: 0 },
          uAmplitude: { value: 0 },
          uWaveScale: { value: 0 },
          uWaveRatio: { value: 0 },
          uSwell: { value: 0 },
          uTurbulence: { value: 0 },
          uTilt: { value: 0 },
          uZoom: { value: 1 },
          uHeight: { value: 0 },
          uFogDepth: { value: 0 },
          uSteps: { value: 0 },
          uBrightness: { value: 0 },
          uOpacity: { value: 0 },
          uGrain: { value: 0 },
          uGrainIntensity: { value: 0 },
          uMouse: { value: new Float32Array([0.5, 0.5]) },
          uParallax: { value: 0 },
          uEnableMouse: { value: false },
          uHorizonColor: { value: new Float32Array(3) },
          uWaveColor: { value: new Float32Array(3) },
          uCrestColor: { value: new Float32Array(3) },
        },
      });
    } catch {
      canvas.remove();
      gl.getExtension("WEBGL_lose_context")?.loseContext();
      return;
    }
    applySettings(program.uniforms, settingsRef.current);

    const mesh = new Mesh(gl, { geometry: new Triangle(gl), program });
    contextRef.current = { renderer, program, mesh };

    const setSize = () => {
      const rect = container.getBoundingClientRect();
      renderer.setSize(Math.max(1, Math.floor(rect.width)), Math.max(1, Math.floor(rect.height)));
      const res = program.uniforms.iResolution.value as Float32Array;
      res[0] = gl.drawingBufferWidth;
      res[1] = gl.drawingBufferHeight;
      renderer.render({ scene: mesh });
    };
    const resizeObserver = new ResizeObserver(setSize);
    resizeObserver.observe(container);
    setSize();

    const currentMouse: [number, number] = [0.5, 0.5];
    const targetMouse: [number, number] = [0.5, 0.5];

    // On the window, not the canvas: see the header comment.
    const onPointerMove = (event: PointerEvent) => {
      const rect = canvas.getBoundingClientRect();
      targetMouse[0] = (event.clientX - rect.left) / rect.width;
      targetMouse[1] = 1.0 - (event.clientY - rect.top) / rect.height;
    };
    const onPointerLeave = () => {
      targetMouse[0] = 0.5;
      targetMouse[1] = 0.5;
    };

    let raf = 0;
    let isVisible = true;
    let isPageVisible = !document.hidden;
    const t0 = performance.now();

    const loop = (t: number) => {
      program.uniforms.iTime.value = (t - t0) * 0.001;
      const follow = settingsRef.current.mouseInteraction;
      currentMouse[0] += 0.05 * ((follow ? targetMouse[0] : 0.5) - currentMouse[0]);
      currentMouse[1] += 0.05 * ((follow ? targetMouse[1] : 0.5) - currentMouse[1]);
      const m = program.uniforms.uMouse.value as Float32Array;
      m[0] = currentMouse[0];
      m[1] = currentMouse[1];
      renderer.render({ scene: mesh });
      raf = requestAnimationFrame(loop);
    };

    const tryStart = () => {
      if (!still && isVisible && isPageVisible && raf === 0) raf = requestAnimationFrame(loop);
    };
    const tryStop = () => {
      if (raf !== 0) {
        cancelAnimationFrame(raf);
        raf = 0;
      }
    };

    const intersection = new IntersectionObserver(
      ([entry]) => {
        isVisible = entry.isIntersecting;
        if (isVisible) tryStart();
        else tryStop();
      },
      { threshold: 0 },
    );
    intersection.observe(container);

    const onVisibility = () => {
      isPageVisible = !document.hidden;
      if (isPageVisible) tryStart();
      else tryStop();
    };
    document.addEventListener("visibilitychange", onVisibility);

    if (!still) {
      window.addEventListener("pointermove", onPointerMove, { passive: true });
      document.documentElement.addEventListener("mouseleave", onPointerLeave);
    }
    tryStart();

    return () => {
      tryStop();
      resizeObserver.disconnect();
      intersection.disconnect();
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("pointermove", onPointerMove);
      document.documentElement.removeEventListener("mouseleave", onPointerLeave);
      contextRef.current = null;
      canvas.remove();
      gl.getExtension("WEBGL_lose_context")?.loseContext();
    };
  }, []);

  // Prop changes after mount. Redraws once so a still (reduced-motion) frame
  // reflects them too; while animating, the loop would have anyway.
  useEffect(() => {
    const context = contextRef.current;
    if (!context) return;
    applySettings(context.program.uniforms, settingsRef.current);
    context.renderer.render({ scene: context.mesh });
  }, [
    horizonColor, waveColor, crestColor, speed, amplitude, waveScale, waveRatio, swell,
    turbulence, tilt, zoom, height, fogDepth, detail, brightness, opacity,
    mouseInteraction, parallaxStrength, grain, grainIntensity,
  ]);

  return <div ref={containerRef} className={`relative h-full w-full overflow-hidden ${className}`.trim()} />;
}
