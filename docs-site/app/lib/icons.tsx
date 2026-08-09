import { icons } from 'lucide-react';
import { createElement, type ReactNode } from 'react';

/** Resolve a lucide-react icon name to a React element (OpenSpec / Fumadocs pattern). */
export function lucideIcon(name: string | undefined): ReactNode {
  if (!name || !(name in icons)) return undefined;
  return createElement(icons[name as keyof typeof icons]);
}
