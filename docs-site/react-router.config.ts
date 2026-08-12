import type { Config } from '@react-router/dev/config';
import { createGetUrl, getSlugs } from 'fumadocs-core/source';
import { glob } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { docsPathRedirects } from './app/lib/docs-redirects';

const docsDirectory = fileURLToPath(new URL('../docs', import.meta.url));
const getDocsUrl = createGetUrl('/docs');

export default {
  ssr: false,
  future: {
    v8_middleware: true,
    v8_passThroughRequests: true,
    v8_splitRouteModules: true,
    v8_viteEnvironmentApi: true,
  },
  routeDiscovery: {
    mode: 'initial',
  },
  async prerender({ getStaticPaths }) {
    const paths = new Set(getStaticPaths());

    paths.add('/');
    paths.add('/search-index.json');

    for (const pattern of ['**/*.md', '**/*.mdx']) {
      for await (const entry of glob(pattern, { cwd: docsDirectory })) {
        paths.add(getDocsUrl(getSlugs(entry)));
      }
    }

    for (const legacyPath of Object.keys(docsPathRedirects)) {
      paths.add(legacyPath);
    }

    return [...paths];
  },
} satisfies Config;
