import type { ReactNode } from 'react';

function Key({ children }: { children: ReactNode }) {
  return <span className="lab-yaml-key">{children}</span>;
}

function Value({ children }: { children: ReactNode }) {
  return <span className="lab-yaml-value">{children}</span>;
}

export function LabYaml() {
  return (
    <>
      <div className="lab-yaml-layout">
        <div className="lab-yaml-editor">
          <div className="lab-yaml-toolbar">
            <span aria-hidden="true" className="lab-yaml-file-icon">{'{ }'}</span>
            <span>two-node.c9s.yaml</span>
            <span className="ml-auto text-xs tracking-widest uppercase">YAML</span>
          </div>
          <pre aria-label="c9s Topology resource with Nokia SR Linux and SONiC"><code>
            <span className="lab-yaml-line"><Key>apiVersion</Key>: <Value>c9s.run/v1alpha1</Value>{'\n'}</span>
            <span className="lab-yaml-line"><Key>kind</Key>: <Value>Topology</Value>{'\n'}</span>
            <span className="lab-yaml-line"><Key>metadata</Key>:{'\n'}</span>
            <span className="lab-yaml-line">{'  '}<Key>name</Key>: <Value>two-node-lab</Value>{'\n'}</span>
            <span className="lab-yaml-line"><Key>spec</Key>:{'\n'}</span>
            <span className="lab-yaml-line">{'  '}<Key>definition</Key>:{'\n'}</span>
            <span className="lab-yaml-line">{'    '}<Key>containerlab</Key>: <Value>|</Value>{'\n'}</span>
            <span className="lab-yaml-line">{'      '}<Key>name</Key>: <Value>two-node-lab</Value>{'\n'}</span>
            <span className="lab-yaml-line">{'      '}<Key>topology</Key>:{'\n'}</span>
            <span className="lab-yaml-region lab-yaml-nodes">
              <span className="lab-yaml-line">{'        '}<Key>nodes</Key>:{'\n'}</span>
              <span className="lab-yaml-line">{'          '}<Key>srl</Key>:{'\n'}</span>
              <span className="lab-yaml-line">{'            '}<Key>kind</Key>: <Value>nokia_srlinux</Value>{'\n'}</span>
              <span className="lab-yaml-line">{'            '}<Key>image</Key>: <Value>ghcr.io/nokia/srlinux</Value>{'\n'}</span>
              <span className="lab-yaml-line">{'          '}<Key>sonic</Key>:{'\n'}</span>
              <span className="lab-yaml-line">{'            '}<Key>kind</Key>: <Value>sonic-vm</Value>{'\n'}</span>
              <span className="lab-yaml-line">{'            '}<Key>image</Key>: <Value>sonic-vm:latest</Value>{'\n'}</span>
            </span>
            <span className="lab-yaml-line">{'\n'}</span>
            <span className="lab-yaml-region lab-yaml-links">
              <span className="lab-yaml-line">{'        '}<Key>links</Key>:{'\n'}</span>
              <span className="lab-yaml-line">{'          - '}<Key>endpoints</Key>:{'\n'}</span>
              <span className="lab-yaml-line">{'              - '}<Value>"srl:e1-1"</Value>{'\n'}</span>
              <span className="lab-yaml-line">{'              - '}<Value>"sonic:eth1"</Value></span>
            </span>
          </code></pre>
        </div>

        <div className="lab-yaml-callouts">
          <div className="lab-yaml-callout lab-yaml-node-callout">
            <span className="lab-yaml-number" aria-hidden="true">01</span>
            <div>
              <h3>Node definitions</h3>
              <p>One Nokia SR Linux router and one SONiC switch. Give each node a name, a kind, and a container image.</p>
              <span className="lab-yaml-tag">2 Node resources → 2 device Pods</span>
            </div>
          </div>
          <div className="lab-yaml-callout lab-yaml-link-callout">
            <span className="lab-yaml-number" aria-hidden="true">02</span>
            <div>
              <h3>Link definition</h3>
              <p>Connect <code>srl:e1-1</code> to <code>sonic:eth1</code>. Each endpoint identifies a node and its interface.</p>
              <span className="lab-yaml-tag">1 Link resource → 1 point-to-point wire</span>
            </div>
          </div>
        </div>
      </div>
    </>
  );
}
