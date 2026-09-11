import type * as THREE_NS from "three";

/**
 * Pixelation that follows the cursor across the card and ripples outward.
 *
 * Two passes. The first keeps a single-channel field: last frame's field,
 * blurred slightly and faded, with a fresh mark painted along the segment the
 * cursor travelled. Blurring a little every frame is diffusion — it is what
 * makes a disturbance spread and soften on its own, the way a wake widens
 * behind something drawn through water, rather than sitting still and dimming.
 *
 * The second pass pixelates the frame wherever that field is strong. Two rules
 * keep it reading as pixels rather than as a smear:
 *
 *   - one grid for the whole frame. Scaling the block size by the field would
 *     make neighbouring fragments snap to different grids, which does not
 *     pixelate, it warps;
 *   - the field is sampled at each block's centre, so a block is in or out as
 *     a whole. The edge follows the grid, and no fragment ever mixes the sharp
 *     and blocky images — mixing is what produces ghosting.
 *
 * It runs on the model's own frame, so it cannot reach anything else on the
 * page: away from the card the frame is transparent, and sampling transparent
 * pixels on a coarser grid leaves them transparent.
 */

const VERTEX = /* glsl */ `
  varying vec2 vUv;
  void main() {
    vUv = uv;
    gl_Position = vec4(position.xy, 0.0, 1.0);
  }
`;

const FIELD_FRAGMENT = /* glsl */ `
  varying vec2 vUv;
  uniform sampler2D uPrevious;
  uniform vec2 uTexel;
  uniform vec2 uFrom;
  uniform vec2 uTo;
  uniform float uAspect;
  uniform float uRadius;
  uniform float uDecay;
  uniform float uSpread;
  uniform float uActive;

  // Distance from p to the segment ab, so a fast flick leaves a continuous
  // mark instead of a row of separate blobs.
  float segmentDistance(vec2 p, vec2 a, vec2 b) {
    vec2 pa = p - a;
    vec2 ba = b - a;
    float h = clamp(dot(pa, ba) / max(dot(ba, ba), 1e-6), 0.0, 1.0);
    return length(pa - ba * h);
  }

  // A nine-tap tent blur. Widening the kernel each frame is what spreads the
  // wake outward; the weights keep it round rather than square.
  float diffuse(vec2 uv) {
    vec2 step = uTexel * uSpread;
    float total = texture2D(uPrevious, uv).r * 4.0;
    total += texture2D(uPrevious, uv + vec2(step.x, 0.0)).r * 2.0;
    total += texture2D(uPrevious, uv - vec2(step.x, 0.0)).r * 2.0;
    total += texture2D(uPrevious, uv + vec2(0.0, step.y)).r * 2.0;
    total += texture2D(uPrevious, uv - vec2(0.0, step.y)).r * 2.0;
    total += texture2D(uPrevious, uv + step).r;
    total += texture2D(uPrevious, uv - step).r;
    total += texture2D(uPrevious, uv + vec2(step.x, -step.y)).r;
    total += texture2D(uPrevious, uv + vec2(-step.x, step.y)).r;
    return total / 16.0;
  }

  void main() {
    float carried = diffuse(vUv) * uDecay;

    vec2 p = vec2(vUv.x * uAspect, vUv.y);
    vec2 a = vec2(uFrom.x * uAspect, uFrom.y);
    vec2 b = vec2(uTo.x * uAspect, uTo.y);
    float mark = (1.0 - smoothstep(0.0, uRadius, segmentDistance(p, a, b))) * uActive;

    gl_FragColor = vec4(max(carried, mark), 0.0, 0.0, 1.0);
  }
`;

