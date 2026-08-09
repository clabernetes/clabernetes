import { HomeLayout } from 'fumadocs-ui/layouts/home';
import {
  ArrowRight,
  Boxes,
  Cable,
  Code2,
  Network,
  Rocket,
  Server,
  Sparkles,
} from 'lucide-react';
import { Link } from 'react-router';
import c9sLogo from '@/assets/c9s-logo-clean.png';
import containerlabMark from '@/assets/clab-mark.svg';
import kubernetesLogo from '@/assets/kubernetes-logo.svg';
import { SwitchIcon } from '@/components/switch-icon';
import { baseOptions } from '@/lib/layout.shared';

const highlights = [
  {
    icon: Network,
    eyebrow: 'Primary API',
    title: 'Node and Link first',
    description:
      'Model each network node and wire as an independently reconciled Kubernetes resource.',
    href: '/docs/concepts/nodes-and-links',
  },
  {
    icon: Boxes,
    eyebrow: 'Kubernetes native',
    title: 'Policy that composes',
    description:
      'Use launcher profiles, scheduling, persistence, services, and familiar cluster workflows.',
    href: '/docs/concepts/launcher-profiles',
  },
  {
    icon: Rocket,
    eyebrow: 'Easy adoption',
    title: 'Containerlab compatible',
    description:
      'Run existing topologies through the supported Topology compiler or emit primitive resources.',
    href: '/docs/concepts/topology',
  },
];

const architecture = [
  {
    icon: Boxes,
    label: 'Describe',
    detail: 'Topology or direct CRs',
  },
  {
    icon: Cable,
    label: 'Reconcile',
    detail: 'Nodes, links, and policy',
  },
  {
    icon: Server,
    label: 'Run',
    detail: 'One launcher per node',
  },
];

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
];

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

