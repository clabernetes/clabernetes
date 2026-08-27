/**
 * Spinning 3D containerlab logo (model.gltf) rendered with Three.js.
 *
 * Ported from containerlab-app's empty-state attractor: auto-spin with
 * drag/tilt inertia, a particle burst on tap, an accent palette that
 * advances on every full rotation, and bubbles rising through the flask.
 *
 * Three.js is imported dynamically inside the effect so it stays out of
 * the initial homepage chunk. Falls back to the animated SVG logo when
 * WebGL is unavailable, and skips the auto-spin for reduced motion.
 */
import { useEffect, useRef, useState } from 'react';
import type * as ThreeNS from 'three';
import clabLogoAnimated from '@/assets/clab-logo-animated.svg';
import modelUrl from '@/assets/clab-logo.gltf?url';

export function ClabLogo3D({ className }: { className?: string }) {
  const threeContainerRef = useRef<HTMLDivElement>(null);
  const [webglUnavailable, setWebglUnavailable] = useState(false);

  useEffect(() => {
    const container = threeContainerRef.current;
    if (!container) return;

    let disposed = false;
    let cleanup: (() => void) | null = null;

    (async () => {
      const THREE = await import('three');
      const { GLTFLoader } = await import('three/addons/loaders/GLTFLoader.js');
      if (disposed) return;

      const reducedMotion = window.matchMedia(
        '(prefers-reduced-motion: reduce)',
      ).matches;

      const scene = new THREE.Scene();
      const camera = new THREE.PerspectiveCamera(40, 1, 0.1, 1000);
      camera.position.set(0, 0, 4.2);

      let renderer: ThreeNS.WebGLRenderer;
      try {
        renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
      } catch {
        setWebglUnavailable(true);
        return;
      }
      renderer.setClearColor(0x000000, 0);
      renderer.setPixelRatio(window.devicePixelRatio);
      renderer.toneMapping = THREE.ACESFilmicToneMapping;
      renderer.toneMappingExposure = 1.2;
      container.appendChild(renderer.domElement);

      // Lighting
      const ambientLight = new THREE.AmbientLight(0xffffff, 0.4);
      scene.add(ambientLight);

      const keyLight = new THREE.DirectionalLight(0xffffff, 1.5);
      keyLight.position.set(3, 3, 5);
      scene.add(keyLight);

      const fillLight = new THREE.DirectionalLight(0x8ec8e8, 0.4);
      fillLight.position.set(-4, 1, 2);
      scene.add(fillLight);

      const rimLight = new THREE.DirectionalLight(0x00c9ff, 0.8);
      rimLight.position.set(-2, -1, -4);
      scene.add(rimLight);

      const glowLight = new THREE.PointLight(0x00c9ff, 1.0, 8);
      glowLight.position.set(0, -0.2, 0.8);
      scene.add(glowLight);

      const bounceLight = new THREE.PointLight(0x00c9ff, 0.3, 6);
      bounceLight.position.set(0, -2, 1);
      scene.add(bounceLight);

      // Model + materials
      let model: ThreeNS.Object3D | null = null;
      interface BubbleAnim {
        mesh: ThreeNS.Mesh;
        originX: number;
        originY: number;
        originZ: number;
        speed: number;
        phase: number;
        range: number;
        endOpacity: number;
      }
      const bubbleMeshes: BubbleAnim[] = [];

      const containerMaterial = new THREE.MeshStandardMaterial({
        color: 0x889099,
        metalness: 0.5,
        roughness: 0.35,
      });

      const accentPalette = [
        { color: 0x00c9ff, emissive: 0x003344, bubbleEmissive: 0x002233 },
        { color: 0xb077ff, emissive: 0x2f1a4f, bubbleEmissive: 0x23173b },
        { color: 0x54d58b, emissive: 0x123827, bubbleEmissive: 0x0f2b1f },
        { color: 0xffa54b, emissive: 0x4a290d, bubbleEmissive: 0x341d0a },
        { color: 0xff6fa9, emissive: 0x4a1730, bubbleEmissive: 0x351224 },
        { color: 0x6f8dff, emissive: 0x1d2a54, bubbleEmissive: 0x161f3d },
      ] as const;
      const SPIN_SPEED = reducedMotion ? 0 : 0.5;
      const DRAG_ROTATION_SPEED = 0.01;
      const MAX_TILT = 0.6;
      const FULL_SPIN = Math.PI * 2;
      const BURST_PARTICLE_COUNT = 210;
      const BURST_DURATION = 0.8;
      const BASE_CYAN_EMISSIVE_INTENSITY = 0.3;
      const BASE_BUBBLE_EMISSIVE_INTENSITY = 0.2;

      const cyanMaterial = new THREE.MeshStandardMaterial({
        color: 0x00c9ff,
        metalness: 0.15,
        roughness: 0.3,
        transparent: true,
        opacity: 0.9,
        emissive: 0x003344,
        emissiveIntensity: BASE_CYAN_EMISSIVE_INTENSITY,
      });

      const bubbleMaterial = new THREE.MeshStandardMaterial({
        color: 0x00c9ff,
        metalness: 0.05,
        roughness: 0.2,
        transparent: true,
        opacity: 0.85,
        emissive: 0x002233,
        emissiveIntensity: BASE_BUBBLE_EMISSIVE_INTENSITY,
      });

      const burstGeometry = new THREE.BufferGeometry();
      const burstPositions = new Float32Array(BURST_PARTICLE_COUNT * 3);
      const burstVelocities = new Float32Array(BURST_PARTICLE_COUNT * 3);
      burstGeometry.setAttribute(
        'position',
        new THREE.BufferAttribute(burstPositions, 3),
      );
      const burstMaterial = new THREE.PointsMaterial({
        color: 0x00c9ff,
        size: 0.12,
        sizeAttenuation: true,
        transparent: true,
        opacity: 0,
        blending: THREE.AdditiveBlending,
        depthWrite: false,
      });
      const burstPoints = new THREE.Points(burstGeometry, burstMaterial);
      burstPoints.position.set(0, -0.12, 0.2);
      burstPoints.visible = false;
      scene.add(burstPoints);

      const ignitionLight = new THREE.PointLight(0x00c9ff, 0, 10);
      ignitionLight.position.set(0, -0.2, 0.9);
      scene.add(ignitionLight);

      let accentPaletteIndex = 0;
      let accumulatedRotationForPalette = 0;
      let previousTotalRotationY = 0;
      let hasRotationSample = false;
      const rimHsl = { h: 0.52, s: 0.8, l: 0.6 };
      let autoRotationY = 0;
      let userRotationY = 0;
      let userTiltX = 0;
      let inertiaY = 0;
      let inertiaX = 0;
      let dragging = false;
      let activePointerId: number | null = null;
      let lastPointerX = 0;
      let lastPointerY = 0;
      let dragDistance = 0;
      let burstActive = false;
      let burstElapsed = BURST_DURATION;
      let igniteBoost = 0;

      const clamp = (value: number, min: number, max: number) =>
        Math.min(max, Math.max(min, value));

      const applyAccentPalette = (index: number) => {
        const accent = accentPalette[index % accentPalette.length];
        cyanMaterial.color.setHex(accent.color);
        cyanMaterial.emissive.setHex(accent.emissive);

        for (const bubble of bubbleMeshes) {
          const bubbleMat = bubble.mesh
            .material as ThreeNS.MeshStandardMaterial;
          bubbleMat.color.setHex(accent.color);
          bubbleMat.emissive.setHex(accent.bubbleEmissive);
        }

        const accentColor = new THREE.Color(accent.color);
        glowLight.color.copy(accentColor);
        bounceLight.color.copy(accentColor.clone().offsetHSL(0, -0.1, -0.08));
        burstMaterial.color.copy(accentColor);
        ignitionLight.color.copy(accentColor);

        accentColor.getHSL(rimHsl);
        rimHsl.s = Math.max(0.65, rimHsl.s * 0.9);
        rimHsl.l = Math.min(0.72, Math.max(0.55, rimHsl.l + 0.08));
        rimLight.color.setHSL(rimHsl.h, rimHsl.s, rimHsl.l);
      };

      applyAccentPalette(accentPaletteIndex);

      const igniteBurst = () => {
        burstActive = true;
        burstElapsed = 0;
        igniteBoost = Math.max(igniteBoost, 1.65);
        burstPoints.visible = true;
        burstMaterial.opacity = 1.0;
        burstMaterial.size = 0.12;

        for (let i = 0; i < BURST_PARTICLE_COUNT; i++) {
          const i3 = i * 3;
          const theta = Math.random() * Math.PI * 2;
          const z = Math.random() * 2 - 1;
          const radial = Math.sqrt(Math.max(0, 1 - z * z));
          const rawX = radial * Math.cos(theta);
          const rawY = Math.abs(z) * 0.9 + 0.1;
          const rawZ = radial * Math.sin(theta);
          const length = Math.hypot(rawX, rawY, rawZ) || 1;
          const speed = 2.2 + Math.random() * 3.4;
          const spread = Math.random() * 0.16;

          burstPositions[i3] = (Math.random() - 0.5) * spread;
          burstPositions[i3 + 1] = (Math.random() - 0.5) * 0.14;
          burstPositions[i3 + 2] = (Math.random() - 0.5) * spread;
          burstVelocities[i3] = (rawX / length) * speed;
          burstVelocities[i3 + 1] = (rawY / length) * speed;
          burstVelocities[i3 + 2] = (rawZ / length) * speed;
        }

        burstGeometry.attributes.position.needsUpdate = true;
      };

      container.style.cursor = 'grab';
      container.style.touchAction = 'none';

      const onPointerDown = (event: PointerEvent) => {
        if (activePointerId !== null) return;
        dragging = true;
        activePointerId = event.pointerId;
        lastPointerX = event.clientX;
        lastPointerY = event.clientY;
        dragDistance = 0;
        inertiaX = 0;
        inertiaY = 0;
        container.style.cursor = 'grabbing';
        container.setPointerCapture?.(event.pointerId);
        event.preventDefault();
      };

      const onPointerMove = (event: PointerEvent) => {
        if (!dragging || event.pointerId !== activePointerId) return;

        const dx = event.clientX - lastPointerX;
        const dy = event.clientY - lastPointerY;
        dragDistance += Math.abs(dx) + Math.abs(dy);
        lastPointerX = event.clientX;
        lastPointerY = event.clientY;

        const deltaY = dx * DRAG_ROTATION_SPEED;
        const deltaX = dy * DRAG_ROTATION_SPEED;
        userRotationY += deltaY;
        userTiltX = clamp(userTiltX + deltaX, -MAX_TILT, MAX_TILT);
        inertiaY = deltaY;
        inertiaX = deltaX;
      };

      const releasePointer = (event: PointerEvent) => {
        if (event.pointerId !== activePointerId) return;

        dragging = false;
        activePointerId = null;
        container.style.cursor = 'grab';
        container.releasePointerCapture?.(event.pointerId);

        // Tiny flick so quick taps still feel responsive.
        if (dragDistance < 6) {
          inertiaY += 0.08;
          igniteBurst();
        }
      };

      container.addEventListener('pointerdown', onPointerDown);
      window.addEventListener('pointermove', onPointerMove);
      window.addEventListener('pointerup', releasePointer);
      window.addEventListener('pointercancel', releasePointer);

      const loader = new GLTFLoader();
      loader.load(modelUrl, (gltf) => {
        if (disposed) return;
        model = gltf.scene;

        let meshIndex = 0;
        model.traverse((child) => {
          if ((child as ThreeNS.Mesh).isMesh) {
            const mesh = child as ThreeNS.Mesh;
            if (meshIndex === 0) {
              mesh.material = cyanMaterial;
            } else if (meshIndex === 1) {
              mesh.material = containerMaterial;
            } else if (meshIndex >= 2 && meshIndex <= 4) {
              mesh.material = bubbleMaterial.clone();
              const bi = meshIndex - 2;
              const configs: [number, number, number][] = [
                [1.0, 15, 0.68], // small
                [0.8, 12, 0.6], // mid
                [0.6, 5, 0.06], // big
              ];
              const [speed, range, endOp] = configs[bi];
              bubbleMeshes.push({
                mesh,
                originX: mesh.position.x,
                originY: mesh.position.y,
                originZ: mesh.position.z,
                speed,
                phase: bi * 1.8,
                range,
                endOpacity: endOp,
              });
            }
            meshIndex++;
          }
        });

        applyAccentPalette(accentPaletteIndex);

        const box = new THREE.Box3().setFromObject(model);
        const center = box.getCenter(new THREE.Vector3());
        const size = box.getSize(new THREE.Vector3());
        model.position.sub(center);
        const maxDim = Math.max(size.x, size.y, size.z);
        model.scale.setScalar(2.2 / maxDim);

        scene.add(model);
      });

      // Resize
      const resize = () => {
        const rect = container.getBoundingClientRect();
        if (rect.width === 0 || rect.height === 0) return;
        camera.aspect = rect.width / rect.height;
        camera.updateProjectionMatrix();
        renderer.setSize(rect.width, rect.height);
      };
      resize();
      const resizeObserver = new ResizeObserver(resize);
      resizeObserver.observe(container);

      // Animation loop
      let animId = 0;
      const timer = new THREE.Timer();
      timer.connect(document);

      const animate = (timestamp?: number) => {
        animId = requestAnimationFrame(animate);
        timer.update(timestamp);
        const delta = timer.getDelta();
        const elapsed = timer.getElapsed();

        if (model) {
          autoRotationY += SPIN_SPEED * delta;

          if (!dragging) {
            const damping = Math.exp(-6 * delta);
            userRotationY += inertiaY;
            userTiltX = clamp(userTiltX + inertiaX, -MAX_TILT, MAX_TILT);
            inertiaY *= damping;
            inertiaX *= damping;
            if (Math.abs(inertiaY) < 0.00005) inertiaY = 0;
            if (Math.abs(inertiaX) < 0.00005) inertiaX = 0;
          }

          const totalRotationY = autoRotationY + userRotationY;
          model.rotation.y = totalRotationY;
          model.rotation.x = userTiltX * 0.35;
          model.rotation.z = -userTiltX * 0.08;

          if (!hasRotationSample) {
            previousTotalRotationY = totalRotationY;
            hasRotationSample = true;
          } else {
            accumulatedRotationForPalette += Math.abs(
              totalRotationY - previousTotalRotationY,
            );
            previousTotalRotationY = totalRotationY;
          }

          while (accumulatedRotationForPalette >= FULL_SPIN) {
            accumulatedRotationForPalette -= FULL_SPIN;
            accentPaletteIndex = (accentPaletteIndex + 1) % accentPalette.length;
            applyAccentPalette(accentPaletteIndex);
          }
        }

        // Bubbles rising through the flask
        if (!reducedMotion) {
          for (const b of bubbleMeshes) {
            const cycle = 3 / b.speed;
            const t = ((elapsed * b.speed + b.phase) % cycle) / cycle;
            b.mesh.position.y = b.originY - t * b.range;
            b.mesh.position.x = b.originX;
            b.mesh.position.z = b.originZ;
            b.mesh.scale.setScalar(1);
            const mat = b.mesh.material as ThreeNS.MeshStandardMaterial;
            mat.opacity = 1 + (b.endOpacity - 1) * t;
          }
        }

        const pulse = reducedMotion ? 0 : Math.sin(elapsed * 1.5);
        if (burstActive) {
          burstElapsed += delta;
          const progress = Math.min(1, burstElapsed / BURST_DURATION);
          const fade = 1 - progress;
          const damping = Math.exp(-1.8 * delta);

          for (let i = 0; i < BURST_PARTICLE_COUNT; i++) {
            const i3 = i * 3;
            burstVelocities[i3 + 1] -= 3.5 * delta;
            burstVelocities[i3] *= damping;
            burstVelocities[i3 + 1] *= damping;
            burstVelocities[i3 + 2] *= damping;
            burstPositions[i3] += burstVelocities[i3] * delta;
            burstPositions[i3 + 1] += burstVelocities[i3 + 1] * delta;
            burstPositions[i3 + 2] += burstVelocities[i3 + 2] * delta;
          }
          burstGeometry.attributes.position.needsUpdate = true;
          burstMaterial.opacity = fade * fade;
          burstMaterial.size = 0.045 + 0.16 * fade;
          igniteBoost = Math.max(igniteBoost, 1.2 * fade);

          if (progress >= 1) {
            burstActive = false;
            burstPoints.visible = false;
          }
        }

        igniteBoost *= Math.exp(-4.8 * delta);
        const ignite = igniteBoost * igniteBoost;

        glowLight.intensity = 0.7 + 0.5 * pulse + ignite * 4.0;
        bounceLight.intensity = 0.2 + 0.15 * pulse + ignite * 1.65;
        rimLight.intensity = 0.8 + ignite * 1.35;
        ignitionLight.intensity = ignite * 10.5;
        cyanMaterial.emissiveIntensity =
          BASE_CYAN_EMISSIVE_INTENSITY + ignite * 1.35;
        for (const b of bubbleMeshes) {
          const bubbleMat = b.mesh.material as ThreeNS.MeshStandardMaterial;
          bubbleMat.emissiveIntensity =
            BASE_BUBBLE_EMISSIVE_INTENSITY + ignite * 0.95;
        }
        const rimHue = (rimHsl.h + 0.018 * Math.sin(elapsed * 0.7) + 1) % 1;
        rimLight.color.setHSL(rimHue, rimHsl.s, rimHsl.l);

        renderer.render(scene, camera);
      };
      animate();

      cleanup = () => {
        cancelAnimationFrame(animId);
        resizeObserver.disconnect();
        timer.dispose();
        container.removeEventListener('pointerdown', onPointerDown);
        window.removeEventListener('pointermove', onPointerMove);
        window.removeEventListener('pointerup', releasePointer);
        window.removeEventListener('pointercancel', releasePointer);
        burstGeometry.dispose();
        burstMaterial.dispose();
        renderer.dispose();
        if (renderer.domElement.parentElement === container) {
          container.removeChild(renderer.domElement);
        }
      };
      if (disposed) {
        cleanup();
        cleanup = null;
      }
    })().catch(() => {
      setWebglUnavailable(true);
    });

    return () => {
      disposed = true;
      cleanup?.();
      cleanup = null;
    };
  }, []);

  return (
    <div aria-hidden="true" className={className}>
      <div
        className="size-full"
        ref={threeContainerRef}
        style={{ display: webglUnavailable ? 'none' : 'block' }}
      />
      {webglUnavailable ? (
        <img alt="" className="mx-auto w-3/4" src={clabLogoAnimated} />
      ) : null}
    </div>
  );
}
