/**
 * Load crd-viewer.js and initialize viewers under root.
 * Ported from eda-labs/mkdocs-crd-viewer assets.
 */

declare global {
  interface Window {
    __crdViewerInit?: (root: ParentNode) => void;
  }
}

let scriptPromise: Promise<void> | null = null;

function loadCrdViewerScript(): Promise<void> {
  if (window.__crdViewerInit) return Promise.resolve();
  if (scriptPromise) return scriptPromise;

  scriptPromise = import('./crd-viewer.js?url').then((mod) => {
    const src = mod.default as string;
    const existing = document.querySelector(`script[data-crd-viewer-script]`);
    if (existing) return;

    return new Promise<void>((resolve, reject) => {
      const script = document.createElement('script');
      script.dataset.crdViewerScript = 'true';
      script.src = src;
      script.onload = () => resolve();
      script.onerror = () => reject(new Error('Failed to load crd-viewer.js'));
      document.head.appendChild(script);
    });
  });

  return scriptPromise;
}

export async function initCrdViewers(root: ParentNode = document): Promise<void> {
  await loadCrdViewerScript();
  window.__crdViewerInit?.(root);
}
