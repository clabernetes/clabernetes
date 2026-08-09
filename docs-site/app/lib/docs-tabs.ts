import type { Root } from 'fumadocs-core/page-tree';
import type { LayoutTab } from 'fumadocs-ui/layouts/shared';
import { getLayoutTabs } from 'fumadocs-ui/layouts/shared';
import { layoutTabIcon } from '@/lib/icons';

const DOCUMENTATION_TAB: LayoutTab = {
  title: 'Documentation',
  description: 'Install, concepts, and operations',
  url: '/docs',
  icon: layoutTabIcon('BookOpen'),
};

const CRD_REFERENCE_TAB: LayoutTab = {
  title: 'CRD Reference',
  description: 'Custom resource definitions reference',
  url: '/docs/crd',
  icon: layoutTabIcon('Braces'),
};

/** Guide + CRD Reference tabs with icons from meta.json and lucide names. */
export function getDocsTabs(tree: Root): LayoutTab[] {
  const fromTree = getLayoutTabs(tree);
  const crdTab = fromTree.find((tab) => tab.url.startsWith('/docs/crd'));

  // Roots from tree.fallback are marked unlisted and hidden in the dropdown
  // unless active — always show both categories.
  const crd: LayoutTab = crdTab
    ? {
      ...crdTab,
      unlisted: false,
      // Tree icons are serialized HTML and ignore tab container sizing.
      icon: layoutTabIcon('Braces'),
    }
    : CRD_REFERENCE_TAB;

  return [DOCUMENTATION_TAB, crd];
}
