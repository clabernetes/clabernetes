"use server";
import "../fetch.config";
import { CoreV1Api, KubeConfig } from "@kubernetes/client-node";

import {
  createC9sRunV1Alpha1NamespacedTopology,
  deleteC9sRunV1Alpha1NamespacedTopology,
  listC9sRunV1Alpha1NamespacedLauncherprofile,
  listC9sRunV1Alpha1NamespacedLink,
  listC9sRunV1Alpha1NamespacedNode,
  listC9sRunV1Alpha1NamespacedTopology,
  listC9sRunV1Alpha1TopologyForAllNamespaces,
  replaceC9sRunV1Alpha1NamespacedTopology,
} from "@/lib/clabernetes-client";

export async function listTopologies(): Promise<string> {
  const response = await listC9sRunV1Alpha1TopologyForAllNamespaces().catch(
    (error: unknown) => {
      throw error;
    },
  );

  return JSON.stringify(response.data?.items);
}

export async function listNamespacedTopologies(namespace: string): Promise<string> {
  const response = await listC9sRunV1Alpha1NamespacedTopology({
    path: { namespace: namespace },
  }).catch((error: unknown) => {
    throw error;
  });

  return JSON.stringify(
    response.data?.items.map((namespace) => {
      return namespace.metadata?.name;
    }),
  );
}

export async function listTopologyNodes(namespace: string, topologyName: string): Promise<string> {
  const response = await listC9sRunV1Alpha1NamespacedNode({
    path: { namespace: namespace },
    query: { labelSelector: `clabernetes/topologyOwner=${topologyName}` },
  }).catch((error: unknown) => {
    throw error;
  });

  return JSON.stringify(response.data?.items);
}

export async function listTopologyLinks(namespace: string, topologyName: string): Promise<string> {
  const response = await listC9sRunV1Alpha1NamespacedLink({
    path: { namespace: namespace },
    query: { labelSelector: `clabernetes/topologyOwner=${topologyName}` },
  }).catch((error: unknown) => {
    throw error;
  });

  return JSON.stringify(response.data?.items);
}

export async function listTopologyLauncherProfiles(namespace: string, topologyName: string): Promise<string> {
  const response = await listC9sRunV1Alpha1NamespacedLauncherprofile({
    path: { namespace: namespace },
    query: { labelSelector: `clabernetes/topologyOwner=${topologyName}` },
  }).catch((error: unknown) => {
    throw error;
  });

  return JSON.stringify(response.data?.items);
}

export async function deleteTopology(namespace: string, name: string): Promise<string> {
  const response = await deleteC9sRunV1Alpha1NamespacedTopology({
    path: { name: name, namespace: namespace },
  });

  return JSON.stringify(response);
}

export async function updateTopology(
  namespace: string,
  name: string,
  body: string,
): Promise<string> {
  const response = await replaceC9sRunV1Alpha1NamespacedTopology({
    body: JSON.parse(body),
    path: { name: name, namespace: namespace },
  });

  return JSON.stringify(response);
}

export async function listNamespaces(): Promise<string> {
  const kc = new KubeConfig();

  kc.loadFromDefault();

  const response = await kc
    .makeApiClient(CoreV1Api)
    .listNamespace()
    .catch((error: unknown) => {
      throw error;
    });

  return JSON.stringify(
    response.items.map((namespace) => {
      return namespace.metadata?.name;
    }),
  );
}

export async function createTopology(namespace: string, body: string): Promise<string> {
  const response = await createC9sRunV1Alpha1NamespacedTopology({
    body: JSON.parse(body),
    path: { namespace: namespace },
  });

  return JSON.stringify(response);
}

export async function listNamespacedPullSecrets(namespace: string): Promise<string> {
  const kc = new KubeConfig();

  kc.loadFromDefault();

  const response = await kc
    .makeApiClient(CoreV1Api)
    .listNamespacedSecret({
      namespace: namespace,
      fieldSelector: "type=kubernetes.io/dockerconfigjson",
    })
    .catch((error: unknown) => {
      throw error;
    });

  return JSON.stringify(
    response.items.map((namespace) => {
      return namespace.metadata?.name;
    }),
  );
}

export async function listNamespacedSecrets(namespace: string): Promise<string> {
  const kc = new KubeConfig();

  kc.loadFromDefault();

  const response = await kc
    .makeApiClient(CoreV1Api)
    .listNamespacedSecret({ namespace: namespace, fieldSelector: "type=Opaque" })
    .catch((error: unknown) => {
      throw error;
    });

  return JSON.stringify(
    response.items.map((namespace) => {
      return namespace.metadata?.name;
    }),
  );
}
