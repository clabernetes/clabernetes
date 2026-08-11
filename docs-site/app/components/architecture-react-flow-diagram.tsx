import {
  ControlButton,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import {
  Boxes,
  Box,
  Cable,
  Maximize2,
  Minimize2,
  Network,
  ScrollText,
  Server,
  Settings,
  Sparkles,
  type LucideIcon,
} from 'lucide-react';
import { useEffect, useState } from 'react';
import '@xyflow/react/dist/style.css';
import './architecture-react-flow-diagram.css';

type ArchitectureFlowNodeData = {
  badge: string;
  detail: string;
  icon: LucideIcon;
  title: string;
  tone: 'cyan' | 'emerald' | 'gray' | 'indigo' | 'magenta';
  chips?: string[];
};

type HandlePosition = 'bottom' | 'left' | 'right' | 'top';

const SHOW_NODE_CONNECTORS = false;

const reactFlowPositions: Record<HandlePosition, Position> = {
  bottom: Position.Bottom,
  left: Position.Left,
  right: Position.Right,
  top: Position.Top,
};

const flowNodeHandles: Record<
  string,
  { source: HandlePosition[]; target: HandlePosition[] }
> = {
  topology: { source: ['bottom'], target: [] },
  'launcher-profile': { source: ['right'], target: ['top'] },
  nodes: { source: [], target: ['top', 'bottom', 'left'] },
  links: { source: [], target: ['top', 'bottom'] },
  'node-controller': { source: ['top', 'bottom'], target: [] },
  'link-controller': { source: ['top', 'bottom'], target: ['top'] },
  launchers: { source: [], target: ['top'] },
};

const flowNodes: Node<ArchitectureFlowNodeData>[] = [
  {
    id: 'topology',
    type: 'architecture',
    position: { x: 210, y: 24 },
    style: { width: 520 },
    data: {
      badge: 'auxiliary · compiler layer',
      detail: 'containerlab + existing knobs → primitives · owns, corrects drift, prunes',
      icon: Boxes,
      title: 'Topology CR',
      tone: 'magenta',
    },
  },
  {
    id: 'launcher-profile',
    type: 'architecture',
    position: { x: 24, y: 220 },
    style: { width: 250 },
    data: {
      badge: 'policy',
      detail: 'launcher policy',
      icon: ScrollText,
      title: 'LauncherProfile',
      tone: 'gray',
    },
  },
  {
    id: 'nodes',
    type: 'architecture',
    position: { x: 345, y: 220 },
    style: { width: 250 },
    data: {
      badge: 'per node',
      detail: 'explicit reference + payload',
      icon: Box,
      title: 'Node CRs',
      tone: 'cyan',
    },
  },
  {
    id: 'links',
    type: 'architecture',
    position: { x: 666, y: 220 },
    style: { width: 250 },
    data: {
      badge: 'per wire',
      detail: 'one wire + connectivity',
      icon: Cable,
      title: 'Link CRs',
      tone: 'indigo',
    },
  },
  {
    id: 'node-controller',
    type: 'architecture',
    position: { x: 195, y: 438 },
    style: { width: 300 },
    data: {
      badge: 'reconcile',
      detail: 'deployments · services · PVC · status',
      icon: Box,
      title: 'Node controller',
      tone: 'cyan',
    },
  },
  {
    id: 'link-controller',
    type: 'architecture',
    position: { x: 550, y: 438 },
    style: { width: 300 },
    data: {
      badge: 'reconcile',
      detail: 'validates links · allocates tunnel ids',
      icon: Cable,
      title: 'Link controller',
      tone: 'indigo',
    },
  },
  {
    id: 'launchers',
    type: 'architecture',
    position: { x: 150, y: 665 },
    style: { width: 700 },
    data: {
      badge: 'one per network node',
      detail:
        'Reads Nodes and terminating Links, then creates topo.clab.yaml and runs clab + tunnels.',
      icon: Server,
      title: 'Launcher pods',
      tone: 'emerald',
      chips: ['Node + Link watch', 'topo.clab.yaml', 'clab + tunnels'],
    },
  },
];

const flowEdges: Edge[] = [
  {
    id: 'topology-launcher-profile',
    source: 'topology',
    target: 'launcher-profile',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
    label: 'emits',
  },
  {
    id: 'topology-nodes',
    source: 'topology',
    target: 'nodes',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
  },
  {
    id: 'topology-links',
    source: 'topology',
    target: 'links',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
  },
  {
    id: 'node-controller-nodes',
    source: 'node-controller',
    target: 'nodes',
    sourceHandle: 'source-top',
    targetHandle: 'target-bottom',
    type: 'smoothstep',
    label: 'reconciles',
  },
  {
    id: 'link-controller-links',
    source: 'link-controller',
    target: 'links',
    sourceHandle: 'source-top',
    targetHandle: 'target-bottom',
    type: 'smoothstep',
  },
  {
    id: 'launcher-profile-nodes',
    source: 'launcher-profile',
    target: 'nodes',
    sourceHandle: 'source-right',
    targetHandle: 'target-left',
    type: 'smoothstep',
    label: 'ref',
  },
  {
    id: 'node-controller-launchers',
    source: 'node-controller',
    target: 'launchers',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
    label: 'creates',
  },
  {
    id: 'link-controller-launchers',
    source: 'link-controller',
    target: 'launchers',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
  },
];

function ArchitectureFlowNode({ data, id }: NodeProps) {
  const flowData = data as ArchitectureFlowNodeData;
  const Icon = flowData.icon;
  const handles = flowNodeHandles[id] ?? { source: [], target: [] };

  return (
    <div className="c9s-react-flow-node" data-tone={flowData.tone}>
      {handles?.target.map((position) => (
        <Handle
          className="c9s-react-flow-handle"
          id={`target-${position}`}
          key={`target-${position}`}
          position={reactFlowPositions[position]}
          type="target"
        />
      ))}
      <div className="c9s-react-flow-node-header">
        <div className="c9s-react-flow-icon">
          <Icon aria-hidden="true" className="size-4" strokeWidth={1.8} />
        </div>
        <div className="min-w-0">
          <p className="c9s-react-flow-eyebrow">{flowData.badge}</p>
          <p className="c9s-react-flow-title">{flowData.title}</p>
        </div>
      </div>
      <p className="c9s-react-flow-detail">{flowData.detail}</p>
      {flowData.chips ? (
        <div className="c9s-react-flow-chips">
          {flowData.chips.map((chip) => (
            <span key={chip}>{chip}</span>
          ))}
        </div>
      ) : null}
      {handles?.source.map((position) => (
        <Handle
          className="c9s-react-flow-handle"
          id={`source-${position}`}
          key={`source-${position}`}
          position={reactFlowPositions[position]}
          type="source"
        />
      ))}
    </div>
  );
}

const nodeTypes = {
  architecture: ArchitectureFlowNode,
};

export function ArchitectureReactFlowDiagram() {
  const [isFullscreen, setIsFullscreen] = useState(false);

  useEffect(() => {
    if (!isFullscreen) {
      return;
    }

    const previousOverflow = document.body.style.overflow;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setIsFullscreen(false);
      }
    };

    document.body.style.overflow = 'hidden';
    window.addEventListener('keydown', closeOnEscape);

    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener('keydown', closeOnEscape);
    };
  }, [isFullscreen]);

  return (
    <figure
      aria-modal={isFullscreen ? 'true' : undefined}
      className={`not-prose c9s-react-flow-diagram${isFullscreen ? ' is-fullscreen' : ''}`}
      role={isFullscreen ? 'dialog' : undefined}
    >
      <div className="c9s-react-flow-heading">
        {/* <div>
          <p className="c9s-react-flow-kicker">
            <Sparkles aria-hidden="true" className="size-3" />
            comparison
          </p>
          <p className="c9s-react-flow-title-large">React Flow · smoothstep edges</p>
        </div> */}
        {isFullscreen ? (
          <span className="c9s-react-flow-status">press esc to exit</span>
        ) : null}
      </div>
      <div
        aria-label="A React Flow version of the clabernetes architecture, with Topology resources flowing into the primary API, controllers, and launcher pods."
        className="c9s-react-flow-canvas"
        role="img"
      >
        <ReactFlow
          className={SHOW_NODE_CONNECTORS ? 'c9s-react-flow-show-connectors' : undefined}
          key={isFullscreen ? 'fullscreen' : 'inline'}
          defaultEdgeOptions={{
            markerEnd: {
              type: MarkerType.ArrowClosed,
              color: '#20d9e7',
            },
            style: {
              stroke: '#20d9e7',
              strokeWidth: 1.25,
            },
          }}
          edges={flowEdges}
          elementsSelectable={false}
          fitView
          maxZoom={1.25}
          minZoom={0.45}
          nodes={flowNodes}
          nodesConnectable={false}
          nodesDraggable={false}
          nodeTypes={nodeTypes}
          panOnDrag={false}
          panOnScroll={false}
          preventScrolling={false}
          proOptions={{ hideAttribution: true }}
          zoomOnDoubleClick={false}
          zoomOnScroll={false}
        >
          <Controls showInteractive={false}>
            <ControlButton
              aria-label={isFullscreen ? 'Exit fullscreen diagram' : 'Open fullscreen diagram'}
              onClick={() => setIsFullscreen((current) => !current)}
              title={isFullscreen ? 'Exit fullscreen' : 'Open fullscreen'}
            >
              {isFullscreen ? (
                <Minimize2 aria-hidden="true" className="size-3.5" />
              ) : (
                <Maximize2 aria-hidden="true" className="size-3.5" />
              )}
            </ControlButton>
          </Controls>
        </ReactFlow>
      </div>
      <figcaption className="sr-only">
        The same architecture rendered with React Flow smoothstep edges and arrow markers for
        comparison with the custom SVG diagram above.
      </figcaption>
    </figure>
  );
}
