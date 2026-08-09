import { use } from 'react';
import { useFumadocsLoader } from 'fumadocs-core/source/client';
import { DocsLayout } from 'fumadocs-ui/layouts/docs';
import {
  DocsBody,
  DocsDescription,
  DocsPage,
  DocsTitle,
} from 'fumadocs-ui/layouts/docs/page';
import { getMDXComponents } from '@/components/mdx';
import { baseOptions } from '@/lib/layout.shared';
import { docs, source } from '@/lib/source';
import type { Route } from './+types/docs';

export async function loader({ params }: Route.LoaderArgs) {
  const slugs = params['*']?.split('/').filter(Boolean) ?? [];
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

  return (
    <DocsLayout {...baseOptions()} tree={pageTree}>
      <Content path={path} />
    </DocsLayout>
  );
}
