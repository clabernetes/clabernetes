import defaultMdxComponents from 'fumadocs-ui/mdx';
import { CrdViewer } from '@/components/crd-viewer';
import { IntroFabricDiagram } from '@/components/fabric-diagram';
import type { MDXComponents } from 'mdx/types';

export function getMDXComponents(components?: MDXComponents) {
  return {
    ...defaultMdxComponents,
    CrdViewer,
    IntroFabricDiagram: IntroFabricDiagram,
    ...components,
  } satisfies MDXComponents;
}

export const useMDXComponents = getMDXComponents;

declare global {
  type MDXProvidedComponents = ReturnType<typeof getMDXComponents>;
}
