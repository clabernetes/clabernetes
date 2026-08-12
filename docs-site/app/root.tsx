import { RootProvider } from 'fumadocs-ui/provider/react-router';
import {
  isRouteErrorResponse,
  Links,
  Meta,
  Outlet,
  redirect,
  Scripts,
  ScrollRestoration,
} from 'react-router';
import { docsRedirect } from '@/lib/docs-redirects';
import c9sLogo from '@/assets/c9s-logo-clean.png';
import StaticSearchDialog from '@/components/static-search';
import { UnderDevelopmentBanner } from '@/components/under-development-banner';
import type { Route } from './+types/root';
import './app.css';

export const links: Route.LinksFunction = () => [
  { rel: 'icon', href: c9sLogo, type: 'image/png' },
  { rel: 'apple-touch-icon', href: c9sLogo, type: 'image/png' },
];

// Cloudflare SPA fallback serves home index.html for unknown paths, so the docs
// route loader never runs. Redirect legacy URLs from the browser path on hydrate.
export async function clientLoader() {
  const target = docsRedirect(window.location.pathname);
  if (target) {
    throw redirect(target, { status: 308 });
  }
  return null;
}

clientLoader.hydrate = true;

export function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        <meta charSet="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <Meta />
        <Links />
      </head>
      <body className="flex min-h-screen flex-col">
        <RootProvider search={{ SearchDialog: StaticSearchDialog }}>
          <UnderDevelopmentBanner />
          {children}
        </RootProvider>
        <ScrollRestoration />
        <Scripts />
      </body>
    </html>
  );
}

export default function App() {
  return <Outlet />;
}

export function ErrorBoundary({ error }: Route.ErrorBoundaryProps) {
  const message = isRouteErrorResponse(error)
    ? `${error.status} ${error.statusText}`
    : 'Something went wrong';

  return (
    <main className="mx-auto flex min-h-screen max-w-3xl flex-col justify-center px-6">
      <p className="text-sm font-medium text-fd-muted-foreground">c9s docs</p>
      <h1 className="mt-2 text-3xl font-semibold">{message}</h1>
      <a className="mt-6 text-fd-primary underline" href="/">
        Return home
      </a>
    </main>
  );
}
