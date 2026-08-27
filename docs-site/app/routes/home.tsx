import { HomeLayout } from 'fumadocs-ui/layouts/home';
import {
  ArrowRight,
  Boxes,
  Cable,
  Code2,
  Network,
  Rocket,
  Server,
} from 'lucide-react';
import { Link } from 'react-router';
import c9sLogo from '@/assets/c9s-logo-clean.png';
import { ClabLogo3D } from '@/components/clab-logo-3d';
import { IntroFabricDiagram } from '@/components/fabric-diagram';
import { baseOptions } from '@/lib/layout.shared';

const accent = 'text-cyan-600 dark:text-cyan-400';

const highlights = [
  {
    icon: Network,
    eyebrow: 'Primary API',
    title: 'One resource per node and wire',
    description:
      'Each network node and each point-to-point link is an independently reconciled Kubernetes resource, so labs are not bounded by one aggregate object.',
    href: '/docs/concepts/nodes-and-links',
  },
  {
    icon: Boxes,
    eyebrow: 'Kubernetes native',
    title: 'Kubernetes manages the lab',
    description:
      'Node profiles carry resources, scheduling, persistence, and service exposure. The cluster owns image pulls, lifecycle, logs, and status.',
    href: '/docs/concepts/node-profiles',
  },
  {
    icon: Rocket,
    eyebrow: 'Easy adoption',
    title: 'Containerlab compatible',
    description:
      'Embed a containerlab definition in a Topology resource, or generate Node and Link manifests from a topology file with clabverter.',
    href: '/docs/concepts/topology',
  },
];

