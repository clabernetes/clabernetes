import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { type ReactElement, useState } from "react";
import type {
  ClabernetesContainerlabDevLauncherprofileV1Alpha1,
  ClabernetesContainerlabDevLinkV1Alpha1,
  ClabernetesContainerlabDevNodeV1Alpha1,
  ClabernetesContainerlabDevTopologyV1Alpha1,
} from "@/lib/clabernetes-client";
import type { Row } from "@tanstack/react-table";
import { CircleAlert, CircleCheck, CircleHelp } from "lucide-react";
import { Button } from "@/components/ui/button.tsx";
import { getExpandCollapseIcon } from "@/components/topologies-table/table.tsx";
import {
  listTopologyLauncherProfiles,
  listTopologyLinks,
  listTopologyNodes,
} from "@/lib/kubernetes.ts";
import { useQuery } from "@tanstack/react-query";

function getTopologyReadyIcon(
  statusProbesEnabled: boolean | undefined,
  topologyReady: boolean | undefined,
): ReactElement {
  if (!statusProbesEnabled) {
    return (
      <div className="relative group">
        <CircleHelp className="h-4 w-4 mt-1 fill-yellow-500" />
        <span className="absolute left-1/2 -translate-x-1/2 bottom-full mb-2 w-max bg-gray-800 text-white text-sm rounded px-2 py-1 opacity-0 group-hover:opacity-100 transition-opacity">
          status probes not enabled
        </span>
      </div>
    );
  }

  switch (topologyReady) {
    case undefined:
      return (
        <div className="relative group">
          <CircleHelp className="h-4 w-4 mt-1 fill-yellow-500" />
          <span className="absolute left-1/2 -translate-x-1/2 bottom-full mb-2 w-max bg-gray-800 text-white text-sm rounded px-2 py-1 opacity-0 group-hover:opacity-100 transition-opacity">
            status probes enabled, but state unknown
          </span>
        </div>
      );
    case true:
      return <CircleCheck className="h-4 w-4 mt-1 fill-green-500" />;
    default:
      return <CircleAlert className="h-4 w-4 mt-1 fill-red-500" />;
  }
}

function getPorts(nodeName: string, ports: number[], expandedPorts: string[]): ReactElement {
  if (expandedPorts.includes(nodeName)) {
    return (
      <ul className="pl-24 list-disc">
        {ports.map((port, index) => (
          <li key={`${index}-${port}`}>{port}</li>
        ))}
      </ul>
    );
  }

  return <></>;
}

function getTopologyNodeCard(
  node: ClabernetesContainerlabDevNodeV1Alpha1,
  expandedTcpPorts: string[],
  setExpandedTcpPorts: (expandedPorts: string[]) => void,
  expandedUdpPorts: string[],
  setExpandedUdpPorts: (expandedPorts: string[]) => void,
): ReactElement {
  const nodeName = (node.metadata?.name as string | undefined) ?? "unknown";
  const nodeExposedPortData = node.status?.exposedPorts;

  const nodeReadiness = node.status?.readiness ?? "unknown";
  // the node spec is a flat containerlab node definition, so kind/image are right there
  const kind = node.spec?.kind ?? "unknown";
  const image = node.spec?.image ?? "unknown";
  const requestedProfileName = node.spec?.launcherProfileRef?.name;
  let profileName = "Config defaults";
  if (requestedProfileName) {
    profileName = `${requestedProfileName} (unresolved)`;
  }

  if (node.status?.appliedLauncherProfile?.name) {
    profileName = node.status.appliedLauncherProfile.name;
  }
  const loadBalancerAddress = nodeExposedPortData?.loadBalancerAddress;
  const allocatedPorts = nodeExposedPortData?.ports ?? [];
  const exposedTcpPorts = allocatedPorts
    .filter((port) => port.protocol === "TCP")
    .map((port) => port.destinationPort);
  const exposedUdpPorts = allocatedPorts
    .filter((port) => port.protocol === "UDP")
    .map((port) => port.destinationPort);

  return (
    <Card key={nodeName}>
      <CardHeader>
        <CardTitle className="flex items-center justify-center">{nodeName}</CardTitle>
      </CardHeader>
      <CardContent className="flex items-center justify-center">
        <div className="flex flex-col text-sm font-normal">
          <div className="flex items-center">
            <span className="w-24 pr-2 text-right font-semibold">Readiness:</span>
            <span>{nodeReadiness}</span>
          </div>
          <div className="flex items-center">
            <span className="w-24 pr-2 text-right font-semibold">Kind:</span>
            <span>{kind}</span>
          </div>
          <div className="flex items-center">
            <span className="w-24 pr-2 text-right font-semibold">Image:</span>
            <span>{`${image}`}</span>
          </div>
          <div className="flex items-center">
            <span className="w-24 pr-2 text-right font-semibold">Profile:</span>
            <span>{profileName}</span>
          </div>
          <div className="flex items-center">
            <span className="w-24 pr-2 text-right font-semibold">LB Address:</span>
            <span>{loadBalancerAddress}</span>
          </div>
          <div className="flex items-center">
            <span className="w-24 pr-2 text-right font-semibold">TCP Ports:</span>
            <Button
              onClick={(): void => {
                const clonedExpandedPorts = [...expandedTcpPorts];

                if (expandedTcpPorts.includes(nodeName)) {
                  setExpandedTcpPorts(
                    clonedExpandedPorts.filter((element) => {
                      return element !== nodeName;
                    }),
                  );
                  return;
                }

                clonedExpandedPorts.push(nodeName);
                setExpandedTcpPorts(clonedExpandedPorts);
              }}
              size="sm"
              variant="ghost"
            >
              {getExpandCollapseIcon(expandedTcpPorts.includes(nodeName))}
            </Button>
          </div>
          {getPorts(nodeName, exposedTcpPorts, expandedTcpPorts)}
          <div className="flex items-center">
            <span className="w-24 pr-2 text-right font-semibold">UDP Ports:</span>
            <Button
              onClick={(): void => {
                const clonedExpandedPorts = [...expandedUdpPorts];

                if (expandedUdpPorts.includes(nodeName)) {
                  setExpandedUdpPorts(
                    clonedExpandedPorts.filter((element) => {
                      return element !== nodeName;
                    }),
                  );
                  return;
                }

                clonedExpandedPorts.push(nodeName);
                setExpandedUdpPorts(clonedExpandedPorts);
              }}
              size="sm"
              variant="ghost"
            >
              {getExpandCollapseIcon(expandedUdpPorts.includes(nodeName))}
            </Button>
          </div>
          {getPorts(nodeName, exposedUdpPorts, expandedUdpPorts)}
        </div>
      </CardContent>
    </Card>
  );
}

