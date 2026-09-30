import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { parse } from 'yaml';
import { expect, it } from 'vitest';
import { LabYaml } from '../components/lab-yaml';

it('renders a c9s Topology CR containing the two-node lab', () => {
  const html = renderToStaticMarkup(createElement(LabYaml));
  const code = html.match(/<pre[^>]*><code>([\s\S]*?)<\/code><\/pre>/)?.[1];
  expect(code).toBeDefined();
  const manifest = parse(code!.replace(/<[^>]*>/g, '').replace(/&quot;/g, '"'));
  expect(manifest.apiVersion).toBe('c9s.run/v1alpha1');
  expect(manifest.kind).toBe('Topology');
  expect(manifest.metadata.name).toBe('two-node-lab');
  const lab = parse(manifest.spec.definition.containerlab);
  expect(lab.topology.nodes).toEqual({
    srl: { kind: 'nokia_srlinux', image: 'ghcr.io/nokia/srlinux' },
    sonic: { kind: 'sonic-vm', image: 'sonic-vm:latest' },
  });
  expect(lab.topology.links).toEqual([{ endpoints: ['srl:e1-1', 'sonic:eth1'] }]);
});
