import c9sLogo from '@/assets/c9s-logo-clean.png';
import { useEffect, useRef, type CSSProperties } from 'react';
import './release-hero.css';

type ReleaseHeroProps = {
  /** Version the counter starts from, same length as `version`. */
  from: string;
  version: string;
  /** Tagline words; the word equal to `highlight` is accented. */
  tagline: string;
  highlight?: string;
  facts?: string[];
};

type MeshNode = { x: number; y: number; vx: number; vy: number; r: number; hot: boolean; p: number };

// Drifting topology nodes linked when close, drawn behind the banner.
function useMesh(canvasRef: React.RefObject<HTMLCanvasElement | null>) {
  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext('2d');
    if (!canvas || !ctx) return;

    const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
    let width = 0;
    let height = 0;
    let nodes: MeshNode[] = [];
    let frame = 0;
    let visible = true;
    const start = performance.now();

    const resize = () => {
      const rect = canvas.getBoundingClientRect();
      const dpr = window.devicePixelRatio || 1;
      width = rect.width;
      height = rect.height;
      canvas.width = width * dpr;
      canvas.height = height * dpr;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      nodes = Array.from({ length: Math.round((width * height) / 14000) }, () => ({
        x: Math.random() * width,
        y: Math.random() * height,
        vx: (Math.random() - 0.5) * 0.18,
        vy: (Math.random() - 0.5) * 0.18,
        r: 1 + Math.random() * 1.8,
        hot: Math.random() < 0.25,
        p: Math.random() * Math.PI * 2,
      }));
      if (reduce) draw(performance.now());
    };

    const draw = (now: number) => {
      const t = (now - start) / 1000;
      const fade = reduce ? 1 : Math.min(1, t / 1.5);
      const reach = Math.max(90, width / 9);
      ctx.clearRect(0, 0, width, height);
      for (const n of nodes) {
        if (reduce) break;
        n.x += n.vx;
        n.y += n.vy;
        if (n.x < 0 || n.x > width) n.vx *= -1;
        if (n.y < 0 || n.y > height) n.vy *= -1;
      }
      ctx.lineWidth = 1;
      for (let i = 0; i < nodes.length; i++) {
        for (let j = i + 1; j < nodes.length; j++) {
          const d = Math.hypot(nodes[i].x - nodes[j].x, nodes[i].y - nodes[j].y);
          if (d >= reach) continue;
          ctx.strokeStyle = `rgba(32,217,231,${(1 - d / reach) * 0.16 * fade})`;
          ctx.beginPath();
          ctx.moveTo(nodes[i].x, nodes[i].y);
          ctx.lineTo(nodes[j].x, nodes[j].y);
          ctx.stroke();
        }
      }
      for (const n of nodes) {
        const glow = n.hot ? 0.55 + 0.45 * Math.sin(t * 2 + n.p) : 0.35;
        ctx.fillStyle = n.hot ? `rgba(244,63,141,${glow * fade})` : `rgba(157,145,173,${glow * fade})`;
        ctx.beginPath();
        ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2);
        ctx.fill();
      }
    };

    const loop = (now: number) => {
      draw(now);
      if (visible) frame = requestAnimationFrame(loop);
    };

    const resizeObserver = new ResizeObserver(resize);
    resizeObserver.observe(canvas);
    resize();

    // Only animate while the banner is on screen.
    const intersection = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting;
      cancelAnimationFrame(frame);
      if (visible && !reduce) frame = requestAnimationFrame(loop);
    });
    intersection.observe(canvas);

    return () => {
      cancelAnimationFrame(frame);
      resizeObserver.disconnect();
      intersection.disconnect();
    };
  }, [canvasRef]);
}

export function ReleaseHero({ from, version, tagline, highlight, facts = [] }: ReleaseHeroProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  useMesh(canvasRef);

  const chars = [...version];
  const glyph = (i: number): CSSProperties =>
    ({ '--i': i, '--n': chars.length }) as CSSProperties;

  return (
    <figure className="c9s-release-hero not-prose" aria-label={`clabernetes ${version}: ${tagline}`}>
      <canvas ref={canvasRef} className="c9s-release-hero-mesh" aria-hidden="true" />
      <div className="c9s-release-hero-layout">
        <div className="c9s-release-hero-beaker" aria-hidden="true">
          <div className="c9s-release-hero-float">
            <img src={c9sLogo} alt="" />
            <div className="c9s-release-hero-shine" style={{ maskImage: `url(${c9sLogo})`, WebkitMaskImage: `url(${c9sLogo})` }} />
            <div className="c9s-release-hero-glint" />
          </div>
        </div>
        <div className="c9s-release-hero-copy">
          <p className="c9s-release-hero-eyebrow">
            clabernetes <b>release</b>
          </p>
          <p className="c9s-release-hero-version">
            <span className="sr-only">{version}</span>
            <span className="c9s-release-hero-glyphs" aria-hidden="true">
              {chars.map((c, i) =>
                c === from[i] ? (
                  <span key={i} style={glyph(i)}>{c}</span>
                ) : (
                  <span key={i} className="c9s-release-hero-swap">
                    <span className="c9s-release-hero-old" style={glyph(i)}>{from[i]}</span>
                    <span className="c9s-release-hero-new" style={glyph(i)}>{c}</span>
                  </span>
                ),
              )}
            </span>
          </p>
          <p className="c9s-release-hero-tagline">
            {tagline.split(' ').map((word, i) => (
              <span
                key={i}
                className={word === highlight ? 'c9s-release-hero-accent' : undefined}
                style={{ '--w': i } as CSSProperties}
              >
                {word}
              </span>
            ))}
          </p>
          {facts.length > 0 && (
            <ul className="c9s-release-hero-facts">
              {facts.map((fact, i) => (
                <li key={fact} style={{ '--f': i } as CSSProperties}>
                  {fact}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </figure>
  );
}
