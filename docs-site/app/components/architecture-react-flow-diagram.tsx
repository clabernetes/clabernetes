import {
  ControlButton,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  type BuiltInEdge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import {
  Boxes,
  Box,
  Cable,
  Maximize2,
  Minimize2,
  ScrollText,
  Server,
  Settings,
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
  chips?: string[];
};

type HandlePosition = 'bottom' | 'left' | 'right' | 'top';

const SHOW_NODE_CONNECTORS = false;
const CARD_STYLE = { width: 280, height: 152 };
// Keep connector lead-ins shorter than half the narrowest row gap.
const EDGE_PATH_OPTIONS = { offset: 12 };

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
  'node-profile': { source: ['right'], target: ['top'] },
  nodes: { source: [], target: ['top', 'bottom', 'left'] },
  links: { source: [], target: ['top', 'bottom'] },
  'node-controller': { source: ['top', 'bottom'], target: [] },
  'link-controller': { source: ['top', 'bottom'], target: ['top'] },
  'planning-pods': { source: ['right'], target: ['top'] },
  'device-pods': { source: [], target: ['top', 'left'] },
};

const flowNodes: Node<ArchitectureFlowNodeData>[] = [
  {
    id: 'topology',
    type: 'architecture',
    position: { x: 344, y: 24 },
    style: CARD_STYLE,
    data: {
      badge: 'auxiliary · compiler layer',
      detail: 'containerlab + existing knobs → primitives · owns, corrects drift, prunes',
      icon: Boxes,
      title: 'Topology CR',
    },
  },
  {
    id: 'node-profile',
    type: 'architecture',
    position: { x: 24, y: 220 },
    style: CARD_STYLE,
    data: {
      badge: 'policy',
      detail: 'reusable workload policy',
      icon: ScrollText,
      title: 'NodeProfile',
    },
  },
  {
    id: 'nodes',
    type: 'architecture',
    position: { x: 344, y: 220 },
    style: CARD_STYLE,
    data: {
      badge: 'per node',
      detail: 'explicit reference + payload',
      icon: Box,
      title: 'Node CRs',
    },
  },
  {
    id: 'links',
    type: 'architecture',
    position: { x: 664, y: 220 },
    style: CARD_STYLE,
    data: {
      badge: 'per wire',
      detail: 'one wire per resource',
      icon: Cable,
      title: 'Link CRs',
    },
  },
  {
    id: 'node-controller',
    type: 'architecture',
    position: { x: 140, y: 416 },
    style: CARD_STYLE,
    data: {
      badge: 'reconcile',
      detail: 'image metadata · one Deployment per Node/group · status',
      icon: Box,
      title: 'Node controller',
      chips: ['fabric <name>-vx', 'expose svc', 'alias svc'],
    },
  },
  {
    id: 'link-controller',
    type: 'architecture',
    position: { x: 548, y: 416 },
    style: CARD_STYLE,
    data: {
      badge: 'reconcile',
      detail: 'validates links · allocates cluster-wide tunnel ids',
      icon: Cable,
      title: 'Link controller',
    },
  },
  {
    id: 'planning-pods',
    type: 'architecture',
    position: { x: 140, y: 720 },
    style: CARD_STYLE,
    data: {
      badge: 'short-lived · locked down',
      detail: 'containerlab module records a device plan → immutable ConfigMap',
      icon: Settings,
      title: 'Planning Pods',
    },
  },
  {
    id: 'device-pods',
    type: 'architecture',
    position: { x: 548, y: 720 },
    style: CARD_STYLE,
    data: {
      badge: 'one per Node / group',
      detail: 'kubelet runs each device image · chassis cards share the Pod',
      icon: Server,
      title: 'Device Pods',
      chips: [
        'preparation init',
        'VXLAN sidecar',
        'device container(s)',
      ],
    },
  },
];

const flowEdges: BuiltInEdge[] = [
  {
    id: 'topology-node-profile',
    source: 'topology',
    target: 'node-profile',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
    pathOptions: EDGE_PATH_OPTIONS,
    label: 'emits',
  },
  {
    id: 'topology-nodes',
    source: 'topology',
    target: 'nodes',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
    pathOptions: EDGE_PATH_OPTIONS,
  },
  {
    id: 'topology-links',
    source: 'topology',
    target: 'links',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
    pathOptions: EDGE_PATH_OPTIONS,
  },
  {
    id: 'node-controller-nodes',
    source: 'node-controller',
    target: 'nodes',
    sourceHandle: 'source-top',
    targetHandle: 'target-bottom',
    type: 'smoothstep',
    pathOptions: EDGE_PATH_OPTIONS,
    label: 'reconciles',
  },
  {
    id: 'link-controller-links',
    source: 'link-controller',
    target: 'links',
    sourceHandle: 'source-top',
    targetHandle: 'target-bottom',
    type: 'smoothstep',
    pathOptions: EDGE_PATH_OPTIONS,
  },
  {
    id: 'node-profile-nodes',
    source: 'node-profile',
    target: 'nodes',
    sourceHandle: 'source-right',
    targetHandle: 'target-left',
    type: 'smoothstep',
    pathOptions: EDGE_PATH_OPTIONS,
    label: 'ref',
  },
  {
    id: 'node-controller-planning-pods',
    source: 'node-controller',
    target: 'planning-pods',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
    pathOptions: EDGE_PATH_OPTIONS,
    label: 'plans',
  },
  {
    id: 'node-controller-device-pods',
    source: 'node-controller',
    target: 'device-pods',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
    pathOptions: { ...EDGE_PATH_OPTIONS, stepPosition: 0.25 },
    label: 'creates',
  },
  {
    id: 'planning-pods-device-pods',
    source: 'planning-pods',
    target: 'device-pods',
    sourceHandle: 'source-right',
    targetHandle: 'target-left',
    type: 'smoothstep',
    pathOptions: EDGE_PATH_OPTIONS,
    label: 'plan',
  },
  {
    id: 'link-controller-device-pods',
    source: 'link-controller',
    target: 'device-pods',
    sourceHandle: 'source-bottom',
    targetHandle: 'target-top',
    type: 'smoothstep',
    pathOptions: EDGE_PATH_OPTIONS,
    label: 'tunnel ids',
  },
];

function ArchitectureFlowNode({ data, id }: NodeProps) {
  const flowData = data as ArchitectureFlowNodeData;
  const Icon = flowData.icon;
  const handles = flowNodeHandles[id] ?? { source: [], target: [] };

  return (
    <div className="c9s-react-flow-node">
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
        {isFullscreen ? (
          <span className="c9s-react-flow-status">press esc to exit</span>
        ) : null}
      </div>
      <div
        aria-label="The clabernetes architecture, with Topology resources compiling into the primary API, controllers running planning pods, and device pods whose connectivity sidecars wire the lab together."
        className="c9s-react-flow-canvas"
        role="img"
      >
        <ReactFlow
          className={SHOW_NODE_CONNECTORS ? 'c9s-react-flow-show-connectors' : undefined}
          key={isFullscreen ? 'fullscreen' : 'inline'}
          defaultEdgeOptions={{
            markerEnd: {
              type: MarkerType.ArrowClosed,
              color: 'var(--color-fd-muted-foreground)',
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
        Topology compiles into NodeProfile, Node, and Link resources; the node controller
        runs planning pods and renders device pods, while the link controller allocates tunnel
        ids consumed by each pod's connectivity sidecar to wire the pods together.
      </figcaption>
    </figure>
  );
}
