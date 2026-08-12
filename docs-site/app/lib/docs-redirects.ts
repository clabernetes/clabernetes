/** Legacy docs URLs that should redirect to their replacement. */
export const docsPathRedirects: Record<string, string> = {
  '/docs/release-notes/0.7.0': '/docs/release-notes/0.7',
};

export function normalizePathname(pathname: string): string {
  return pathname.endsWith('/') && pathname !== '/'
    ? pathname.slice(0, -1)
    : pathname;
}

export function docsRedirect(pathname: string): string | undefined {
  return docsPathRedirects[normalizePathname(pathname)];
}