export default function Home() {
  return (
    <HomeLayout {...baseOptions()}>
      <title>c9s — containerlab on Kubernetes</title>
      <meta
        name="description"
        content="Run containerlab network topologies across a Kubernetes cluster."
      />

      <div className="c9s-home relative isolate overflow-hidden">
        <div
          aria-hidden="true"
          className="c9s-grid pointer-events-none absolute inset-0 -z-20"
        />

        <section className="relative mx-auto grid min-h-[calc(100vh-4rem)] w-full max-w-7xl items-center gap-12 px-6 py-20 lg:grid-cols-[1.1fr_0.9fr] lg:px-8 lg:py-24">
          <div className="relative z-10">
            <div className="mb-7 inline-flex items-center gap-2 rounded-full border border-fd-primary/20 bg-fd-primary/5 px-3 py-1.5 text-xs font-semibold tracking-wide text-fd-primary uppercase backdrop-blur">
              <Sparkles className="size-3.5" />
              containerlab, distributed
            </div>

            <h1 className="max-w-4xl text-5xl leading-[1.02] font-bold tracking-[-0.045em] text-balance sm:text-6xl lg:text-7xl xl:text-[5.4rem]">
              Distributed containerlabs,{' '}
              <span className="c9s-gradient-text">powered by Kubernetes.</span>
            </h1>

            <p className="mt-7 max-w-2xl text-lg leading-8 text-fd-muted-foreground md:text-xl">
              Clabernetes or c9s (<small><span className="italic">pronounced: see-nines</span></small>) turns every network node and wire into an independently
              reconciled resource, then runs your containerlab workloads across
              the cluster.
            </p>

            <div className="mt-10 flex flex-wrap gap-3">
              <Link
                className="group inline-flex items-center gap-2 rounded-xl bg-fd-primary px-5 py-3 font-semibold text-fd-primary-foreground shadow-lg shadow-fd-primary/20 transition hover:-translate-y-0.5 hover:shadow-xl hover:shadow-fd-primary/25 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-fd-primary"
                to="/docs/quickstart"
              >
                Launch your first lab
                <ArrowRight className="size-4 transition-transform group-hover:translate-x-0.5" />
              </Link>
              <Link
                className="inline-flex items-center rounded-xl border bg-fd-card/70 px-5 py-3 font-semibold backdrop-blur transition hover:-translate-y-0.5 hover:bg-fd-accent"
                to="/docs"
              >
                Explore the docs
              </Link>
              <a
                className="inline-flex items-center gap-2 px-3 py-3 text-sm font-medium text-fd-muted-foreground transition hover:text-fd-foreground"
                href="https://github.com/clabernetes/clabernetes"
                rel="noreferrer"
                target="_blank"
              >
                <Code2 className="size-4" />
                GitHub
              </a>
            </div>

            <div className="mt-12 flex flex-wrap gap-x-7 gap-y-3 text-sm text-fd-muted-foreground">
              {[
                'Node + Link API',
                'Topology compiler',
                'Kubernetes reconciliation',
              ].map((item) => (
                <span className="flex items-center gap-2" key={item}>
                  <span className="size-1.5 rounded-full bg-cyan-400 shadow-[0_0_8px_rgba(34,211,238,0.85)]" />
                  {item}
                </span>
              ))}
            </div>
          </div>

          <div className="relative mx-auto w-full max-w-[34rem]">
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
        </section>

        <section className="mx-auto w-full max-w-7xl px-6 pb-24 lg:px-8">
          <div className="grid overflow-hidden rounded-2xl border bg-fd-card/55 shadow-sm backdrop-blur md:grid-cols-3">
            {[
              ['One resource', 'per network node'],
              ['One resource', 'per point-to-point wire'],
              ['One cluster', 'for distributed labs'],
            ].map(([value, label], index) => (
              <div
                className={`px-6 py-6 text-center ${index > 0 ? 'border-t md:border-t-0 md:border-l' : ''
                  }`}
                key={label}
              >
                <p className="text-xl font-bold tracking-tight">{value}</p>
                <p className="mt-1 text-sm text-fd-muted-foreground">{label}</p>
              </div>
            ))}
          </div>
        </section>

        <section className="mx-auto w-full max-w-7xl px-6 py-20 lg:px-8">
          <div className="max-w-2xl">
            <p className="text-sm font-semibold tracking-wide text-fd-primary uppercase">
              Designed for real labs
            </p>
            <h2 className="mt-3 text-3xl font-bold tracking-tight sm:text-4xl">
              A Kubernetes API for network infrastructure.
            </h2>
            <p className="mt-4 text-lg leading-8 text-fd-muted-foreground">
              Keep containerlab vocabulary while gaining the reconciliation,
              policy, and distribution model of Kubernetes.
            </p>
          </div>

          <div className="mt-10 grid gap-5 md:grid-cols-3">
            {highlights.map(
              ({ icon: Icon, eyebrow, title, description, href }) => (
                <Link
                  className="group relative overflow-hidden rounded-2xl border bg-fd-card/65 p-7 shadow-sm backdrop-blur transition duration-300 hover:-translate-y-1 hover:border-fd-primary/35 hover:shadow-xl hover:shadow-fd-primary/5"
                  key={title}
                  to={href}
                >
                  <div
                    aria-hidden="true"
                    className="absolute -top-16 -right-16 size-36 rounded-full bg-fd-primary/0 blur-2xl transition group-hover:bg-fd-primary/10"
                  />
                  <div className="mb-8 flex size-11 items-center justify-center rounded-xl border bg-fd-background shadow-sm">
                    <Icon className="size-5 text-fd-primary" />
                  </div>
                  <p className="text-xs font-semibold tracking-wide text-fd-primary uppercase">
                    {eyebrow}
                  </p>
                  <h3 className="mt-2 text-xl font-semibold">{title}</h3>
                  <p className="mt-3 text-sm leading-6 text-fd-muted-foreground">
                    {description}
                  </p>
                  <span className="mt-7 inline-flex items-center gap-1.5 text-sm font-semibold">
                    Learn more
                    <ArrowRight className="size-4 transition-transform group-hover:translate-x-1" />
                  </span>
                </Link>
              ),
            )}
          </div>
        </section>

        <section className="mx-auto w-full max-w-7xl px-6 py-20 lg:px-8">
          <div className="rounded-3xl border bg-fd-card/60 p-7 shadow-sm backdrop-blur sm:p-10 lg:p-12">
            <div className="grid gap-10 lg:grid-cols-[0.7fr_1.3fr] lg:items-center">
              <div>
                <p className="text-sm font-semibold tracking-wide text-fd-primary uppercase">
                  How it works
                </p>
                <h2 className="mt-3 text-3xl font-bold tracking-tight">
                  From intent to running lab.
                </h2>
                <p className="mt-4 leading-7 text-fd-muted-foreground">
                  Author direct resources or bring an existing topology. c9s
                  handles the controller and launcher lifecycle.
                </p>
              </div>

              <div className="grid gap-3 sm:grid-cols-3">
                {architecture.map(({ icon: Icon, label, detail }, index) => (
                  <div className="relative" key={label}>
                    <div className="h-full rounded-2xl border bg-fd-background/80 p-5">
                      <div className="flex items-center justify-between">
                        <Icon className="size-5 text-fd-primary" />
                        <span className="font-mono text-xs text-fd-muted-foreground">
                          0{index + 1}
                        </span>
                      </div>
                      <p className="mt-7 font-semibold">{label}</p>
                      <p className="mt-1 text-sm text-fd-muted-foreground">
                        {detail}
                      </p>
                    </div>
                    {index < architecture.length - 1 ? (
                      <ArrowRight className="absolute top-1/2 -right-2.5 z-10 hidden size-5 rounded-full bg-fd-card text-fd-primary sm:block" />
                    ) : null}
                  </div>
                ))}
              </div>
            </div>
          </div>
        </section>

        <section className="mx-auto w-full max-w-5xl px-6 py-24 text-center lg:px-8">
          <p className="text-sm font-semibold tracking-wide text-fd-primary uppercase">
            Ready to build?
          </p>
          <h2 className="mt-3 text-4xl font-bold tracking-tight text-balance sm:text-5xl">
            Your first distributed lab is one quickstart away.
          </h2>
          <p className="mx-auto mt-5 max-w-2xl text-lg text-fd-muted-foreground">
            Launch a disposable KinD cluster, install c9s, and connect your first
            network nodes.
          </p>
          <Link
            className="group mt-8 inline-flex items-center gap-2 rounded-xl bg-fd-primary px-6 py-3.5 font-semibold text-fd-primary-foreground shadow-lg shadow-fd-primary/20 transition hover:-translate-y-0.5"
            to="/docs/quickstart"
          >
            Open the quickstart
            <ArrowRight className="size-4 transition-transform group-hover:translate-x-0.5" />
          </Link>
        </section>
      </div>
    </HomeLayout>
  );
}