const COMPOSITE_FRAGMENT = /* glsl */ `
  varying vec2 vUv;
  uniform sampler2D uScene;
  uniform sampler2D uField;
  uniform vec2 uResolution;
  uniform float uBlock;
  uniform float uSharpBelow;
  uniform float uBlockyAbove;

  void main() {
    vec2 grid = uResolution / uBlock;
    vec2 snapped = (floor(vUv * grid) + 0.5) / grid;

    /*
     * Sampled at the block's centre, so a whole block shares one value and
     * dissolves as a unit. A hard cutoff here made blocks snap back to sharp
     * the instant the wake thinned past it; a band lets each one fade.
     *
     * Blending is safe at this point precisely because the value is constant
     * across the block. The ghosting an earlier version had came from the mix
     * varying *within* a block, which double-exposes the detail underneath.
     */
    float inside = smoothstep(uSharpBelow, uBlockyAbove, texture2D(uField, snapped).r);

    /*
     * Average the block rather than point-sampling its centre. One texel per
     * block lets anything small and bright — a mote, a specular highlight —
     * take over a whole block, which showed up as lone white and green squares
     * in the wake. Four offset taps, each already bilinear, is enough to put
     * that back in proportion.
     */
    vec2 cell = 1.0 / grid;
    vec4 blocky = texture2D(uScene, snapped + cell * vec2(-0.25, -0.25));
    blocky += texture2D(uScene, snapped + cell * vec2(0.25, -0.25));
    blocky += texture2D(uScene, snapped + cell * vec2(-0.25, 0.25));
    blocky += texture2D(uScene, snapped + cell * vec2(0.25, 0.25));
    blocky *= 0.25;

    gl_FragColor = mix(texture2D(uScene, vUv), blocky, inside);

    /*
     * A render target holds linear colour: three only applies the output
     * transfer when it draws to the canvas, not into a target. Without this
     * the whole card composites about a stop too dark.
     */
    #include <colorspace_fragment>
  }
`;

export type PixelLens = {
  render(scene: THREE_NS.Scene, camera: THREE_NS.Camera, delta: number): void;
  /** Cursor position in canvas UV, origin bottom-left. */
  setPointer(u: number, v: number): void;
  /** Stops marking; whatever is on screen spreads out and fades. */
  clearPointer(): void;
  resize(width: number, height: number): void;
  dispose(): void;
};

/**
 * Block size in CSS pixels, multiplied by the device pixel ratio in use so a
 * block covers the same amount of screen on a retina display as anywhere else.
 */
const BLOCK_CSS = 34;
/** Seconds for the wake to fade to nothing. */
const PERSIST = 1.05;
/** Texels the diffusion reaches per 60Hz frame. Higher spreads wider, faster. */
const SPREAD = 1.5;
/*
 * The band a block fades across. Below the first it is the real image, above
 * the second it is a solid block, and in between it dissolves between the two —
 * which is what stops blocks popping back as the wake thins.
 */
const SHARP_BELOW = 0.08;
const BLOCKY_ABOVE = 0.42;

