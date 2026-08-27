import c9sLogo from '@/assets/c9s-logo-clean.png';
import kubernetesLogo from '@/assets/kubernetes-logo.svg';
import { SwitchIcon } from '@/components/switch-icon';
import { Laptop } from 'lucide-react';

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
      className={`c9s-fabric-node absolute z-20 flex h-[36%] w-[27%] flex-col overflow-hidden rounded-xl border bg-fd-background p-2 shadow-sm sm:rounded-2xl sm:p-3 ${position}`}
    >
      <div className="relative flex items-center gap-1">
        <span className="flex items-center gap-1 text-[8px] font-medium text-fd-muted-foreground sm:text-[10px]">
          <img
            alt=""
            aria-hidden="true"
            className="size-3 sm:size-3.5"
            src={kubernetesLogo}
          />
          <span>pod</span>
        </span>
      </div>
      <SwitchIcon
        className={`relative mx-auto mt-1.5 size-8 sm:mt-3 sm:size-11 ${tone}`}
      />
      <p className="mt-1 truncate text-center font-mono text-[8px] font-semibold sm:mt-1.5 sm:text-[10px]">
        {name}
      </p>
    </div>
  );
}

/**
 * `fill` makes the stage take the height of its grid cell instead of its own
 * aspect ratio, so it can align exactly with the column beside it.
 */
export function IntroFabricDiagram({ fill = false }: { fill?: boolean }) {
  return (
    <div
      className={`not-prose relative mx-auto w-full ${
        fill ? 'h-full max-w-[32rem]' : 'max-w-[30rem]'
      }`}
    >
      <div
        className={`c9s-fabric-stage relative overflow-hidden rounded-2xl border bg-fd-card shadow-md ${
          fill ? 'h-full min-h-[24rem]' : 'aspect-[6/5]'
        }`}
      >
        <div className="absolute inset-x-5 top-5 z-30 flex items-center justify-between gap-3 sm:inset-x-7 sm:top-7">
          <div className="flex min-w-0 items-center gap-2.5">
            <div className="flex size-9 shrink-0 items-center justify-center rounded-xl border bg-fd-background p-1.5">
              <img
                alt=""
                aria-hidden="true"
                className="size-full object-contain"
                src={c9sLogo}
              />
            </div>
            <div className="min-w-0">
              <p className="truncate text-xs font-semibold sm:text-sm">
                Distributed lab
              </p>
              <p className="truncate text-[9px] text-fd-muted-foreground sm:text-[10px]">
                One device Pod per network node
              </p>
            </div>
          </div>
        </div>

        <div
          aria-label="A leaf-spine datacenter fabric with two spine switches and three leaf switches, each running in its own Kubernetes pod"
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

const tryC9sNodes = [
  {
    name: 'srl1',
    kind: 'Nokia SR Linux',
    position: 'top-1/2 left-0 -translate-y-1/2',
    tone: 'text-cyan-500',
    icon: SwitchIcon,
    interfaceName: 'e1-1',
    interfaceAddress: '192.0.2.0/31',
    interfacePosition: 'left-full',
    interfaceAlignment: 'items-start text-left',
    interfaceConnectorPosition: '-right-1.5',
  },
  {
    name: 'multitool',
    kind: 'Linux client',
    position: 'top-1/2 right-0 -translate-y-1/2',
    tone: 'text-emerald-500',
    icon: Laptop,
    interfaceName: 'eth1',
    interfaceAddress: '192.0.2.1/31',
    interfacePosition: 'right-full',
    interfaceAlignment: 'items-end text-right',
    interfaceConnectorPosition: '-left-1.5',
  },
] as const;

function TryC9sNode({
  name,
  kind,
  position,
  tone,
  icon: Icon,
  interfaceName,
  interfaceAddress,
  interfacePosition,
  interfaceAlignment,
  interfaceConnectorPosition,
}: (typeof tryC9sNodes)[number]) {
  return (
    <div
      className={`c9s-fabric-node absolute z-20 flex aspect-square w-[28%] flex-col items-center justify-center overflow-visible rounded-xl border bg-fd-background p-2 shadow-sm sm:w-[18%] sm:rounded-2xl sm:px-3 sm:py-4 ${position}`}
    >
      <span className="text-center text-[8px] font-medium text-fd-muted-foreground sm:text-[10px]">
        {kind}
      </span>
      <div className="mt-2 flex size-9 items-center justify-center rounded-lg border bg-fd-background p-1.5 sm:mt-3 sm:size-11">
        <Icon className={`size-full ${tone}`} />
      </div>
      <p className="mt-2 text-center font-mono text-[9px] leading-none font-semibold sm:text-[10px]">
        {name}
      </p>
      <span
        className={`absolute top-1/2 ${interfacePosition} z-30 flex -translate-y-1/2 flex-col rounded-md border bg-fd-background px-1.5 py-1 font-mono text-[8px] font-semibold leading-tight shadow-sm sm:text-[9px] ${interfaceAlignment} ${tone}`}
      >
        <span>{interfaceName}</span>
        <span className="font-normal text-fd-muted-foreground">
          {interfaceAddress}
        </span>
        <span
          aria-hidden="true"
          className={`absolute top-1/2 ${interfaceConnectorPosition} size-2 -translate-y-1/2 rounded-full bg-current ring-2 ring-fd-background`}
        />
      </span>
    </div>
  );
}

export function TryC9sDiagram() {
  return (
    <div className="not-prose relative w-full">
      <div className="c9s-polka-canvas relative aspect-[4/3] w-full overflow-hidden rounded-2xl border sm:aspect-[3/1]">
        <div className="absolute inset-x-5 bottom-5 z-30 text-center sm:inset-x-7 sm:bottom-7">
          <p className="truncate text-xs font-semibold sm:text-sm">
            try-c9s sample topology
          </p>
          <p className="truncate text-[9px] text-fd-muted-foreground sm:text-[10px]">
            One switch, one client, one link
          </p>
        </div>

        <div
          aria-label="The try-c9s sample topology connects srl1, a Nokia SR Linux switch, to multitool, a Linux client, over srl1 e1-1 and multitool eth1."
          className="absolute inset-x-5 top-4 bottom-[30%] sm:inset-x-7 sm:top-5 sm:bottom-[28%]"
          role="img"
        >
          <svg
            aria-hidden="true"
            className="absolute inset-0 z-10 size-full overflow-visible"
            preserveAspectRatio="none"
            viewBox="0 0 100 100"
          >
            <line
              className="c9s-try-c9s-link sm:hidden"
              pathLength="1"
              x1="28"
              x2="72"
              y1="50"
              y2="50"
            />
            <line
              className="c9s-try-c9s-link hidden sm:block"
              pathLength="1"
              x1="18"
              x2="82"
              y1="50"
              y2="50"
            />
          </svg>
          <span className="absolute top-[62%] left-1/2 z-30 -translate-x-1/2 rounded-md border bg-fd-background/90 px-1.5 py-0.5 text-[8px] font-medium text-fd-muted-foreground shadow-sm sm:text-[9px]">
            point-to-point link
          </span>
          {tryC9sNodes.map((node) => (
            <TryC9sNode key={node.name} {...node} />
          ))}
        </div>
      </div>
    </div>
  );
}
