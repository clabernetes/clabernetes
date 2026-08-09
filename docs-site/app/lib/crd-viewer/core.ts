/**
 * CRD parsing and HTML rendering — TypeScript port of mkdocs-crd-viewer core.py.
 * @see https://github.com/eda-labs/mkdocs-crd-viewer
 */

import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { parseAllDocuments } from 'yaml';
import { renderDescriptionMarkdown } from './markdown';

export class CrdRenderError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CrdRenderError';
  }
}

export interface FieldNode {
  label: string;
  path: string;
  field_type: string;
  description: string;
  required: boolean;
  children: FieldNode[];
  default: unknown;
  enum: unknown[];
  field_format: string | null;
  minimum: unknown;
  maximum: unknown;
}

export interface Section {
  key: string;
  title: string;
  description: string;
  children: FieldNode[];
}

export interface CrdView {
  source_path: string;
  kind: string;
  group: string;
  version: string;
  sections: Section[];
}

type SchemaDict = Record<string, unknown>;

let renderCounter = 0;

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function renderDescriptionHtml(text: string, className: string): string {
  return `<div class="${className}">${renderDescriptionMarkdown(text)}</div>`;
}

function parseCrdDocuments(content: string): SchemaDict[] {
  const documents = parseAllDocuments(content, {
    customTags: [
      {
        tag: 'tag:yaml.org,2002:value',
        resolve: (value: string) => value,
      },
    ],
  });

  return documents
    .map((doc) => doc.toJSON() as SchemaDict | null)
    .filter((doc): doc is SchemaDict => doc !== null && typeof doc === 'object');
}

export function renderCrdViewer(
  projectRoot: string,
  source: string,
  options: {
    version?: string | null;
    title?: string | null;
    collapsed?: boolean;
    showStatus?: boolean;
  } = {},
): string {
  const {
    version = null,
    title = null,
    collapsed = false,
    showStatus = true,
  } = options;

  const sourcePath = resolveSourcePath(projectRoot, source);
  const view = loadCrdView(sourcePath, { version, showStatus });
  return renderView(view, { title, collapsed });
}

export function loadCrdView(
  sourcePath: string,
  options: { version?: string | null; showStatus?: boolean } = {},
): CrdView {
  const { version = null, showStatus = true } = options;

  if (!fs.existsSync(sourcePath)) {
    throw new CrdRenderError(`CRD file not found: ${sourcePath}`);
  }

  const content = fs.readFileSync(sourcePath, 'utf-8');
  const documents = parseCrdDocuments(content);
  const crds = documents.filter((doc) => doc.kind === 'CustomResourceDefinition');

  if (crds.length === 0) {
    throw new CrdRenderError(
      `No CustomResourceDefinition documents found in ${sourcePath}`,
    );
  }

  if (crds.length > 1) {
    const available = crds
      .map(
        (doc) =>
          `${(doc.spec as SchemaDict)?.group}/${((doc.spec as SchemaDict)?.names as SchemaDict)?.kind}`,
      )
      .join(', ');
    throw new CrdRenderError(
      `Multiple CustomResourceDefinition documents found in ${sourcePath}. Keep one CRD per file. Found: ${available}`,
    );
  }

  const crd = crds[0];
  const spec = (crd.spec as SchemaDict) || {};
  const names = (spec.names as SchemaDict) || {};
  const selectedKind = names.kind as string | undefined;
  const selectedGroup = spec.group as string | undefined;

  const versions = spec.versions;
  if (!Array.isArray(versions) || versions.length === 0) {
    throw new CrdRenderError(
      `CRD ${selectedKind} in ${sourcePath} does not define spec.versions`,
    );
  }

  const versionEntry = selectVersion(versions as SchemaDict[], version);
  const selectedVersion = versionEntry.name as string;
  const schema =
    ((versionEntry.schema as SchemaDict)?.openAPIV3Schema as SchemaDict) || {};

  if (!schema || typeof schema !== 'object') {
    throw new CrdRenderError(
      `CRD ${selectedKind} ${selectedVersion} in ${sourcePath} is missing schema.openAPIV3Schema`,
    );
  }

  const sections = buildSections(schema, showStatus);
  if (sections.length === 0) {
    throw new CrdRenderError(
      `CRD ${selectedKind} ${selectedVersion} in ${sourcePath} has no renderable spec/status schema`,
    );
  }

  return {
    source_path: sourcePath,
    kind: selectedKind || 'Unknown',
    group: selectedGroup || 'unknown.group',
    version: selectedVersion || 'unknown',
    sections,
  };
}

function resolveSourcePath(projectRoot: string, source: string): string {
  const candidate = path.resolve(source);
  if (path.isAbsolute(source)) return candidate;
  return path.resolve(projectRoot, source);
}

