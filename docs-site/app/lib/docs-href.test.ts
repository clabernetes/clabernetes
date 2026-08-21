import { describe, expect, it } from 'vitest';
import { rewriteMarkdownFileHref } from './docs-href';

describe('rewriteMarkdownFileHref', () => {
  it('strips .mdx from site paths and keeps the hash', () => {
    expect(rewriteMarkdownFileHref('/docs/installation.mdx#upgrade')).toBe(
      '/docs/installation#upgrade',
    );
  });

  it('strips .md from relative file completions', () => {
    expect(rewriteMarkdownFileHref('../installation.md')).toBe(
      '../installation',
    );
  });

  it('collapses index files to the folder route', () => {
    expect(rewriteMarkdownFileHref('/docs/crd/index.md')).toBe('/docs/crd');
  });

  it('leaves already-canonical docs routes unchanged', () => {
    expect(rewriteMarkdownFileHref('/docs/installation#upgrade')).toBe(
      '/docs/installation#upgrade',
    );
  });

  it('leaves external URLs unchanged even when they end in .md', () => {
    expect(
      rewriteMarkdownFileHref('https://example.com/readme.md'),
    ).toBe('https://example.com/readme.md');
  });
});
