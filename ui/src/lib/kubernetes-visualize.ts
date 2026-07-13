"use server";
import type {Edge} from "@xyflow/react";
import {
  AppsV1Api,
  CoreV1Api,
  KubeConfig,
  type V1DeploymentList,
  type V1ServiceList,
} from "@kubernetes/client-node";
import {
  listClabernetesContainerlabDevV1Alpha1NamespacedLink
} from "@/lib/clabernetes-client";

async function deploymentsByOwner(
  namespace: string,
  owningTopologyName: string,
): Promise<V1DeploymentList> {
  const labelSelector = `clabernetes/topologyOwner=${owningTopologyName}`;
  const kc = new KubeConfig();

  kc.loadFromDefault();

  return await kc
      .makeApiClient(AppsV1Api)
      .listNamespacedDeployment({namespace: namespace, labelSelector: labelSelector})
      .catch((error: unknown) => {
        throw error;
      });
}

async function servicesByOwner(
  namespace: string,
  owningTopologyName: string,
): Promise<V1ServiceList> {
  const labelSelector = `clabernetes/topologyOwner=${owningTopologyName}`;
  const kc = new KubeConfig();

  kc.loadFromDefault();

  return await kc
      .makeApiClient(CoreV1Api)
      .listNamespacedService({namespace: namespace, labelSelector: labelSelector})
      .catch((error: unknown) => {
        throw error;
      });
}

export interface VisualizeObject {
  data: Record<string, unknown>;
  id: string;
  type: string;
  position: {
    x: number;
    y: number;
  };
  style: {
    height: number;
    width: number;
  };
}

// biome-ignore lint/complexity/noExcessiveCognitiveComplexity: its fiiiiiine
export async function visualizeTopology(namespace: string, name: string): Promise<string> {
  const nodes: VisualizeObject[] = [];
  const edges: Edge[] = [];

  const deployments = await deploymentsByOwner(namespace, name);

  const services = await servicesByOwner(namespace, name);

  const links = await listClabernetesContainerlabDevV1Alpha1NamespacedLink({
    path: { namespace: namespace },
    query: { labelSelector: `clabernetes/topologyOwner=${name}` },
  }).catch((error: unknown) => {
    throw error;
  });

  nodes.push({
    data: {
      label: name,
      resourceName: name,
    },
    id: name,
    position: { x: 0, y: 0 },
    style: { height: 90, width: 150 },
    type: "topology",
  });

  for (const deployment of deployments.items) {
    const labels = deployment.metadata?.labels ?? {};
    const deploymentName = labels["clabernetes/name"] ?? "";
    const containerlabNodeName = labels["clabernetes/topologyNode"] ?? "";

    nodes.push({
      data: {
        label: containerlabNodeName,
        resourceName: deployment.metadata?.name as string,
      },
      id: deploymentName,
      position: { x: 0, y: 0 },
      style: { height: 90, width: 150 },
      type: "deployment",
    });

    edges.push({
      id: `${name} / ${deploymentName}`,
      source: name,
      target: deploymentName,
    });
  }

  for (const service of services.items) {
    const labels = service.metadata?.labels ?? {};
    const deploymentName = labels["clabernetes/name"] ?? "";
    const containerlabNodeName = labels["clabernetes/topologyNode"] ?? "";
    const serviceType = labels["clabernetes/topologyServiceType"] ?? "";

    let qualifiedServiceName = `svc/${containerlabNodeName}`;
    if (serviceType === "fabric") {
      qualifiedServiceName += "-vx";
    }

    nodes.push({
      data: {
        label: `${containerlabNodeName}-${serviceType}`,
        serviceKind: serviceType,
        resourceName: service.metadata?.name as string,
      },
      id: qualifiedServiceName,
      position: { x: 0, y: 0 },
      style: { height: 90, width: 150 },
      type: "service",
    });

    edges.push({
      id: `${deploymentName} / ${qualifiedServiceName}`,
      source: deploymentName,
      target: qualifiedServiceName,
    });
  }

  for (const link of links.data?.items ?? []) {
    const endpointA = link.spec?.endpointA;
    const endpointB = link.spec?.endpointB;

    if (endpointA === undefined || endpointB === undefined) {
      continue;
    }

    // the launcher nodes terminating each side of the link live on the link's endpoint labels
    const linkLabels = link.metadata?.labels ?? {};
    const aLauncherNode = linkLabels["clabernetes/linkEndpointA"] ?? endpointA.nodeName;
    const bLauncherNode = linkLabels["clabernetes/linkEndpointB"] ?? endpointB.nodeName;

    const aFabricService = `svc/${aLauncherNode}-vx`;
    const aInterface = `${endpointA.nodeName}-${endpointA.interfaceName}`;
    const bFabricService = `svc/${bLauncherNode}-vx`;
    const bInterface = `${endpointB.nodeName}-${endpointB.interfaceName}`;

    nodes.push({
      data: {
        label: aInterface,
        owningNode: endpointA.nodeName,
      },
      id: aInterface,
      position: { x: 0, y: 0 },
      style: { height: 50, width: 150 },
      type: "interface",
    });

    edges.push({
      id: `${aFabricService} / ${aInterface}`,
      source: aFabricService,
      target: aInterface,
    });

    nodes.push({
      data: {
        label: bInterface,
        owningNode: endpointB.nodeName,
      },
      id: bInterface,
      position: { x: 0, y: 0 },
      style: { height: 50, width: 150 },
      type: "interface",
    });

    edges.push({
      id: `${bFabricService} / ${bInterface}`,
      source: bFabricService,
      target: bInterface,
    });

    edges.push({
      id: `${aInterface} / ${bInterface}`,
      source: aInterface,
      target: bInterface,
    });
  }

  return JSON.stringify({
    edges: edges,
    nodes: nodes,
  });
}
