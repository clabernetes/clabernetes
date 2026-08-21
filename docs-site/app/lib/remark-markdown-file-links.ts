import type { Root } from 'mdast';
import { visit } from 'unist-util-visit';
import { rewriteMarkdownFileHref } from './docs-href';

/**
 * Rewrite markdown/MDX file hrefs while Fumadocs compiles MDX, so authored
 * IDE completions become site routes in the generated output.
 */
export function remarkMarkdownFileLinks() {
  return (tree: Root) => {
    visit(tree, ['link', 'definition'], (node) => {
      if (node.type !== 'link' && node.type !== 'definition') {
        return;
      }

      node.url = rewriteMarkdownFileHref(node.url);
    });
  };
}
