import { icons } from 'lucide-react';
import { createElement, type ReactNode } from 'react';

const TAB_ICON_WRAPPER =
  'size-full [&_svg]:size-full max-md:p-1.5 max-md:rounded-md max-md:border max-md:bg-fd-secondary';

/** Resolve a lucide-react icon name to a React element (OpenSpec / Fumadocs pattern). */
export function lucideIcon(name: string | undefined): ReactNode {
  if (!name || !(name in icons)) return undefined;
  return createElement(icons[name as keyof typeof icons]);
}

/** Icon for DocsLayout category tabs — matches Fumadocs `defaultTransform` sizing. */
export function layoutTabIcon(name: string): ReactNode {
  const icon = lucideIcon(name);
  if (!icon) return undefined;
  return createElement('div', { className: TAB_ICON_WRAPPER }, icon);
}