function selectVersion(
  versions: SchemaDict[],
  requested: string | null,
): SchemaDict {
  if (requested) {
    const match = versions.find((v) => v.name === requested);
    if (match) return match;
    const available = versions.map((v) => String(v.name)).join(', ');
    throw new CrdRenderError(
      `Requested CRD version ${JSON.stringify(requested)} not found. Available versions: ${available}`,
    );
  }

  const storage = versions.find((v) => v.storage === true);
  if (storage) return storage;

  const served = versions.find((v) => v.served === true);
  if (served) return served;

  return versions[0];
}

function buildSections(schema: SchemaDict, showStatus: boolean): Section[] {
  const rootProperties = (schema.properties as SchemaDict) || {};
  if (typeof rootProperties !== 'object') return [];

  const sections: Section[] = [];
  for (const [key, title] of [
    ['spec', 'SPEC'],
    ['status', 'STATUS'],
  ] as const) {
    if (key === 'status' && !showStatus) continue;
    const sectionSchema = rootProperties[key];
    if (!sectionSchema || typeof sectionSchema !== 'object') continue;
    sections.push(
      buildSection(key, title, sectionSchema as SchemaDict),
    );
  }

  if (sections.length > 0) return sections;

  const rootCandidates: SchemaDict = {};
  for (const [key, value] of Object.entries(rootProperties)) {
    if (
      key !== 'apiVersion' &&
      key !== 'kind' &&
      key !== 'metadata' &&
      typeof value === 'object' &&
      value !== null
    ) {
      rootCandidates[key] = value;
    }
  }

  if (Object.keys(rootCandidates).length === 0) return [];

  const required = Array.isArray(schema.required) ? schema.required : [];
  const syntheticSchema: SchemaDict = {
    description: schema.description ?? '',
    properties: rootCandidates,
    required: required.filter((name) => name in rootCandidates),
  };

  return [buildSection('root', 'ROOT', syntheticSchema)];
}

function buildSection(
  key: string,
  title: string,
  schema: SchemaDict,
): Section {
  const properties = (schema.properties as SchemaDict) || {};
  const required = new Set(
    Array.isArray(schema.required) ? schema.required : [],
  );

  const children: FieldNode[] = [];
  for (const [name, propertySchema] of Object.entries(properties)) {
    if (typeof propertySchema === 'object' && propertySchema !== null) {
      children.push(
        buildNode(name, propertySchema as SchemaDict, key, required.has(name)),
      );
    }
  }

  return {
    key,
    title,
    description: String(schema.description ?? '').trim(),
    children,
  };
}

function buildNode(
  name: string,
  schema: SchemaDict,
  pathPrefix: string,
  required: boolean,
): FieldNode {
  const path = pathPrefix ? `${pathPrefix}.${name}` : name;
  const children: FieldNode[] = [];

  const properties = schema.properties;
  if (properties && typeof properties === 'object') {
    const childRequired = new Set(
      Array.isArray(schema.required) ? schema.required : [],
    );
    for (const [childName, childSchema] of Object.entries(properties)) {
      if (typeof childSchema === 'object' && childSchema !== null) {
        children.push(
          buildNode(
            childName,
            childSchema as SchemaDict,
            path,
            childRequired.has(childName),
          ),
        );
      }
    }
  }

  const itemSchema = schema.items;
  if (
    typeof itemSchema === 'object' &&
    itemSchema !== null &&
    schemaHasNestedChildren(itemSchema as SchemaDict)
  ) {
    children.push(
      buildVirtualNode('[]', itemSchema as SchemaDict, `${path}[]`),
    );
  }

  const additional = schema.additionalProperties;
  if (additional !== undefined) {
    children.push(buildMapNode(additional, path));
  }

  return {
    label: name,
    path,
    field_type: schemaType(schema),
    description: String(schema.description ?? '').trim(),
    required,
    children,
    default: schema.default,
    enum: Array.isArray(schema.enum) ? [...schema.enum] : [],
    field_format: schema.format != null ? String(schema.format) : null,
    minimum: schema.minimum,
    maximum: schema.maximum,
  };
}