interface ExpandProps {
  readonly row: Row<ClabernetesContainerlabDevTopologyV1Alpha1>;
}

export function Expand(props: ExpandProps): ReactElement {
  const { row } = props;

  const obj = row.original;
  const namespace = obj.metadata?.namespace as string;
  const name = obj.metadata?.name as string;

  const [expandedTcpPorts, setExpandedTcpPorts] = useState<string[]>([]);

  const [expandedUdpPorts, setExpandedUdpPorts] = useState<string[]>([]);

  const { data: objNodes } = useQuery({
    enabled: true,
    queryFn: async (): Promise<ClabernetesContainerlabDevNodeV1Alpha1[]> => {
      const response = await listTopologyNodes(namespace, name);

      return JSON.parse(response) as ClabernetesContainerlabDevNodeV1Alpha1[];
    },
    queryKey: ["topology-nodes", { name: name, namespace: namespace }],
    retry: true,
    throwOnError: true,
  });
  const { data: objLinks } = useQuery({
    enabled: true,
    queryFn: async (): Promise<ClabernetesContainerlabDevLinkV1Alpha1[]> => {
      const response = await listTopologyLinks(namespace, name);

      return JSON.parse(response) as ClabernetesContainerlabDevLinkV1Alpha1[];
    },
    queryKey: ["topology-links", { name: name, namespace: namespace }],
    retry: true,
    throwOnError: true,
  });
  const { data: objProfiles } = useQuery({
    enabled: true,
    queryFn: async (): Promise<ClabernetesContainerlabDevLauncherprofileV1Alpha1[]> => {
      const response = await listTopologyLauncherProfiles(namespace, name);

      return JSON.parse(response) as ClabernetesContainerlabDevLauncherprofileV1Alpha1[];
    },
    queryKey: ["topology-launcher-profiles", { name: name, namespace: namespace }],
    retry: true,
    throwOnError: true,
  });

  const sortedNodes = [...(objNodes ?? [])].sort((a, b) =>
    ((a.metadata?.name as string | undefined) ?? "").localeCompare(
      (b.metadata?.name as string | undefined) ?? "",
    ),
  );
  const links = objLinks ?? [];
  const invalidLinkCount = links.filter((link) => (link.status?.error ?? "") !== "").length;
  const profileNames = [...(objProfiles ?? [])]
    .map((profile) => profile.metadata?.name ?? "")
    .filter((profileName) => profileName !== "")
    .sort()
    .join(", ");

  return (
    <div>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-center">
            <div className="flex flex-col text-sm font-normal">
              <div className="flex items-center">
                <span className="w-24 pr-2 text-right font-semibold">Namespace:</span>
                <span>{namespace}</span>
              </div>
              <div className="flex items-center">
                <span className="w-24 pr-2 text-right font-semibold">Name:</span>
                <span>{name}</span>
              </div>
              <div className="flex items-center">
                <span className="w-24 pr-2 text-right font-semibold">Ready:</span>
                <span>
                  {getTopologyReadyIcon(obj.spec?.statusProbes?.enabled, obj.status?.topologyReady)}
                </span>
              </div>
              <div className="flex items-center">
                <span className="w-24 pr-2 text-right font-semibold">Nodes:</span>
                <span>{sortedNodes.length}</span>
              </div>
              <div className="flex items-center">
                <span className="w-24 pr-2 text-right font-semibold">Links:</span>
                <span>
                  {links.length}
                  {invalidLinkCount > 0 ? ` (${invalidLinkCount} invalid)` : ""}
                </span>
              </div>
              <div className="flex items-center">
                <span className="w-24 pr-2 text-right font-semibold">Profiles:</span>
                <span>{profileNames || "none"}</span>
              </div>
            </div>
          </CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-4">
          {sortedNodes.map((node) => {
            return getTopologyNodeCard(
              node,
              expandedTcpPorts,
              setExpandedTcpPorts,
              expandedUdpPorts,
              setExpandedUdpPorts,
            );
          })}
        </CardContent>
      </Card>
    </div>
  );
}
