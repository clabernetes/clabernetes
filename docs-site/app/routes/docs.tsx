import { use, useMemo } from 'react';
import { useFumadocsLoader } from 'fumadocs-core/source/client';
import { DocsLayout } from 'fumadocs-ui/layouts/docs';
import {
  DocsBody,
  DocsDescription,
  DocsPage,
  DocsTitle,
} from 'fumadocs-ui/layouts/docs/page';
import { redirect } from 'react-router';
import { getMDXComponents } from '@/components/mdx';
import { getDocsTabs } from '@/lib/docs-tabs';
import { baseOptions } from '@/lib/layout.shared';
import { docs, source } from '@/lib/source';
import type { Route } from './+types/docs';

export async function loader({ params }: Route.LoaderArgs) {
  // params['*'] contains the full catch-all path, such as
  // "release-notes/0.7.0"; keep it whole for legacy redirects, then split it
  // by "/" into the slug segments expected by the Fumadocs source.
  const slug = params['*'] ?? '';

  // REDIRECTS SHOULD BE HANDLED HERE
  // Redirect legacy release notes path to the new one.
  if (slug === 'release-notes/0.7.0') {
    throw redirect('/docs/release-notes/0.7', { status: 308 });
  }
  // END REDIRECTS

  const slugs = slug.split('/').filter(Boolean);
  const page = source.getPage(slugs);

  if (!page) {
    throw new Response('Documentation page not found', { status: 404 });
  }

  await docs.getPage(page.path)?.preload();

  return {
    path: page.path,
    pageTree: await source.serializePageTree(source.getPageTree()),
  };
}

export async function clientLoader({
  serverLoader,
}: Route.ClientLoaderArgs) {
  const data = await serverLoader();
  await docs.getPage(data.path)?.preload();
  return data;
}

clientLoader.hydrate = true;

function Content({ path }: { path: string }) {
  const page = docs.getPage(path);

  if (!page) {
    throw new Error(`Unknown documentation page: ${path}`);
  }

  const { toc } = use(page.load());
  const MDX = page.body;

  return (
    <DocsPage toc={toc}>
      <title>{`${page.title} | c9s`}</title>
      {page.description ? (
        <meta name="description" content={page.description} />
      ) : null}
      <DocsTitle>{page.title}</DocsTitle>
      <DocsDescription>{page.description}</DocsDescription>
      <DocsBody>
        <MDX components={getMDXComponents()} />
      </DocsBody>
    </DocsPage>
  );
}

export default function DocumentationPage({
  loaderData,
}: Route.ComponentProps) {
  const { path, pageTree } = useFumadocsLoader(loaderData);
  const tabs = useMemo(() => getDocsTabs(pageTree), [pageTree]);

  return (
    <DocsLayout
      {...baseOptions()}
      tree={pageTree}
      tabs={tabs}
    >
      <Content path={path} />
    </DocsLayout>
  );
}