function buildVirtualNode(
  label: string,
  schema: SchemaDict,
  path: string,
): FieldNode {
  const properties = (schema.properties as SchemaDict) || {};
  const required = new Set(
    Array.isArray(schema.required) ? schema.required : [],
  );
  const children: FieldNode[] = [];

  for (const [childName, childSchema] of Object.entries(properties)) {
    if (typeof childSchema === 'object' && childSchema !== null) {
      children.push(
        buildNode(
          childName,
          childSchema as SchemaDict,
          path,
          required.has(childName),
        ),
      );
    }
  }

  const additional = schema.additionalProperties;
  if (additional !== undefined) {
    children.push(buildMapNode(additional, path));
  }

  const itemSchema = schema.items;
  if (
    typeof itemSchema === 'object' &&
    itemSchema !== null &&
    schemaHasNestedChildren(itemSchema as SchemaDict)
  ) {
    children.push(
      buildVirtualNode('[]', itemSchema as SchemaDict, `${path}[]`),
    );
  }

  return {
    label,
    path,
    field_type: schemaType(schema),
    description: String(schema.description ?? '').trim(),
    children,
    default: schema.default,
    enum: Array.isArray(schema.enum) ? [...schema.enum] : [],
    field_format: schema.format != null ? String(schema.format) : null,
    minimum: schema.minimum,
    maximum: schema.maximum,
    required: false,
  };
}

function buildMapNode(additional: unknown, path: string): FieldNode {
  if (additional === true) {
    return {
      label: '<key>',
      path: `${path}.*`,
      field_type: 'any',
      description: 'Additional map entries are allowed.',
      required: false,
      children: [],
      default: null,
      enum: [],
      field_format: null,
      minimum: undefined,
      maximum: undefined,
    };
  }

  if (!additional || typeof additional !== 'object') {
    return {
      label: '<key>',
      path: `${path}.*`,
      field_type: 'unknown',
      description: 'Additional map entries are allowed.',
      required: false,
      children: [],
      default: null,
      enum: [],
      field_format: null,
      minimum: undefined,
      maximum: undefined,
    };
  }

  const node = buildVirtualNode('<key>', additional as SchemaDict, `${path}.*`);
  if (!node.description) {
    node.description = 'Schema for additional map entries.';
  }
  return node;
}

function schemaType(schema: SchemaDict): string {
  const rawType = schema.type;
  if (Array.isArray(rawType)) {
    return rawType.map(String).join(' | ');
  }
  if (typeof rawType === 'string') {
    if (rawType === 'array' && typeof schema.items === 'object' && schema.items) {
      return `array[${schemaType(schema.items as SchemaDict)}]`;
    }
    return rawType;
  }
  if (schema['x-kubernetes-int-or-string'] === true) {
    return 'integer | string';
  }

  const compositeTypes: string[] = [];
  for (const key of ['oneOf', 'anyOf'] as const) {
    const options = schema[key];
    if (Array.isArray(options)) {
      for (const option of options) {
        if (
          typeof option === 'object' &&
          option !== null &&
          typeof (option as SchemaDict).type === 'string'
        ) {
          compositeTypes.push((option as SchemaDict).type as string);
        }
      }
    }
  }
  if (compositeTypes.length > 0) {
    const unique: string[] = [];
    for (const item of compositeTypes) {
      if (!unique.includes(item)) unique.push(item);
    }
    return unique.join(' | ');
  }

  if (
    typeof schema.properties === 'object' ||
    schema.additionalProperties !== undefined
  ) {
    return 'object';
  }
  if (typeof schema.items === 'object' && schema.items) {
    return 'array';
  }
  if (schema.enum) {
    return 'enum';
  }
  return 'unknown';
}

function schemaHasNestedChildren(schema: SchemaDict): boolean {
  return Boolean(
    typeof schema.properties === 'object' ||
    schema.additionalProperties !== undefined ||
    typeof schema.items === 'object',
  );
}

function renderView(
  view: CrdView,
  options: { title: string | null; collapsed: boolean },
): string {
  const viewerId = viewerIdFor(view, renderCounter++);
  const displayTitle = options.title || view.kind;
  const meta = `${view.group} / ${view.version}`;
  const sectionsHtml = view.sections
    .map((section) => renderSection(viewerId, section))
    .join('\n');

  const headerHtml = `
  <div class="crd-viewer__header">
    <div>
      <p class="crd-viewer__title">${escapeHtml(displayTitle)}</p>
      <p class="crd-viewer__meta">${escapeHtml(meta)}</p>
    </div>
    <button type="button" class="crd-viewer__toggle" data-crd-toggle-all data-expanded="false">Expand All</button>
  </div>`;

  const collapsedAttr = options.collapsed
    ? ' data-crd-collapsible data-crd-collapsed="true"'
    : '';

  return `
<section class="crd-viewer" data-crd-viewer-root${collapsedAttr} id="${viewerId}">
  ${headerHtml}
  ${sectionsHtml}
</section>
`.trim();
}

