"use client";

import { useEffect, useRef, useState } from "react";
import type * as THREE_NS from "three";
import type { GLTF } from "three/examples/jsm/loaders/GLTFLoader.js";

/**
 * The RTX 3080 that sits in the hero.
 *
 * Everything three.js is behind a dynamic import inside the effect, so neither
 * the library (~170 KB gz) nor the 1.3 MB model touches the first paint — the
 * page is readable and interactive before any of this arrives.
 *
 * Three behaviours, all deliberately restrained:
 *   - the card turns slowly to face the cursor, damped, never more than a few
 *     degrees off its resting pose;
 *   - green motes drift off its surface;
 *   - the fans spin up once, when it first comes into view.
 *
 * Model: "GeForce RTX 3080 Graphics Card" by _surovic_, CC-BY-4.0.
 * See the credit in the footer — attribution is a licence condition, not a
 * courtesy, so it has to stay on a page a reader can actually see.
 */

/** How far the card may turn from rest, in radians. Subtle on purpose. */
const MAX_YAW = 0.26;
const MAX_PITCH = 0.14;
/** Fraction of the remaining distance covered per frame at 60fps. */
const DAMPING = 0.045;

const PARTICLE_COUNT = 1600;
/**
 * Base opacity of the dust cloud, before the scroll thins it.
 *
 * Higher than it looks because each speck is a soft radial falloff rather than
 * a hard dot: most of a sprite's area is nearly transparent, and what makes
 * the cloud read is many of them overlapping additively. Raised alongside the
 * size cut — a speck a sixth of the area needs the help to stay visible.
 */
const PARTICLE_OPACITY = 0.62;
/**
 * Camera pull-back beyond a perfect fit. Above 1 the card sits inside the
 * frame with air around it; at 1 it touches the edges exactly.
 */
const FILL = 1.69;
/**
 * Fan speed at full scroll, as a multiple of the baked clip's own rate. The
 * clip is already the 1500rpm one, so this only needs to take the edge off.
 */
const MAX_FAN_RATE = 1.35;
/** Seconds for a scroll burst to fade once the page stops moving. */
const IMPULSE_FADE = 0.35;
/** Seconds for the fans to reach whatever the scroll is currently asking for. */
const SPIN_CHASE = 0.12;
/** Phosphor green, matching --accent. */
const ACCENT = 0x00e87a;

type Phase = "loading" | "ready" | "failed";