export function createPixelLens(
  THREE: typeof THREE_NS,
  renderer: THREE_NS.WebGLRenderer,
  width: number,
  height: number,
): PixelLens {
  const fieldPass = new THREE.Scene();
  const compositePass = new THREE.Scene();
  const camera = new THREE.OrthographicCamera(-1, 1, 1, -1, 0, 1);
  const quad = new THREE.PlaneGeometry(2, 2);

  /*
   * Sized by the drawing buffer, not by CSS pixels. The canvas backing store is
   * width x devicePixelRatio, so a target sized in CSS pixels renders the card
   * at half resolution on a retina display and upscales it.
   */
  const makeScene = () => {
    const ratio = renderer.getPixelRatio();
    return new THREE.WebGLRenderTarget(
      Math.max(1, Math.round(width * ratio)),
      Math.max(1, Math.round(height * ratio)),
      { minFilter: THREE.LinearFilter, magFilter: THREE.LinearFilter, depthBuffer: true, stencilBuffer: false },
    );
  };

  // The field needs no resolution — half scale is cheaper and diffuses more
  // smoothly, since every tap is already a blur.
  const fieldWidth = () => Math.max(1, Math.floor(width / 2));
  const fieldHeight = () => Math.max(1, Math.floor(height / 2));
  const makeField = () =>
    new THREE.WebGLRenderTarget(fieldWidth(), fieldHeight(), {
      minFilter: THREE.LinearFilter,
      magFilter: THREE.LinearFilter,
      depthBuffer: false,
      stencilBuffer: false,
    });

  let scene = makeScene();
  let fieldA = makeField();
  let fieldB = makeField();

  const fieldMaterial = new THREE.ShaderMaterial({
    vertexShader: VERTEX,
    fragmentShader: FIELD_FRAGMENT,
    depthTest: false,
    depthWrite: false,
    uniforms: {
      uPrevious: { value: fieldA.texture },
      uTexel: { value: new THREE.Vector2(1 / fieldWidth(), 1 / fieldHeight()) },
      uFrom: { value: new THREE.Vector2(0.5, 0.5) },
      uTo: { value: new THREE.Vector2(0.5, 0.5) },
      uAspect: { value: width / height },
      uRadius: { value: 0.055 },
      uDecay: { value: 0.99 },
      uSpread: { value: SPREAD },
      uActive: { value: 0 },
    },
  });

  const compositeMaterial = new THREE.ShaderMaterial({
    vertexShader: VERTEX,
    fragmentShader: COMPOSITE_FRAGMENT,
    transparent: true,
    // A straight copy. Normal blending would multiply by alpha a second time,
    // because what comes out of the scene pass is already premultiplied.
    blending: THREE.NoBlending,
    depthTest: false,
    depthWrite: false,
    uniforms: {
      uScene: { value: scene.texture },
      uField: { value: fieldB.texture },
      uResolution: {
        value: new THREE.Vector2(width, height).multiplyScalar(renderer.getPixelRatio()),
      },
      uBlock: { value: BLOCK_CSS * renderer.getPixelRatio() },
      uSharpBelow: { value: SHARP_BELOW },
      uBlockyAbove: { value: BLOCKY_ABOVE },
    },
  });

  fieldPass.add(new THREE.Mesh(quad, fieldMaterial));
  compositePass.add(new THREE.Mesh(quad, compositeMaterial));

  const from = new THREE.Vector2(0.5, 0.5);
  const to = new THREE.Vector2(0.5, 0.5);
  let active = 0;
  let placed = false;

  return {
    setPointer(u, v) {
      // First sight should not draw a stripe in from wherever the cursor was
      // last assumed to be.
      if (!placed) {
        from.set(u, v);
        placed = true;
      } else {
        from.copy(to);
      }
      to.set(u, v);
      active = 1;
    },

    clearPointer() {
      active = 0;
      placed = false;
    },

    resize(nextWidth, nextHeight) {
      width = Math.max(1, nextWidth);
      height = Math.max(1, nextHeight);
      scene.dispose();
      fieldA.dispose();
      fieldB.dispose();
      scene = makeScene();
      fieldA = makeField();
      fieldB = makeField();

      const ratio = renderer.getPixelRatio();
      fieldMaterial.uniforms.uTexel.value.set(1 / fieldWidth(), 1 / fieldHeight());
      fieldMaterial.uniforms.uAspect.value = width / height;
      compositeMaterial.uniforms.uResolution.value.set(width * ratio, height * ratio);
      compositeMaterial.uniforms.uBlock.value = BLOCK_CSS * ratio;
    },

    render(target, cam, delta) {
      const step = Math.min(delta, 0.05);

      // Both are frame-rate independent: the wake takes the same wall-clock
      // time to fade, and spreads the same distance per second, at any refresh.
      fieldMaterial.uniforms.uDecay.value = Math.exp(-step / PERSIST);
      fieldMaterial.uniforms.uSpread.value = SPREAD * step * 60;
      fieldMaterial.uniforms.uActive.value = active;
      fieldMaterial.uniforms.uFrom.value.copy(from);
      fieldMaterial.uniforms.uTo.value.copy(to);
      fieldMaterial.uniforms.uPrevious.value = fieldA.texture;

      renderer.setRenderTarget(fieldB);
      renderer.render(fieldPass, camera);

      renderer.setRenderTarget(scene);
      renderer.render(target, cam);

      renderer.setRenderTarget(null);
      compositeMaterial.uniforms.uScene.value = scene.texture;
      compositeMaterial.uniforms.uField.value = fieldB.texture;
      renderer.render(compositePass, camera);

      // Ping-pong: this frame's field is next frame's history.
      const swap = fieldA;
      fieldA = fieldB;
      fieldB = swap;

      // A stationary cursor keeps marking; only the segment collapses to a point.
      from.copy(to);
    },

    dispose() {
      scene.dispose();
      fieldA.dispose();
      fieldB.dispose();
      fieldMaterial.dispose();
      compositeMaterial.dispose();
      quad.dispose();
    },
  };
}