function renderSection(viewerId: string, section: Section): string {
  const descriptionHtml = section.description
    ? renderDescriptionHtml(section.description, 'crd-viewer__section-description')
    : '';
  const childrenHtml = section.children
    .map((node) => renderNode(viewerId, node))
    .join('\n');
  const sectionClass = `crd-viewer__section crd-viewer__section--${section.key}`;

  return `
<section class="${sectionClass}">
  <p class="crd-viewer__section-title">${escapeHtml(section.title)}</p>
  ${descriptionHtml}
  <ul class="crd-viewer__tree">
    ${childrenHtml}
  </ul>
</section>
`.trim();
}

function renderNode(viewerId: string, node: FieldNode): string {
  const nodeId = nodeIdFor(node.path);
  const contentId = `${nodeId}-content`;
  const label = escapeHtml(node.label);
  const requiredHtml = node.required
    ? '<sup class="crd-viewer__required" title="Required">*</sup>'
    : '';
  const badgeHtml = `<span class="crd-viewer__badge">${escapeHtml(node.field_type)}</span>`;
  const anchorHtml = `<a class="crd-viewer__anchor" href="#${nodeId}" aria-label="Link to ${label}">#</a>`;
  const factsHtml = renderFacts(node);
  const descriptionHtml = node.description
    ? renderDescriptionHtml(node.description, 'crd-viewer__description')
    : '';

  let bodyHtml = '';
  if (descriptionHtml || factsHtml) {
    bodyHtml = `
<div class="crd-viewer__body">
  ${descriptionHtml}
  ${factsHtml}
</div>`.trim();
  }

  let childrenHtml = '';
  if (node.children.length > 0) {
    const nestedNodes = node.children
      .map((child) => renderNode(viewerId, child))
      .join('\n');
    childrenHtml = `
<ul class="crd-viewer__children">
  ${nestedNodes}
</ul>`.trim();
  }

  const contentParts = [bodyHtml, childrenHtml].filter(Boolean);
  const contentHtml = contentParts.join('\n');
  const contentBlock = `<div class="crd-viewer__content" id="${contentId}" hidden>${contentHtml}</div>`;

  return `
<li class="crd-viewer__item" id="${nodeId}">
  <div class="crd-viewer__node" data-crd-node data-open="false">
    <div class="crd-viewer__row">
      <button
        type="button"
        class="crd-viewer__summary"
        data-crd-toggle-node
        aria-expanded="false"
        aria-controls="${contentId}"
      >
        <span class="crd-viewer__chevron" aria-hidden="true"></span>
        <span class="crd-viewer__label">${label}${requiredHtml}</span>
        ${badgeHtml}
      </button>
      ${anchorHtml}
    </div>
    ${contentBlock}
  </div>
</li>`.trim();
}

function renderFacts(node: FieldNode): string {
  const facts: Array<[string, string]> = [];
  if (node.default !== undefined && node.default !== null) {
    facts.push(['default', formatValue(node.default)]);
  }
  if (node.enum.length > 0) {
    facts.push(['enum', formatEnum(node.enum)]);
  }
  if (node.field_format) {
    facts.push(['format', node.field_format]);
  }
  if (node.minimum !== undefined || node.maximum !== undefined) {
    facts.push(['range', formatRange(node.minimum, node.maximum)]);
  }

  if (facts.length === 0) return '';

  const items = facts
    .map(
      ([name, value]) =>
        `<span class="crd-viewer__fact"><strong>${escapeHtml(name)}:</strong> ${escapeHtml(value)}</span>`,
    )
    .join('');

  return `<div class="crd-viewer__facts">${items}</div>`;
}

function formatEnum(values: unknown[]): string {
  const rendered = values.map((v) => JSON.stringify(v));
  if (rendered.length <= 4) return rendered.join(', ');
  const preview = rendered.slice(0, 3).join(', ');
  return `${preview}, +${rendered.length - 3} more`;
}

function formatRange(minimum: unknown, maximum: unknown): string {
  if (minimum === undefined || minimum === null) return `<= ${maximum}`;
  if (maximum === undefined || maximum === null) return `>= ${minimum}`;
  return `${minimum} to ${maximum}`;
}

function formatValue(value: unknown): string {
  return JSON.stringify(value);
}

function viewerIdFor(view: CrdView, sequence: number): string {
  const digest = createHash('sha1')
    .update(`${view.source_path}:${view.kind}:${view.group}:${view.version}:${sequence}`)
    .digest('hex')
    .slice(0, 10);
  return `crd-viewer-${digest}`;
}

function nodeIdFor(fieldPath: string): string {
  const normalized = fieldPath
    .replace(/\[\]/g, '-items')
    .replace(/\.\*/g, '-key');
  return normalized
    .replace(/[^a-zA-Z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .toLowerCase();
}
