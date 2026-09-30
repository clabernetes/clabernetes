import { useId, useRef } from 'react';
import { Maximize2, X } from 'lucide-react';
import switchDark from '@/assets/clab-icons/switch-dark.svg';
import switchLight from '@/assets/clab-icons/switch-light.svg';
import './release-runtime-diagram.css';

type DiagramKind = 'startup' | 'networks';

const descriptions = {
  startup: 'TLDR: The network device Pod contains a native NOS container, a privileged connectivity sidecar, and an init container that stages files and verifies their planned digests.',
  networks: 'Two device Pods communicate over separate transports: the routed management mesh on UDP 14789, and Ethernet link wires on UDP 14790. Management gateways remain Pod-local. Link frames, carrier updates and heartbeats share the wire path.',
};

function DeviceIcon({ x, y }: { x: number; y: number }) {
  return <>
    <image className="dark:hidden" href={switchLight} x={x} y={y} width="40" height="40" />
    <image className="hidden dark:block" href={switchDark} x={x} y={y} width="40" height="40" />
  </>;
}

function RuntimeDrawing({ kind }: { kind: DiagramKind }) {
  const id = useId();
  return (
    <svg viewBox={kind === 'startup' ? '0 0 900 440' : '0 0 900 500'} role="img" aria-labelledby={`${id}-title ${id}-description`}>
      <title id={`${id}-title`}>{kind === 'startup' ? 'From device plan to a running Pod' : 'Two networks, two jobs'}</title>
      <desc id={`${id}-description`}>{descriptions[kind]}</desc>
      <defs>
        <marker id={`${id}-arrow`} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" />
        </marker>
      </defs>
      {kind === 'startup' ? <>
        <rect className="runtime-card" x="24" y="20" width="852" height="90" rx="12" />
        <text className="runtime-title" x="48" y="50">Reusable planner pool</text>
        <text x="48" y="75">Imported containerlab hooks record what the device needs.</text>
        <text className="runtime-muted" x="48" y="96">New input: fresh process · unchanged accepted input: reuse the plan</text>
        <path className="runtime-arrow" d="M 450 110 V 170" markerEnd={`url(#${id}-arrow)`} />
        <text className="runtime-muted" x="468" y="144">validated device plan</text>
        <rect className="runtime-pod" x="24" y="180" width="852" height="210" rx="12" />
        <text className="runtime-title" x="48" y="210">Network device Pod</text>
        <text className="runtime-muted" x="852" y="210" textAnchor="end">shared network namespace</text>
        <rect className="runtime-card" x="48" y="230" width="230" height="130" rx="10" />
        <text className="runtime-title" x="64" y="260">1. Prepare</text>
        <text x="64" y="289">Stage files and verify</text>
        <text x="64" y="311">their planned digests.</text>
        <text className="runtime-muted" x="64" y="340">Init container completes</text>
        <path className="runtime-arrow" d="M 278 292 H 333" markerEnd={`url(#${id}-arrow)`} />
        <rect className="runtime-card" x="338" y="230" width="230" height="130" rx="10" />
        <text className="runtime-title" x="354" y="260">2. Connect</text>
        <text x="354" y="289">clabwire prepares local</text>
        <text x="354" y="311">interfaces before boot.</text>
        <text className="runtime-muted" x="354" y="340">Sidecar keeps running</text>
        <path className="runtime-arrow" d="M 568 292 H 623" markerEnd={`url(#${id}-arrow)`} />
        <rect className="runtime-card" x="628" y="230" width="224" height="130" rx="10" />
        <DeviceIcon x={802} y={239} />
        <text className="runtime-title" x="644" y="260">3. Run device</text>
        <text x="644" y="289">Actual NOS container</text>
        <text x="644" y="311">Native logs and exec</text>
        <text className="runtime-muted" x="644" y="340">Kubelet pulls the image</text>
      </> : <>
        {[{ x: 24, name: 'Device Pod A', ip: '172.20.20.11' }, { x: 626, name: 'Device Pod B', ip: '172.20.20.12' }].map(({ x, name, ip }) => <g key={name}>
          <rect className="runtime-pod" x={x} y="20" width="250" height="414" rx="12" />
          <DeviceIcon x={x + 20} y={40} />
          <text className="runtime-title" x={x + 72} y="65">{name}</text>
          <text x={x + 20} y="106">Network device</text>
          <path className="runtime-line" d={`M ${x + 125} 120 V 150`} />
          <rect className="runtime-card" x={x + 20} y="150" width="210" height="115" rx="10" />
          <text className="runtime-title" x={x + 36} y="178">Management port</text>
          <text x={x + 36} y="204">{ip}</text>
          <text className="runtime-muted" x={x + 36} y="232">Pod-local gateway</text>
          <text className="runtime-muted" x={x + 36} y="252">Proxy ARP / IPv6 ND</text>
          <rect className="runtime-card" x={x + 20} y="315" width="210" height="94" rx="10" />
          <text className="runtime-title" x={x + 36} y="345">Data port · veth</text>
          <text x={x + 36} y="373">clabwire wire pump</text>
          <text className="runtime-muted" x={x + 36} y="396">Device sees an L2 cable</text>
        </g>)}
        <text className="runtime-title" x="450" y="65" textAnchor="middle">Kubernetes Pod network</text>
        <text className="runtime-muted" x="450" y="89" textAnchor="middle">same worker or different workers</text>
        <text className="runtime-title" x="450" y="163" textAnchor="middle">Routed management mesh</text>
        <text x="450" y="188" textAnchor="middle">UDP 14789 · peer IP traffic</text>
        <path className="runtime-arrow runtime-management" d="M 260 214 H 640" markerStart={`url(#${id}-arrow)`} markerEnd={`url(#${id}-arrow)`} />
        <text className="runtime-muted" x="450" y="245" textAnchor="middle">Peer directory supplies destinations</text>
        <text className="runtime-muted" x="450" y="267" textAnchor="middle">Other broadcasts stay Pod-local</text>
        <text className="runtime-title" x="450" y="326" textAnchor="middle">Ethernet link wire</text>
        <text x="450" y="351" textAnchor="middle">UDP 14790 · frames and fragments</text>
        <path className="runtime-arrow" d="M 260 376 H 640" markerStart={`url(#${id}-arrow)`} markerEnd={`url(#${id}-arrow)`} />
        <text className="runtime-muted" x="450" y="407" textAnchor="middle">Carrier + heartbeats share this path</text>
        <text className="runtime-muted" x="450" y="462" textAnchor="middle">Port shutdown or peer loss lowers carrier at the other end; dropped frames are not retransmitted.</text>
        <text className="runtime-muted" x="450" y="486" textAnchor="middle">Management overlay is optional. Declared data links remain active when it is disabled.</text>
      </>}
    </svg>
  );
}

