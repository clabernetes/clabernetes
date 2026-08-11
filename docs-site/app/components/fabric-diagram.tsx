import c9sLogo from '@/assets/c9s-logo-clean.png';
import containerlabMark from '@/assets/clab-mark.svg';
import kubernetesLogo from '@/assets/kubernetes-logo.svg';
import { SwitchIcon } from '@/components/switch-icon';

const fabricNodes = [
  {
    name: 'spine-01',
    position: 'top-0 left-[15%]',
    tone: 'text-fuchsia-500',
  },
  {
    name: 'spine-02',
    position: 'top-0 right-[15%]',
    tone: 'text-fuchsia-500',
  },
  {
    name: 'leaf-01',
    position: 'bottom-0 left-0',
    tone: 'text-cyan-500',
  },
  {
    name: 'leaf-02',
    position: 'bottom-0 left-[36.5%]',
    tone: 'text-cyan-500',
  },
  {
    name: 'leaf-03',
    position: 'right-0 bottom-0',
    tone: 'text-cyan-500',
  },
] as const;

const fabricLinks = [
  [28.5, 36, 13.5, 64],
  [28.5, 36, 50, 64],
  [28.5, 36, 86.5, 64],
  [71.5, 36, 13.5, 64],
  [71.5, 36, 50, 64],
  [71.5, 36, 86.5, 64],
] as const;

function FabricNode({
  name,
  position,
  tone,
}: (typeof fabricNodes)[number]) {
  return (
    <div
      className={`c9s-fabric-node absolute z-20 flex h-[36%] w-[27%] flex-col overflow-hidden rounded-xl border bg-fd-background/90 p-2 shadow-lg backdrop-blur sm:rounded-2xl sm:p-3 ${position}`}
    >
      <div className="relative flex items-center justify-between gap-1">
        <span className="flex items-center gap-1 text-[8px] font-medium text-fd-muted-foreground sm:text-[10px]">
          <img
            alt=""
            aria-hidden="true"
            className="size-3 sm:size-3.5"
            src={kubernetesLogo}
          />
          <span>pod</span>
        </span>
        <span className="flex items-center gap-1 text-[8px] font-medium text-fd-muted-foreground sm:text-[10px]">
          <img
            alt=""
            aria-hidden="true"
            className="size-3 sm:size-3.5"
            src={containerlabMark}
          />
          <span>clab</span>
        </span>
      </div>
      <SwitchIcon
        className={`relative mx-auto mt-1.5 size-8 shadow-sm sm:mt-3 sm:size-11 ${tone}`}
      />
      <p className="mt-1 truncate text-center font-mono text-[8px] font-semibold sm:mt-1.5 sm:text-[10px]">
        {name}
      </p>
    </div>
  );
}

export function IntroFabricDiagram() {
  return (
    <div className="not-prose relative mx-auto w-full max-w-[34rem]">
      <div
        aria-hidden="true"
        className="absolute inset-12 -z-10 rounded-full bg-cyan-400/20 blur-3xl dark:bg-cyan-400/15"
      />
      <div
        aria-hidden="true"
        className="absolute -top-8 right-4 -z-10 size-48 rounded-full bg-fuchsia-500/20 blur-3xl"
      />

      <div className="c9s-fabric-stage relative aspect-square overflow-hidden rounded-[2.25rem] border border-white/50 bg-white/55 shadow-2xl shadow-cyan-950/10 backdrop-blur-xl dark:border-white/10 dark:bg-white/[0.035] dark:shadow-black/30">
        <div
          aria-hidden="true"
          className="absolute inset-0 bg-gradient-to-br from-fuchsia-500/10 via-transparent to-cyan-400/15"
        />

        <div className="absolute inset-x-5 top-5 z-30 flex items-center justify-between gap-3 sm:inset-x-7 sm:top-7">
          <div className="flex min-w-0 items-center gap-2.5">
            <div className="flex size-9 shrink-0 items-center justify-center rounded-xl border bg-fd-background/80 p-1.5 shadow-sm backdrop-blur">
              <img
                alt=""
                aria-hidden="true"
                className="size-full object-contain"
                src={c9sLogo}
              />
            </div>
            <div className="min-w-0">
              <p className="truncate text-xs font-semibold sm:text-sm">
                Distributed labs
              </p>
              <p className="truncate text-[9px] text-fd-muted-foreground sm:text-[10px]">
                Declaratively defined, intelligently distributed
              </p>
            </div>
          </div>
          <span className="shrink-0 rounded-full border bg-fd-background/75 px-2 py-1 text-[9px] font-semibold text-fd-muted-foreground backdrop-blur sm:px-2.5 sm:text-[10px]">
            A pod per network node
          </span>
        </div>

        <div
          aria-label="A leaf-spine datacenter fabric with two spine switches and three leaf switches distributed across five Kubernetes pods, each running containerlab"
          className="absolute inset-x-5 top-[21%] bottom-5 sm:inset-x-7 sm:bottom-7"
          role="img"
        >
          <svg
            aria-hidden="true"
            className="absolute inset-0 z-10 size-full overflow-visible"
            preserveAspectRatio="none"
            viewBox="0 0 100 100"
          >
            {fabricLinks.map(([x1, y1, x2, y2]) => (
              <line
                className="c9s-fabric-link"
                key={`${x1}-${x2}`}
                pathLength="1"
                x1={x1}
                x2={x2}
                y1={y1}
                y2={y2}
              />
            ))}
          </svg>
          {fabricNodes.map((node) => (
            <FabricNode key={node.name} {...node} />
          ))}
        </div>
      </div>
    </div>
  );
}
