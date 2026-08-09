import { HomeLayout } from 'fumadocs-ui/layouts/home';
import { ArrowRight, Boxes, Network, Rocket } from 'lucide-react';
import { Link } from 'react-router';
import { baseOptions } from '@/lib/layout.shared';

const highlights = [
  {
    icon: Network,
    title: 'Node and Link first',
    description:
      'Model each network node and wire as an independently reconciled Kubernetes resource.',
  },
  {
    icon: Boxes,
    title: 'Built for Kubernetes',
    description:
      'Use launcher profiles, scheduling, persistence, services, and familiar cluster workflows.',
  },
  {
    icon: Rocket,
    title: 'Containerlab compatible',
    description:
      'Run existing topologies through the supported Topology compiler or emit primitive resources.',
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

      <section className="mx-auto flex w-full max-w-6xl flex-col px-6 py-20 md:py-32">
        <p className="mb-4 text-sm font-semibold tracking-wide text-fd-primary uppercase">
          containerlab + Kubernetes
        </p>
        <h1 className="max-w-4xl text-5xl leading-tight font-bold tracking-tight md:text-7xl">
          Distributed network labs, reconciled by Kubernetes.
        </h1>
        <p className="mt-6 max-w-2xl text-lg leading-8 text-fd-muted-foreground md:text-xl">
          Clabernetes, or c9s, turns network nodes and links into Kubernetes
          resources and runs containerlab workloads across your cluster.
        </p>
        <div className="mt-10 flex flex-wrap gap-3">
          <Link
            className="inline-flex items-center gap-2 rounded-lg bg-fd-primary px-5 py-3 font-medium text-fd-primary-foreground"
            to="/docs/quickstart"
          >
            Try c9s
            <ArrowRight className="size-4" />
          </Link>
          <Link
            className="inline-flex items-center rounded-lg border bg-fd-card px-5 py-3 font-medium"
            to="/docs"
          >
            Read the docs
          </Link>
        </div>
      </section>

      <section className="mx-auto grid w-full max-w-6xl gap-4 px-6 pb-24 md:grid-cols-3">
        {highlights.map(({ icon: Icon, title, description }) => (
          <article
            className="rounded-xl border bg-fd-card/80 p-6 shadow-sm"
            key={title}
          >
            <Icon className="mb-4 size-6 text-fd-primary" />
            <h2 className="font-semibold">{title}</h2>
            <p className="mt-2 text-sm leading-6 text-fd-muted-foreground">
              {description}
            </p>
          </article>
        ))}
      </section>
    </HomeLayout>
  );
}
