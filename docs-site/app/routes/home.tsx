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
import { IntroFabricDiagram } from '@/components/fabric-diagram';
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

          <IntroFabricDiagram />
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
