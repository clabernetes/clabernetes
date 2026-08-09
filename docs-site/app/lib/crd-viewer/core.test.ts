import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import {
  CrdRenderError,
  loadCrdView,
  renderCrdViewer,
} from './core';

function dedent(content: string): string {
  const lines = content.replace(/^\n/, '').split('\n');
  const indent = lines
    .filter((line) => line.trim().length > 0)
    .reduce((min, line) => {
      const match = line.match(/^ */);
      const size = match ? match[0].length : 0;
      return Math.min(min, size);
    }, Number.POSITIVE_INFINITY);

  return lines
    .map((line) => line.slice(indent))
    .join('\n')
    .trim();
}

function writeFile(filePath: string, content: string): void {
  fs.mkdirSync(path.dirname(filePath), { recursive: true });
  fs.writeFileSync(filePath, dedent(content) + '\n', 'utf-8');
}

const tmpDirs: string[] = [];

afterEach(() => {
  for (const dir of tmpDirs) {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  tmpDirs.length = 0;
});

function makeTmpDir(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'crd-viewer-'));
  tmpDirs.push(dir);
  return dir;
}

describe('loadCrdView', () => {
  it('selects storage version', () => {
    const tmp = makeTmpDir();
    const source = path.join(tmp, 'example.yaml');
    writeFile(
      source,
      `
      apiVersion: apiextensions.k8s.io/v1
      kind: CustomResourceDefinition
      metadata:
        name: widgets.example.com
      spec:
        group: example.com
        names:
          kind: Widget
          plural: widgets
        scope: Namespaced
        versions:
          - name: v1alpha1
            served: true
            storage: false
            schema:
              openAPIV3Schema:
                type: object
                properties:
                  spec:
                    type: object
                    properties:
                      field:
                        type: string
          - name: v1
            served: true
            storage: true
            schema:
              openAPIV3Schema:
                type: object
                properties:
                  spec:
                    type: object
                    properties:
                      field:
                        type: integer
      `,
    );

    const view = loadCrdView(source);
    expect(view.version).toBe('v1');
    expect(view.sections[0].children[0].field_type).toBe('integer');
  });

  it('rejects multiple CRDs in one file', () => {
    const tmp = makeTmpDir();
    const source = path.join(tmp, 'example.yaml');
    writeFile(
      source,
      `
      apiVersion: apiextensions.k8s.io/v1
      kind: CustomResourceDefinition
      metadata:
        name: widgets.example.com
      spec:
        group: example.com
        names:
          kind: Widget
          plural: widgets
        versions:
          - name: v1
            served: true
            storage: true
            schema:
              openAPIV3Schema:
                type: object
                properties:
                  spec:
                    type: object
      ---
      apiVersion: apiextensions.k8s.io/v1
      kind: CustomResourceDefinition
      metadata:
        name: gadgets.example.com
      spec:
        group: example.com
        names:
          kind: Gadget
          plural: gadgets
        versions:
          - name: v1
            served: true
            storage: true
            schema:
              openAPIV3Schema:
                type: object
                properties:
                  spec:
                    type: object
      `,
    );

    expect(() => loadCrdView(source)).toThrow(CrdRenderError);
    expect(() => loadCrdView(source)).toThrow(
      'Multiple CustomResourceDefinition documents found',
    );
  });
});

describe('renderCrdViewer', () => {
  it('outputs spec, status, and metadata facts', () => {
    const tmp = makeTmpDir();
    const source = path.join(tmp, 'example.yaml');
    writeFile(
      source,
      `
      apiVersion: apiextensions.k8s.io/v1
      kind: CustomResourceDefinition
      metadata:
        name: widgets.example.com
      spec:
        group: example.com
        names:
          kind: Widget
          plural: widgets
        versions:
          - name: v1
            served: true
            storage: true
            schema:
              openAPIV3Schema:
                type: object
                properties:
                  spec:
                    type: object
                    description: WidgetSpec defines the desired state.
                    required:
                      - size
                    properties:
                      size:
                        type: string
                        enum: [small, medium]
                        description: Selected size.
                      labels:
                        type: object
                        additionalProperties:
                          type: string
                  status:
                    type: object
                    description: WidgetStatus defines the observed state.
                    properties:
                      phase:
                        type: string
      `,
    );

    const html = renderCrdViewer(tmp, 'example.yaml', { collapsed: true });

    expect(html).toContain('SPEC');
    expect(html).toContain('STATUS');
    expect(html).toContain('Widget');
    expect(html).toContain('Selected size.');
    expect(html).toContain('enum');
    expect(html).toContain('&lt;key&gt;');
  });

  it('renders scalar arrays inline', () => {
    const tmp = makeTmpDir();
    const source = path.join(tmp, 'example.yaml');
    writeFile(
      source,
      `
      apiVersion: apiextensions.k8s.io/v1
      kind: CustomResourceDefinition
      metadata:
        name: fabrics.example.com
      spec:
        group: example.com
        names:
          kind: Fabric
          plural: fabrics
        versions:
          - name: v1alpha1
            served: true
            storage: true
            schema:
              openAPIV3Schema:
                type: object
                properties:
                  spec:
                    type: object
                    properties:
                      leafNodeSelector:
                        type: array
                        items:
                          type: string
      `,
    );

    const html = renderCrdViewer(tmp, 'example.yaml');
    expect(html).toContain('array[string]');
    expect(html).not.toContain('>items<');
  });

  it('renders leaf entries as collapsible nodes', () => {
    const tmp = makeTmpDir();
    const source = path.join(tmp, 'example.yaml');
    writeFile(
      source,
      `
      apiVersion: apiextensions.k8s.io/v1
      kind: CustomResourceDefinition
      metadata:
        name: fabrics.example.com
      spec:
        group: example.com
        names:
          kind: Fabric
          plural: fabrics
        versions:
          - name: v1alpha1
            served: true
            storage: true
            schema:
              openAPIV3Schema:
                type: object
                properties:
                  spec:
                    type: object
                    properties:
                      fabricSelector:
                        description: Selects Fabric resources.
                        type: array
                        items:
                          type: string
      `,
    );

    const html = renderCrdViewer(tmp, 'example.yaml');
    expect(html).toContain('data-crd-toggle-node');
    expect(html).toContain('fabricSelector');
    expect(html).toContain('crd-viewer__content');
  });
});
