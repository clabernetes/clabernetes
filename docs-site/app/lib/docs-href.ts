const markdownFileExtension = /\.(?:mdx|md)$/i;

function isExternalHref(href: string): boolean {
  return /^(?:[a-z][a-z\d+.-]*:|\/\/)/i.test(href);
}

/**
 * Rewrite authored markdown/MDX file hrefs to Fumadocs routes.
 *
 * IDEs complete links as content-file paths (`../installation.mdx`,
 * `/docs/installation.mdx#upgrade`). The site serves those pages without the
 * extension (`/docs/installation#upgrade`).
 */
export function rewriteMarkdownFileHref(href: string): string {
  if (!href || href.startsWith('#') || isExternalHref(href)) {
    return href;
  }

  const suffixIndex = href.search(/[?#]/);
  const path = suffixIndex === -1 ? href : href.slice(0, suffixIndex);
  const suffix = suffixIndex === -1 ? '' : href.slice(suffixIndex);

  if (!markdownFileExtension.test(path)) {
    return href;
  }

  let next = path.replace(markdownFileExtension, '');
  if (next.endsWith('/index')) {
    next = next.slice(0, -'/index'.length);
  }

  if (next === '') {
    next = '/';
  }

  return `${next}${suffix}`;
}
