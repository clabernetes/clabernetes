import { useId, useRef } from 'react';
import { Maximize2, X } from 'lucide-react';
import containerDark from '@/assets/clab-icons/container-dark.svg';
import containerLight from '@/assets/clab-icons/container-light.svg';
import routerDark from '@/assets/clab-icons/router-dark.svg';
import routerLight from '@/assets/clab-icons/router-light.svg';
import switchDark from '@/assets/clab-icons/switch-dark.svg';
import switchLight from '@/assets/clab-icons/switch-light.svg';
import './release-runtime-diagram.css';

type DiagramKind = 'startup' | 'networks' | 'netem';

const titles = {
  startup: 'From device plan to a running Pod',
  networks: 'Two networks, two jobs',
  netem: 'Where each impairment applies',
};

const viewBoxes = {
  startup: '0 0 900 440',
  networks: '0 0 900 500',
  netem: '0 0 840 470',
};

const descriptions = {
  startup: 'TLDR: The network device Pod contains a native NOS container, a privileged connectivity sidecar, and an init container that stages files and verifies their planned digests.',
  networks: 'Two device Pods communicate over separate transports: the routed management mesh on UDP 14789, and Ethernet link wires on UDP 14790. Management gateways remain Pod-local. Link frames, carrier updates and heartbeats share the wire path.',
  netem: 'The brief link from the example above, with srl1 and srl2 in separate Pods. Each color follows one endpoint\'s packets and the netem entry in its position on the link, which impairs the packets that endpoint sends. The peer Pod\'s clabwire sidecar programs it as the root qdisc of its sidecar leg, where the wire delivers those packets to the device. The devices\' own e1-1 interfaces carry no qdisc.',
};

const icons = {
  switch: [switchLight, switchDark],
  router: [routerLight, routerDark],
  container: [containerLight, containerDark],
};

function DeviceIcon({ x, y, icon = 'switch' }: { x: number; y: number; icon?: keyof typeof icons }) {
  const [light, dark] = icons[icon];
  return <>
    <image className="dark:hidden" href={light} x={x} y={y} width="40" height="40" />
    <image className="hidden dark:block" href={dark} x={x} y={y} width="40" height="40" />
  </>;
}

// Each Pod receives its peer's impairment on its own sidecar leg. Cards sit on the inner side of
// each Pod, around the wire; the port labels and the callout face away from each other. Each
// endpoint has one color for its packets' lane and for the impairment its netem entry declares.
const netemPodWidth = 350;
const netemCardWidth = 150;
const netemCalloutWidth = 150;
const netemLaneGap = 7;
const netemQdiscY = 280;
const netemPods = [
  { name: 'srl1', x: 15, inner: 1, endpoint: 'a', wireY: 347, peer: 'srl2', peerEndpoint: 'b', netem: ['rate 10000 kbit/s'] },
  { name: 'srl2', x: 475, inner: -1, endpoint: 'b', wireY: 333, peer: 'srl1', peerEndpoint: 'a', netem: ['delay 50ms', 'jitter 5ms', 'loss 0.5%'] },
].map((pod) => {
  const cardX = pod.inner > 0 ? pod.x + netemPodWidth - 16 - netemCardWidth : pod.x + 16;
  const cx = cardX + netemCardWidth / 2;
  return {
    ...pod, cardX, cx,
    calloutX: pod.inner > 0 ? pod.x + 16 : pod.x + netemPodWidth - 16 - netemCalloutWidth,
    // The inner lane carries this Pod's device packets down to the wire; the outer lane carries
    // the peer's packets up from the sidecar leg, through the impairment, to the device.
    sendX: cx + pod.inner * netemLaneGap,
    receiveX: cx - pod.inner * netemLaneGap,
  };
});

