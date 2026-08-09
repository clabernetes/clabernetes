import { loader } from 'fumadocs-core/source';
import { defineDocs } from 'fumadocs-mdx/macro';
import { lucideIcon } from '@/lib/icons';

export const docs = defineDocs({
  dir: '../docs',
  docs: {
    async: true,
  },
});

export const source = loader({
  baseUrl: '/docs',
  source: docs.toFumadocsSource(),
  icon(icon) {
    return lucideIcon(icon);
  },
});