const steps = [
  {
    icon: Boxes,
    label: 'Describe',
    detail: 'Apply a Topology, or author Node and Link resources directly.',
  },
  {
    icon: Cable,
    label: 'Reconcile',
    detail: 'Controllers validate the lab, plan each node, and allocate wires.',
  },
  {
    icon: Server,
    label: 'Run',
    detail: 'One device Pod per node, wired together across the cluster.',
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
        {/* Hero */}
        <section className="mx-auto grid w-full max-w-7xl items-center gap-10 px-6 py-16 lg:grid-cols-[1.15fr_0.85fr] lg:gap-14 lg:px-8 lg:py-20">
          <div>
            <div className="mb-6 inline-flex items-center gap-2 rounded-full border bg-fd-card py-1.5 pr-3.5 pl-2.5 text-xs font-semibold tracking-wide uppercase">
              <img
                alt=""
                aria-hidden="true"
                className="size-6 object-contain"
                src={c9sLogo}
              />
              containerlab, distributed
            </div>

            <h1 className="text-4xl leading-[1.08] font-bold tracking-[-0.035em] xl:text-5xl">
              <span className="block">Distributed containerlabs,</span>
              <span className={`block ${accent}`}>powered by Kubernetes.</span>
            </h1>

            <p className="mt-6 max-w-xl text-lg leading-8 text-fd-muted-foreground">
              Clabernetes, or c9s (
              <span className="italic">pronounced see-nines</span>), turns each
              network node and wire into its own Kubernetes resource: one device
              Pod per node, links wired across the cluster, and management
              reachable through Services.
            </p>

            <div className="mt-8 flex flex-wrap items-center gap-3">
              <Link
                className="group inline-flex items-center gap-2 rounded-xl bg-fd-primary px-5 py-3 font-semibold text-fd-primary-foreground shadow-lg shadow-fd-primary/20 transition hover:-translate-y-0.5 hover:shadow-xl focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-fd-primary"
                to="/docs/quickstart"
              >
                Launch your first lab
                <ArrowRight className="size-4 transition-transform group-hover:translate-x-0.5" />
              </Link>
              <Link
                className="inline-flex items-center rounded-xl border bg-fd-card px-5 py-3 font-semibold transition hover:-translate-y-0.5 hover:bg-fd-accent"
                to="/docs"
              >
                Explore the docs
              </Link>
              <a
                className="inline-flex items-center gap-2 px-3 py-3 text-sm font-medium text-fd-muted-foreground transition-colors hover:text-fd-foreground"
                href="https://github.com/clabernetes/clabernetes"
                rel="noreferrer"
                target="_blank"
              >
                <Code2 className="size-4" />
                GitHub
              </a>
            </div>

            <div className="mt-8 flex flex-wrap gap-x-7 gap-y-3 text-sm text-fd-muted-foreground">
              {[
                'Node and Link API',
                'Topology compiler',
                'No custom CNI required',
              ].map((item) => (
                <span className="flex items-center gap-2" key={item}>
                  <span className="size-1.5 rounded-full bg-cyan-500" />
                  {item}
                </span>
              ))}
            </div>
          </div>

          <ClabLogo3D className="mx-auto aspect-square w-full max-w-[24rem] lg:max-w-[28rem]" />
        </section>

        {/* What it is */}
        <section className="mx-auto w-full max-w-7xl px-6 py-16 lg:px-8">
          <div className="max-w-2xl">
            <p className="text-sm font-semibold tracking-wide text-fd-muted-foreground uppercase">
              Designed for real labs
            </p>
            <h2 className="mt-2 text-3xl font-bold tracking-tight sm:text-4xl">
              A Kubernetes API for network infrastructure.
            </h2>
            <p className="mt-3 text-lg leading-8 text-fd-muted-foreground">
              Keep containerlab vocabulary for nodes and links, and let
              Kubernetes handle scheduling, storage, access, and lifecycle.
            </p>
          </div>

          <div className="mt-8 grid gap-5 md:grid-cols-3">
            {highlights.map(({ icon: Icon, eyebrow, title, description, href }) => (
              <Link
                className="group flex flex-col rounded-2xl border bg-fd-card p-6 shadow-sm transition duration-300 hover:-translate-y-1 hover:shadow-lg"
                key={title}
                to={href}
              >
                <div className="mb-5 flex size-10 items-center justify-center rounded-xl border bg-fd-background">
                  <Icon className={`size-5 ${accent}`} />
                </div>
                <p className="text-xs font-semibold tracking-wide text-fd-muted-foreground uppercase">
                  {eyebrow}
                </p>
                <h3 className="mt-1.5 text-lg font-semibold">{title}</h3>
                <p className="mt-2 text-sm leading-6 text-fd-muted-foreground">
                  {description}
                </p>
                <span className="mt-5 inline-flex items-center gap-1.5 text-sm font-semibold">
                  Learn more
                  <ArrowRight className="size-4 transition-transform group-hover:translate-x-1" />
                </span>
              </Link>
            ))}
          </div>
        </section>

        {/* How it works, paired with the fabric it produces */}
        <section className="mx-auto w-full max-w-7xl px-6 py-16 lg:px-8">
          <div className="grid gap-12 lg:grid-cols-[1fr_1fr]">
            <div>
              <p className="text-sm font-semibold tracking-wide text-fd-muted-foreground uppercase">
                How it works
              </p>
              <h2 className="mt-2 text-3xl font-bold tracking-tight sm:text-4xl">
                From definition to running lab.
              </h2>
              <p className="mt-3 text-lg leading-8 text-fd-muted-foreground">
                Author direct resources or bring an existing containerlab
                topology. The controllers take it from there.
              </p>

              <ol className="mt-7 space-y-3">
                {steps.map(({ icon: Icon, label, detail }, index) => (
                  <li
                    className="flex items-start gap-4 rounded-xl border bg-fd-card p-4"
                    key={label}
                  >
                    <div className="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-fd-background">
                      <Icon className={`size-5 ${accent}`} />
                    </div>
                    <div className="min-w-0">
                      <p className="flex items-center gap-2 font-semibold">
                        {label}
                        <span className="font-mono text-xs font-normal text-fd-muted-foreground">
                          0{index + 1}
                        </span>
                      </p>
                      <p className="mt-0.5 text-sm leading-6 text-fd-muted-foreground">
                        {detail}
                      </p>
                    </div>
                  </li>
                ))}
              </ol>
            </div>

            <IntroFabricDiagram fill />
          </div>
        </section>

        {/* Get started */}
        <section className="mx-auto w-full max-w-3xl px-6 py-20 text-center lg:px-8">
          <p className="text-sm font-semibold tracking-wide text-fd-muted-foreground uppercase">
            Get started
          </p>
          <h2 className="mt-2 text-3xl font-bold tracking-tight text-balance sm:text-4xl">
            Your first distributed lab is one command away.
          </h2>
          <p className="mx-auto mt-3 max-w-xl text-lg text-fd-muted-foreground">
            A disposable KinD cluster, c9s, and a sample SR Linux lab, built
            locally end to end.
          </p>

          <div className="mx-auto mt-7 max-w-lg overflow-hidden rounded-xl border bg-fd-card text-left shadow-sm">
            <div
              aria-hidden="true"
              className="flex items-center gap-1.5 border-b px-4 py-2.5"
            >
              <span className="size-2.5 rounded-full bg-red-400/80" />
              <span className="size-2.5 rounded-full bg-yellow-400/80" />
              <span className="size-2.5 rounded-full bg-green-400/80" />
            </div>
            <div className="space-y-1 overflow-x-auto px-4 py-3.5 font-mono text-xs whitespace-nowrap sm:text-sm">
              <p>
                <span className="text-fd-muted-foreground select-none">$ </span>
                git clone https://github.com/clabernetes/clabernetes
              </p>
              <p>
                <span className="text-fd-muted-foreground select-none">$ </span>
                make try-c9s
              </p>
            </div>
          </div>

          <div className="mt-7">
            <Link
              className="group inline-flex items-center gap-2 rounded-xl bg-fd-primary px-6 py-3.5 font-semibold text-fd-primary-foreground shadow-lg shadow-fd-primary/20 transition hover:-translate-y-0.5"
              to="/docs/quickstart"
            >
              Open the quickstart
              <ArrowRight className="size-4 transition-transform group-hover:translate-x-0.5" />
            </Link>
          </div>
        </section>
      </div>
    </HomeLayout>
  );
}
