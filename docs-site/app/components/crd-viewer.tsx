'use client';

import { useEffect, useRef } from 'react';
import { crdViews } from '@/generated/crd-views';
import { initCrdViewers } from '@/lib/crd-viewer/runtime';

export interface CrdViewerProps {
  /** Key matching an entry in generated/crd-views.ts (e.g. `node`, `topology`). */
  name: string;
}

export function CrdViewer({ name }: CrdViewerProps) {
  const html = crdViews[name];
  const rootRef = useRef<HTMLDivElement>(null);

  if (!html) {
    throw new Error(`Unknown CRD view "${name}". Run scripts/generate-crd-views.ts.`);
  }

  useEffect(() => {
    const root = rootRef.current ?? document;
    void initCrdViewers(root);
  }, [html]);

  return <div ref={rootRef} dangerouslySetInnerHTML={{ __html: html }} />;
}
