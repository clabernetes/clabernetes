import defaultMdxComponents from 'fumadocs-ui/mdx';
import { Home, Rocket, Download } from 'lucide-react';
import { CrdViewer } from '@/components/crd-viewer';
import {
  IntroFabricDiagram,
  TryC9sDiagram,
} from '@/components/fabric-diagram';
import type { MDXComponents } from 'mdx/types';
import { Accordion, Accordions } from 'fumadocs-ui/components/accordion';
import * as TabsComponents from 'fumadocs-ui/components/tabs';

export function getMDXComponents(components?: MDXComponents) {
  return {
    ...defaultMdxComponents,
    CrdViewer,
    Home,
    Rocket,
    Download,
    IntroFabricDiagram: IntroFabricDiagram,
    TryC9sDiagram: TryC9sDiagram,
    Accordion: Accordion,
    Accordions: Accordions,
    ...TabsComponents,
    ...components,
  } satisfies MDXComponents;
}

export const useMDXComponents = getMDXComponents;

declare global {
  type MDXProvidedComponents = ReturnType<typeof getMDXComponents>;
}