export function ReleaseRuntimeDiagram({ kind }: { kind: DiagramKind }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = kind === 'startup' ? 'From device plan to a running Pod' : 'Two networks, two jobs';
  return (
    <figure className="not-prose release-runtime-diagram">
      <div className="runtime-heading">
        <span>{title}</span>
        <button type="button" onClick={() => dialog.current?.showModal()} aria-label={`Expand ${title}`}>
          <Maximize2 size={16} aria-hidden="true" /> Expand
        </button>
      </div>
      <p className="runtime-scroll-hint">Scroll horizontally to see the full diagram.</p>
      <div className="runtime-scroll" role="region" aria-label={title} tabIndex={0}><RuntimeDrawing kind={kind} /></div>
      <figcaption>{descriptions[kind]}</figcaption>
      <dialog ref={dialog} aria-label={title}>
        <div className="runtime-heading">
          <span>{title}</span>
          <button type="button" onClick={() => dialog.current?.close()} aria-label="Close diagram">
            <X size={18} aria-hidden="true" /> Close
          </button>
        </div>
        <p className="runtime-scroll-hint">Scroll horizontally to see the full diagram.</p>
        <div className="runtime-scroll" role="region" aria-label={title} tabIndex={0}><RuntimeDrawing kind={kind} /></div>
      </dialog>
    </figure>
  );
}