export function GpuModel({
  className,
  progress,
}: {
  className?: string;
  /**
   * Scroll progress, 0 at the cover shot and 1 once the copy has arrived.
   *
   * A ref rather than a prop value: this changes on every scroll frame, and
   * routing that through React state would re-render the component sixty times
   * a second to mutate a number the render loop reads directly.
   */
  progress?: { current: number };
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [phase, setPhase] = useState<Phase>("loading");

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    /*
     * Setup is async and React invokes effects twice in development, so a run
     * can be torn down while it is still building. Every step registers its own
     * undo as soon as it exists, and `abandon()` after each await unwinds
     * whatever this run got as far as creating — otherwise a cancelled run
     * leaves an orphaned canvas behind and the phase never leaves "loading".
     */
    let disposed = false;
    let frame = 0;
    const undo: Array<() => void> = [];

    const unwind = () => {
      cancelAnimationFrame(frame);
      while (undo.length > 0) undo.pop()?.();
    };

    /** True when this run has been cancelled; tears down its partial work. */
    const abandon = () => {
      if (!disposed) return false;
      unwind();
      return true;
    };

    (async () => {
      let THREE: typeof THREE_NS;
      let GLTFLoader: typeof import("three/examples/jsm/loaders/GLTFLoader.js").GLTFLoader;
      let MeshoptDecoder: typeof import("three/examples/jsm/libs/meshopt_decoder.module.js").MeshoptDecoder;
      let RoomEnvironment: typeof import("three/examples/jsm/environments/RoomEnvironment.js").RoomEnvironment;

      try {
        [THREE, { GLTFLoader }, { MeshoptDecoder }, { RoomEnvironment }] = await Promise.all([
          import("three"),
          import("three/examples/jsm/loaders/GLTFLoader.js"),
          import("three/examples/jsm/libs/meshopt_decoder.module.js"),
          import("three/examples/jsm/environments/RoomEnvironment.js"),
        ]);
      } catch {
        if (!disposed) setPhase("failed");
        return;
      }
      if (abandon()) return;

      const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

      let renderer: THREE_NS.WebGLRenderer;
      try {
        renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: "high-performance" });
      } catch {
        // No WebGL — the hero still reads fine without a card in it.
        if (!disposed) setPhase("failed");
        return;
      }

      const size = () => ({
        width: host.clientWidth || 1,
        height: host.clientHeight || 1,
      });

      renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
      renderer.setSize(size().width, size().height, false);
      renderer.toneMapping = THREE.ACESFilmicToneMapping;
      renderer.toneMappingExposure = 0.68;
      renderer.setClearColor(0x000000, 0);
      renderer.domElement.style.width = "100%";
      renderer.domElement.style.height = "100%";
      renderer.domElement.style.display = "block";
      host.appendChild(renderer.domElement);
      undo.push(() => {
        renderer.dispose();
        renderer.domElement.remove();
      });
      if (abandon()) return;

      const scene = new THREE.Scene();
      const camera = new THREE.PerspectiveCamera(32, size().width / size().height, 0.01, 100);

      /*
       * A dark studio. RoomEnvironment gives the metal something to reflect —
       * without it a PBR shroud on a near-black page is a silhouette — and the
       * two lights do the shaping: a soft white key, and the accent raking one
       * edge so the card picks up the page's green rather than being painted it.
       */
      const pmrem = new THREE.PMREMGenerator(renderer);
      const envRT = pmrem.fromScene(new RoomEnvironment(), 0.04);
      scene.environment = envRT.texture;
      undo.push(() => {
        envRT.texture.dispose();
        pmrem.dispose();
      });

      const key = new THREE.DirectionalLight(0xffffff, 1.15);
      key.position.set(2.5, 3, 4);
      scene.add(key);

      const rim = new THREE.DirectionalLight(ACCENT, 4.2);
      rim.position.set(-3.5, 0.8, -2.2);
      scene.add(rim);

      const fill = new THREE.DirectionalLight(0xaecbff, 0.28);
      fill.position.set(-1, -2, 2);
      scene.add(fill);

      // pivot carries the cursor rotation; the model inside it is only ever
      // transformed to sit level and centred, so the two never fight.
      const pivot = new THREE.Group();
      scene.add(pivot);

      let mixer: THREE_NS.AnimationMixer | null = null;
      let fanAction: THREE_NS.AnimationAction | null = null;

      const loader = new GLTFLoader().setMeshoptDecoder(MeshoptDecoder);

      let gltf: GLTF;
      try {
        gltf = await loader.loadAsync("/models/rtx-3080.glb");
      } catch {
        if (!disposed) setPhase("failed");
        unwind();
        return;
      }
      if (abandon()) return;

      const model = gltf.scene;
      undo.push(() => {
        model.traverse((child) => {
          const mesh = child as THREE_NS.Mesh;
          if (!mesh.isMesh) return;
          mesh.geometry?.dispose();
          const material = mesh.material;
          if (Array.isArray(material)) material.forEach((m) => m.dispose());
          else material?.dispose();
        });
      });

      /*
       * Lay the card down without hard-coding the exporter's quaternions.
       * Measure the world box, and if the longest axis is not X, rotate it on
       * to X. Self-correcting if the asset is ever re-exported differently.
       */
      const WORLD_X = new THREE.Vector3(1, 0, 0);
      const WORLD_Y = new THREE.Vector3(0, 1, 0);
      const WORLD_Z = new THREE.Vector3(0, 0, 1);

      /*
       * Every decision below reads a world-space bounding box, so every
       * rotation must be world-space as well. Object3D.rotateX/Y/Z turn about
       * the object's *own* axes, which have already moved by the time the
       * second rotation runs — measure in one frame and turn in another and
       * the card ends up on its end.
       */
      let box = new THREE.Box3().setFromObject(model);
      let extent = box.getSize(new THREE.Vector3());
      if (extent.y > extent.x && extent.y >= extent.z) {
        model.rotateOnWorldAxis(WORLD_Z, -Math.PI / 2);
      } else if (extent.z > extent.x) {
        model.rotateOnWorldAxis(WORLD_Y, -Math.PI / 2);
      }
      model.updateMatrixWorld(true);

      /*
       * Turn the fans to face the viewer.
       *
       * A fan is a disc, so its bounding box is thin along exactly one axis —
       * its axis of rotation, and the direction it blows. Finding that
       * empirically and swinging it on to +Z beats hard-coding a quaternion,
       * and survives the model being re-exported from a different tool.
       */
      let fan: THREE_NS.Mesh | null = null;
      model.traverse((child) => {
        const mesh = child as THREE_NS.Mesh;
        if (!fan && mesh.isMesh && /^fan_RTX/.test(mesh.name)) fan = mesh;
      });

      if (fan) {
        const fanBox = new THREE.Box3().setFromObject(fan);
        const fanExtent = fanBox.getSize(new THREE.Vector3());
        // Thinnest axis of the disc is the one it spins about.
        if (fanExtent.y < fanExtent.x && fanExtent.y < fanExtent.z) {
          model.rotateOnWorldAxis(WORLD_X, Math.PI / 2);
        } else if (fanExtent.x < fanExtent.y && fanExtent.x < fanExtent.z) {
          model.rotateOnWorldAxis(WORLD_Y, Math.PI / 2);
        }
        model.updateMatrixWorld(true);

        // The fans should blow toward the camera, not away from it.
        const facing = new THREE.Box3().setFromObject(fan).getCenter(new THREE.Vector3());
        const shroud = new THREE.Box3().setFromObject(model).getCenter(new THREE.Vector3());
        if (facing.z < shroud.z) {
          model.rotateOnWorldAxis(WORLD_Y, Math.PI);
          model.updateMatrixWorld(true);
        }
      }

      // A whisper of tilt so it is an object under light, not a flat scan.
      model.rotateOnWorldAxis(WORLD_X, 0.05);
      model.rotateOnWorldAxis(WORLD_Y, -0.06);
      model.updateMatrixWorld(true);

      /*
       * Normalise to one unit long, then centre — strictly in that order.
       * Scaling happens about the object's own origin, so re-centring first and
       * scaling second drags the centre back out by (1 - scale) x offset, which
       * throws the card clean out of frame.
       */
      box = new THREE.Box3().setFromObject(model);
      extent = box.getSize(new THREE.Vector3());
      model.scale.multiplyScalar(1 / Math.max(extent.x, extent.y, extent.z));
      model.updateMatrixWorld(true);

      box = new THREE.Box3().setFromObject(model);
      model.position.sub(box.getCenter(new THREE.Vector3()));
      pivot.add(model);

      /*
       * Frame the card to the viewport rather than trusting a magic distance.
       * Fit both axes and take the further of the two, so a tall phone gets the
       * whole card just as a wide desktop does — recomputed on every resize.
       */
      const framed = new THREE.Box3().setFromObject(model).getSize(new THREE.Vector3());
      let baseZ = 2;
      const fit = () => {
        const aspect = camera.aspect;
        const half = THREE.MathUtils.degToRad(camera.fov) / 2;
        const forHeight = framed.y / 2 / Math.tan(half);
        const forWidth = framed.x / 2 / (Math.tan(half) * aspect);
        baseZ = Math.max(forHeight, forWidth) * FILL;
        camera.lookAt(0, 0, 0);
      };
      fit();

      model.traverse((child) => {
        const mesh = child as THREE_NS.Mesh;
        if (!mesh.isMesh) return;
        const material = mesh.material as THREE_NS.MeshStandardMaterial;
        if (material?.isMeshStandardMaterial) {
          // Let the environment carry the metal, and lift the baked emissive so
          // the lettering glows instead of sitting flat.
          material.envMapIntensity = 0.5;
          material.emissiveIntensity = 1.35;
        }
      });

      // ---- particles -----------------------------------------------------
      /*
       * Motes seeded from actual surface positions, so they leave the card
       * where the card is rather than from a box around it. Each carries its
       * own drift and phase; they fade out as they rise and respawn at source.
       */
      const sources: number[] = [];
      const tmp = new THREE.Vector3();
      model.updateWorldMatrix(true, true);
      model.traverse((child) => {
        const mesh = child as THREE_NS.Mesh;
        if (!mesh.isMesh || !mesh.geometry) return;
        const position = mesh.geometry.getAttribute("position");
        if (!position) return;
        const stride = Math.max(1, Math.floor(position.count / 60));
        for (let i = 0; i < position.count; i += stride) {
          tmp.fromBufferAttribute(position as THREE_NS.BufferAttribute, i);
          mesh.localToWorld(tmp);
          sources.push(tmp.x, tmp.y, tmp.z);
        }
      });

      const seeds = new Float32Array(PARTICLE_COUNT * 3);
      const drift = new Float32Array(PARTICLE_COUNT * 3);
      const life = new Float32Array(PARTICLE_COUNT);
      const span = new Float32Array(PARTICLE_COUNT);
      const points = new Float32Array(PARTICLE_COUNT * 3);
      const alpha = new Float32Array(PARTICLE_COUNT);
      /* Static size multiplier per mote, read by the vertex shader. */
      const scale = new Float32Array(PARTICLE_COUNT);
      /*
       * Brightness paired to that size, and inverse to it. A big mote spread
       * over several times the area at the same brightness is a blob; at a
       * fraction of it, it is haze the small ones sit inside.
       */
      const weight = new Float32Array(PARTICLE_COUNT);

      const sourceCount = sources.length / 3 || 1;
      for (let i = 0; i < PARTICLE_COUNT; i++) {
        const s = Math.floor(Math.random() * sourceCount) * 3;
        seeds[i * 3] = sources[s] ?? 0;
        seeds[i * 3 + 1] = sources[s + 1] ?? 0;
        seeds[i * 3 + 2] = sources[s + 2] ?? 0;
        // Wider lateral spread and a slower climb than the rise alone would
        // give: the cloud should open out as it leaves the card rather than
        // streaming off the top of it.
        drift[i * 3] = (Math.random() - 0.5) * 0.044;
        drift[i * 3 + 1] = 0.010 + Math.random() * 0.026;
        drift[i * 3 + 2] = (Math.random() - 0.5) * 0.044;
        span[i] = 2.8 + Math.random() * 3.6;
        life[i] = Math.random() * span[i];

        // A narrow spread, skewed small. Dust is one population: a wide range
        // here is what made a handful of specks read as blobs sitting on top of
        // the rest rather than as the same cloud seen at different depths.
        const s2 = Math.random();
        scale[i] = 0.7 + s2 * s2 * 1.1;
        weight[i] = 1 / (0.5 + scale[i]);
      }

      const particleGeometry = new THREE.BufferGeometry();
      particleGeometry.setAttribute("position", new THREE.BufferAttribute(points, 3));
      particleGeometry.setAttribute("aAlpha", new THREE.BufferAttribute(alpha, 1));
      particleGeometry.setAttribute("aScale", new THREE.BufferAttribute(scale, 1));

      const particleMaterial = new THREE.PointsMaterial({
        color: ACCENT,
        // The base every speck's own `aScale` multiplies. Small: this is dust,
        // and the sprite being a gaussian already spends part of that footprint
        // on falloff rather than on a visible core.
        size: 0.0055,
        sizeAttenuation: true,
        transparent: true,
        depthWrite: false,
        blending: THREE.AdditiveBlending,
        opacity: PARTICLE_OPACITY,
      });

      /*
       * Three things PointsMaterial has no uniform for: a per-particle fade, a
       * per-particle size, and a soft edge.
       *
       * The last is what keeps a brighter, denser cloud from reading as grit.
       * An unmapped point is a flat quad with hard corners, so raising its size
       * and opacity gives bigger, harder squares. A gaussian falloff across
       * `gl_PointCoord` spends the extra size on diffusion instead — and the
       * discard past the radius keeps the corners out of the additive sum,
       * where they would otherwise build into visible boxes wherever motes
       * overlap.
       */
      particleMaterial.onBeforeCompile = (shader) => {
        shader.vertexShader = shader.vertexShader
          .replace(
            "#include <common>",
            "#include <common>\nattribute float aAlpha;\nattribute float aScale;\nvarying float vAlpha;",
          )
          .replace("#include <begin_vertex>", "#include <begin_vertex>\nvAlpha = aAlpha;")
          .replace("gl_PointSize = size;", "gl_PointSize = size * aScale;")
          // After the size-attenuation block, not before it: `size` is in world
          // units there and only becomes pixels once it has been divided by
          // depth. A floor applied early would read as 1.0 world units — a
          // speck the size of the card.
          .replace(
            "#include <logdepthbuf_vertex>",
            "gl_PointSize = max( gl_PointSize, 1.0 );\n#include <logdepthbuf_vertex>",
          );
        shader.fragmentShader = shader.fragmentShader
          .replace("#include <common>", "#include <common>\nvarying float vAlpha;")
          .replace(
            "#include <opaque_fragment>",
            [
              "float d = length( gl_PointCoord - vec2( 0.5 ) ) * 2.0;",
              "if ( d > 1.0 ) discard;",
              "float fall = exp( - d * d * 3.8 ) * ( 1.0 - d * d );",
              "gl_FragColor.a *= vAlpha * fall;",
              "#include <opaque_fragment>",
            ].join("\n"),
          );
      };

      const particles = new THREE.Points(particleGeometry, particleMaterial);
      particles.frustumCulled = false;
      pivot.add(particles);
      undo.push(() => {
        particleGeometry.dispose();
        particleMaterial.dispose();
      });

      // ---- fans ----------------------------------------------------------
      if (gltf.animations.length > 0) {
        mixer = new THREE.AnimationMixer(model);
        const clip =
          gltf.animations.find((a) => a.name.includes("1500")) ??
          gltf.animations.find((a) => a.name.includes("1200")) ??
          gltf.animations[0];
        fanAction = mixer.clipAction(clip);
        fanAction.play();
        // Still until something scrolls. `fanSpin` below drives this.
        fanAction.timeScale = 0;
      }

      // ---- input ---------------------------------------------------------
      const targetRotation = { x: 0, y: 0 };
      const currentRotation = { x: 0, y: 0 };

      const onPointerMove = (event: PointerEvent) => {
        // Rotation is measured against the viewport, not the canvas: the card
        // should track the cursor anywhere in the hero, not only over itself.
        const nx = (event.clientX / window.innerWidth) * 2 - 1;
        const ny = (event.clientY / window.innerHeight) * 2 - 1;
        targetRotation.y = nx * MAX_YAW;
        targetRotation.x = ny * MAX_PITCH;
      };

      if (!still) {
        window.addEventListener("pointermove", onPointerMove, { passive: true });
        undo.push(() => window.removeEventListener("pointermove", onPointerMove));
      }

      // ---- scroll speed --------------------------------------------------
      /*
       * The fans answer to the scroll wheel: still when the page is still,
       * faster the harder it is scrolled. Velocity is sampled here and decays
       * in the draw loop, so letting go coasts the fans down rather than
       * cutting them dead.
       */
      let scrollImpulse = 0;
      let fanSpin = 0;
      let lastScrollY = window.scrollY;
      let lastScrollAt = performance.now();

      const onScroll = () => {
        const now = performance.now();
        const elapsed = Math.max(now - lastScrollAt, 1);
        const travelled = Math.abs(window.scrollY - lastScrollY);
        lastScrollY = window.scrollY;
        lastScrollAt = now;
        /*
         * Speed, not distance: pixels per millisecond, so the fans answer to
         * how hard the page is being thrown rather than how far it went. ~2.5
         * px/ms is a hard flick and pins them; a gentle drag sits near a fifth.
         * Taking the larger keeps a fast flick from being erased by the slow
         * tail of the same gesture arriving in the same frame.
         */
        scrollImpulse = Math.max(scrollImpulse, Math.min(travelled / elapsed / 2.5, 1));
      };

      window.addEventListener("scroll", onScroll, { passive: true });
      undo.push(() => window.removeEventListener("scroll", onScroll));

      // ---- visibility ----------------------------------------------------
      // No point burning frames on a hero that has scrolled away.
      let visible = true;

      const observer = new IntersectionObserver(
        ([entry]) => {
          visible = entry.isIntersecting;
        },
        { threshold: 0.05 },
      );
      observer.observe(host);
      undo.push(() => observer.disconnect());

      const resize = () => {
        const { width, height } = size();
        renderer.setSize(width, height, false);
        camera.aspect = width / height;
        camera.updateProjectionMatrix();
        fit();
      };
      const resizeObserver = new ResizeObserver(resize);
      resizeObserver.observe(host);
      undo.push(() => resizeObserver.disconnect());

      // Last chance to bail before anything starts running or becomes visible.
      if (abandon()) return;
      setPhase("ready");

      // ---- loop ----------------------------------------------------------
      // Delta tracked by hand: THREE.Clock is deprecated as of r186, and this
      // needs nothing the replacement offers.
      let last = performance.now();

      const draw = () => {
        const now = performance.now();
        // Capped so a backgrounded tab does not resume with one enormous step.
        const delta = Math.min((now - last) / 1000, 0.05);
        last = now;

        // Frame-rate independent damping, so a 144Hz screen does not track
        // the cursor four times faster than a 30Hz one.
        const ease = 1 - Math.pow(1 - DAMPING, delta * 60);
        currentRotation.x += (targetRotation.x - currentRotation.x) * ease;
        currentRotation.y += (targetRotation.y - currentRotation.y) * ease;

        /*
         * Scroll settles the card back so the copy can land on it: it turns
         * a little off square, the exposure drops and the motes thin out. It
         * never moves — the card is the fixed thing the copy arrives onto. The
         * cursor's few degrees ride on top rather than replacing any of it.
         */
        const p = Math.min(Math.max(progress?.current ?? 0, 0), 1);
        pivot.rotation.x = currentRotation.x;
        pivot.rotation.y = currentRotation.y - p * 0.45;
        camera.position.z = baseZ;
        renderer.toneMappingExposure = 0.68 - 0.34 * p;
        particleMaterial.opacity = PARTICLE_OPACITY * (1 - 0.6 * p);

        /*
         * Scroll drives the fans. The impulse decays fast so they wind down
         * the moment scrolling stops, and the spin itself is eased toward it
         * so neither starting nor stopping is abrupt.
         */
        /*
         * Two time constants, and the order matters: the spin has to chase the
         * impulse faster than the impulse fades, or the fans spend every flick
         * climbing toward a number that has already gone and never arrive.
         */
        scrollImpulse *= Math.exp(-delta / IMPULSE_FADE);
        fanSpin += (scrollImpulse - fanSpin) * (1 - Math.exp(-delta / SPIN_CHASE));
        if (fanAction) fanAction.timeScale = fanSpin * MAX_FAN_RATE;
        mixer?.update(delta);

        for (let i = 0; i < PARTICLE_COUNT; i++) {
          life[i] += delta;
          if (life[i] > span[i]) life[i] = 0;
          const t = life[i];
          const progress = t / span[i];

          // The wander grows with age rather than being constant, so motes
          // separate from their neighbours the further they get from the card.
          points[i * 3] = seeds[i * 3] + drift[i * 3] * t + Math.sin(t * 1.3 + i) * 0.013 * t;
          points[i * 3 + 1] = seeds[i * 3 + 1] + drift[i * 3 + 1] * t;
          points[i * 3 + 2] =
            seeds[i * 3 + 2] + drift[i * 3 + 2] * t + Math.cos(t * 1.1 + i * 0.7) * 0.010 * t;

          /*
           * Fade in, hold, fade out. Flattened against the plain sine so a mote
           * spends most of its life at full brightness instead of most of it
           * arriving or leaving — that hold is most of what makes the cloud
           * present. The shimmer on top is shallow and slow for the same
           * reason: a fast, deep one twinkles, which reads as sharp.
           */
          const envelope = Math.pow(Math.sin(progress * Math.PI), 0.6);
          const shimmer = 0.82 + 0.18 * Math.sin(t * 2.6 + i * 1.7);
          alpha[i] = envelope * shimmer * weight[i];
        }
        particleGeometry.attributes.position.needsUpdate = true;
        particleGeometry.attributes.aAlpha.needsUpdate = true;

        renderer.render(scene, camera);
      };

      if (still) {
        // One frame, level and quiet.
        if (fanAction) fanAction.timeScale = 0;
        renderer.render(scene, camera);
      } else {
        const loop = () => {
          frame = requestAnimationFrame(loop);
          if (visible) draw();
        };
        loop();
      }

    })();

    return () => {
      disposed = true;
      unwind();
    };
  }, []);

  return (
    <div
      ref={hostRef}
      className={className}
      aria-hidden="true"
      data-phase={phase}
      style={{
        // Fades in once there is something to see, so the hero never flashes
        // an empty box while 1.3 MB is on the wire.
        opacity: phase === "ready" ? 1 : 0,
        transition: "opacity 1200ms ease",
      }}
    />
  );
}