function NetemDrawing({ id }: { id: string }) {
  const [left, right] = netemPods;
  return <>
    {netemPods.map(({ name, x }) => <g key={name}>
      <rect className="runtime-pod" x={x} y="20" width={netemPodWidth} height="390" rx="12" />
      <text className="runtime-title" x={x + 20} y="52">Pod {name}</text>
      <text className="runtime-muted" x={x + netemPodWidth - 20} y="52" textAnchor="end">one network namespace</text>
    </g>)}
    {netemPods.map(({ name, cx }) => <rect key={name} className="runtime-tube" x={cx - 14} y="160" width="28" height="140" />)}
    <rect className="runtime-tube" x={left.cardX + netemCardWidth} y="326" width={right.cardX - left.cardX - netemCardWidth} height="28" />
    {/* Each lane runs from the sender's e1-1, behind both sidecar cards and across the wire, to
        the peer's e1-1; the one crossing of the two lanes stays hidden behind a sidecar card. */}
    {netemPods.map((pod) => {
      const peer = pod === left ? right : left;
      return <g key={pod.name} className={`runtime-endpoint-${pod.endpoint}`}>
        <path className="runtime-lane" d={`M ${pod.sendX} 160 V ${pod.wireY} H ${peer.receiveX} V 170`} markerEnd={`url(#${id}-lane-${pod.endpoint})`} />
        <path className="runtime-lane" d={`M ${pod.sendX} 214 V 226`} markerEnd={`url(#${id}-lane-${pod.endpoint})`} />
        <path className="runtime-lane" d={`M ${420 - pod.inner * 10} ${pod.wireY} H ${420 + pod.inner * 10}`} markerEnd={`url(#${id}-lane-${pod.endpoint})`} />
      </g>;
    })}
    {netemPods.map(({ name, endpoint, cardX, cx, inner, receiveX, calloutX, peer, peerEndpoint, netem }) => {
      const labelX = cx + inner * 24;
      const anchor = inner > 0 ? 'start' : 'end';
      const mutedY = 52 + netem.length * 22;
      const calloutHeight = mutedY + 18;
      const calloutY = netemQdiscY - calloutHeight / 2;
      const leaderStart = inner > 0 ? calloutX + netemCalloutWidth : calloutX;
      return <g key={name}>
        <g className={`runtime-endpoint-${endpoint}`}>
          <rect className="runtime-card runtime-node-card" x={cardX} y="80" width={netemCardWidth} height="80" rx="10" />
          <DeviceIcon x={cardX + 16} y={100} icon="router" />
          <text className="runtime-title runtime-lane-text" x={cardX + 70} y="116">{name}</text>
          <text className="runtime-muted" x={cardX + 70} y="138">SR Linux</text>
        </g>
        <rect className="runtime-card" x={cardX} y="300" width={netemCardWidth} height="80" rx="10" />
        <DeviceIcon x={cardX + 16} y={320} icon="container" />
        <text className="runtime-title" x={cardX + 70} y="336">clabwire</text>
        <text className="runtime-muted" x={cardX + 70} y="358">sidecar</text>
        <text className="runtime-mono" x={labelX} y="184" textAnchor={anchor}>e1-1</text>
        <text className="runtime-muted" x={labelX} y="236" textAnchor={anchor}>veth</text>
        <text className="runtime-mono" x={labelX} y="286" textAnchor={anchor}>c9ss…</text>
        <g className={`runtime-endpoint-${peerEndpoint}`}>
          <path className="runtime-leader" d={`M ${leaderStart} ${netemQdiscY} H ${receiveX - inner * 10}`} />
          <circle className="runtime-qdisc-halo" cx={receiveX} cy={netemQdiscY} r="9" />
          <circle className="runtime-qdisc" cx={receiveX} cy={netemQdiscY} r="5" />
          <rect className="runtime-callout" x={calloutX} y={calloutY} width={netemCalloutWidth} height={calloutHeight} rx="10" />
          <text className="runtime-lane-text" x={calloutX + 14} y={calloutY + 26}>{peer}:e1-1 netem</text>
          {netem.map((line, index) => <text key={line} x={calloutX + 14} y={calloutY + 50 + index * 22}>{line}</text>)}
          <text className="runtime-muted" x={calloutX + 14} y={calloutY + mutedY}>{peer} → {name} packets</text>
        </g>
      </g>;
    })}
    <text className="runtime-title" x="420" y="312" textAnchor="middle">c9s wire</text>
    <text className="runtime-muted" x="420" y="378" textAnchor="middle">UDP 14790</text>
    {netemPods.map(({ name, peer, endpoint }, index) => <g key={name} className={`runtime-endpoint-${endpoint}`}>
      <path className="runtime-lane" d={`M ${31 + index * 186} 441 H ${55 + index * 186}`} />
      <text className="runtime-muted" x={63 + index * 186} y="446">{name} → {peer} packets</text>
    </g>)}
    <g className="runtime-qdisc-key">
      <circle className="runtime-qdisc-halo" cx="412" cy="441" r="9" />
      <circle className="runtime-qdisc" cx="412" cy="441" r="5" />
      <text className="runtime-muted" x="430" y="446">netem root qdisc where packets leave the sidecar leg</text>
    </g>
  </>;
}

function RuntimeDrawing({ kind }: { kind: DiagramKind }) {
  const id = useId();
  return (
    <svg className={`runtime-${kind}`} viewBox={viewBoxes[kind]} role="img" aria-labelledby={`${id}-title ${id}-description`}>
      <title id={`${id}-title`}>{titles[kind]}</title>
      <desc id={`${id}-description`}>{descriptions[kind]}</desc>
      <defs>
        <marker id={`${id}-arrow`} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" />
        </marker>
        {/* A marker renders in the context of its own definition, so each lane color has one. */}
        {(['a', 'b'] as const).map((endpoint) => <marker key={endpoint} id={`${id}-lane-${endpoint}`} className={`runtime-endpoint-${endpoint}`} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="3.2" markerHeight="3.2" orient="auto-start-reverse">
          <path className="runtime-lane-fill" d="M 0 0 L 10 5 L 0 10 z" />
        </marker>)}
      </defs>
      {kind === 'netem' ? <NetemDrawing id={id} /> : kind === 'startup' ? <>
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
  const title = titles[kind];
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
