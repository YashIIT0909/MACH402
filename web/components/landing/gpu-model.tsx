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

const PARTICLE_COUNT = 650;
/**
 * Camera pull-back beyond a perfect fit. Above 1 the card sits inside the
 * frame with air around it; at 1 it touches the edges exactly.
 */
const FILL = 1.69;
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

      const sourceCount = sources.length / 3 || 1;
      for (let i = 0; i < PARTICLE_COUNT; i++) {
        const s = Math.floor(Math.random() * sourceCount) * 3;
        seeds[i * 3] = sources[s] ?? 0;
        seeds[i * 3 + 1] = sources[s + 1] ?? 0;
        seeds[i * 3 + 2] = sources[s + 2] ?? 0;
        drift[i * 3] = (Math.random() - 0.5) * 0.02;
        drift[i * 3 + 1] = 0.012 + Math.random() * 0.03;
        drift[i * 3 + 2] = (Math.random() - 0.5) * 0.02;
        span[i] = 2.2 + Math.random() * 3.0;
        life[i] = Math.random() * span[i];
      }

      const particleGeometry = new THREE.BufferGeometry();
      particleGeometry.setAttribute("position", new THREE.BufferAttribute(points, 3));
      particleGeometry.setAttribute("aAlpha", new THREE.BufferAttribute(alpha, 1));

      const particleMaterial = new THREE.PointsMaterial({
        color: ACCENT,
        size: 0.0075,
        sizeAttenuation: true,
        transparent: true,
        depthWrite: false,
        blending: THREE.AdditiveBlending,
        opacity: 0.8,
      });

      // Per-particle fade, which PointsMaterial has no uniform for.
      particleMaterial.onBeforeCompile = (shader) => {
        shader.vertexShader = shader.vertexShader
          .replace("#include <common>", "#include <common>\nattribute float aAlpha;\nvarying float vAlpha;")
          .replace("#include <begin_vertex>", "#include <begin_vertex>\nvAlpha = aAlpha;");
        shader.fragmentShader = shader.fragmentShader
          .replace("#include <common>", "#include <common>\nvarying float vAlpha;")
          .replace(
            "#include <opaque_fragment>",
            "gl_FragColor.a *= vAlpha;\n#include <opaque_fragment>",
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
          gltf.animations.find((a) => a.name.includes("1200")) ??
          gltf.animations.find((a) => a.name.includes("600")) ??
          gltf.animations[0];
        fanAction = mixer.clipAction(clip);
        fanAction.play();
        // Held at a standstill until the hero is seen; `spin` eases this to 1.
        fanAction.timeScale = 0;
      }

      // ---- input ---------------------------------------------------------
      const targetRotation = { x: 0, y: 0 };
      const currentRotation = { x: 0, y: 0 };

      const onPointerMove = (event: PointerEvent) => {
        // Measured against the viewport, not the canvas: the card should track
        // the cursor anywhere in the hero, not only when it is over the model.
        const nx = (event.clientX / window.innerWidth) * 2 - 1;
        const ny = (event.clientY / window.innerHeight) * 2 - 1;
        targetRotation.y = nx * MAX_YAW;
        targetRotation.x = ny * MAX_PITCH;
      };

      if (!still) {
        window.addEventListener("pointermove", onPointerMove, { passive: true });
        undo.push(() => window.removeEventListener("pointermove", onPointerMove));
      }

      // ---- visibility ----------------------------------------------------
      // No point burning frames on a hero that has scrolled away.
      let visible = true;
      let spin = 0;
      let seen = false;

      const observer = new IntersectionObserver(
        ([entry]) => {
          visible = entry.isIntersecting;
          if (entry.isIntersecting) seen = true;
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
        particleMaterial.opacity = 0.8 * (1 - 0.6 * p);

        // One-shot spin-up on first sight, then hold.
        if (seen && spin < 1) spin = Math.min(1, spin + delta / 2.2);
        if (fanAction) fanAction.timeScale = spin;
        mixer?.update(delta);

        for (let i = 0; i < PARTICLE_COUNT; i++) {
          life[i] += delta;
          if (life[i] > span[i]) life[i] = 0;
          const t = life[i];
          const progress = t / span[i];

          points[i * 3] = seeds[i * 3] + drift[i * 3] * t + Math.sin(t * 1.9 + i) * 0.006;
          points[i * 3 + 1] = seeds[i * 3 + 1] + drift[i * 3 + 1] * t;
          points[i * 3 + 2] = seeds[i * 3 + 2] + drift[i * 3 + 2] * t;

          // Fade in, hold, fade out — with a sparkle riding on top.
          const envelope = Math.sin(progress * Math.PI);
          const sparkle = 0.55 + 0.45 * Math.sin(t * 7.3 + i * 1.7);
          alpha[i] = envelope * sparkle;
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
