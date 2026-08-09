import { type ReactElement, useState } from "react";
import {
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet.tsx";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
} from "@/components/ui/form.tsx";
import { Input } from "@/components/ui/input.tsx";
import { NamespaceSelector } from "@/components/namespace-selector.tsx";
import { Button } from "@/components/ui/button.tsx";
import { getQueryClient } from "@/lib/get-query-client.ts";
import type { C9sRunTopologyV1Alpha1 } from "@/lib/clabernetes-client";
import { createTopology } from "@/lib/kubernetes.ts";
import { Textarea } from "@/components/ui/textarea.tsx";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog.tsx";
import { Separator } from "@/components/ui/separator.tsx";
import { FolderMinus, FolderPlus } from "lucide-react";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible.tsx";
import { Card } from "@/components/ui/card.tsx";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu.tsx";
import Link from "next/link";
import { Switch } from "@/components/ui/switch.tsx";
import { PullSecretsSelector } from "@/components/pullsecrets-selector.tsx";
import { SecretSelector } from "@/components/secrets-selector.tsx";

function getCollapsableFolderIcon(isExpanded: boolean): ReactElement {
  if (isExpanded) {
    return <FolderMinus className="h-4 w-4" />;
  }

  return <FolderPlus className="h-4 w-4" />;
}

interface CreateAlertDialogProps {
  readonly title: string;
  readonly description: string;
}

function AddAlert(
  alert: CreateAlertDialogProps,
  alerts: CreateAlertDialogProps[],
  setAlerts: (alerts: CreateAlertDialogProps[]) => void,
): void {
  if (alerts.length === 0) {
    setAlerts([alert]);

    return;
  }

  const clonedAlerts = [...alerts];

  if (alerts.includes(alert)) {
    setAlerts(
      clonedAlerts.filter((element) => {
        return element !== alert;
      }),
    );
    return;
  }

  clonedAlerts.push(alert);
  setAlerts(clonedAlerts);
}

export const configmapModeValues = ["read", "execute"] as const;
export const imagePullThroughOverrideModeValues = ["auto", "always", "never"] as const;

export const formSchema = z.object({
  name: z.string().min(2),
  namespace: z.string(),
  definition: z.string(),
  expose: z.object({
    disable: z.boolean(),
    disableAutoExpose: z.boolean(),
  }),
  deployment: z.object({
    filesFromConfigmap: z.array(
      z.object({
        nodeName: z.string(),
        filePath: z.string(),
        configmap: z.string(),
        configmapPath: z.string(),
        mode: z.enum(configmapModeValues).default(configmapModeValues[0]),
      }),
    ),
    filesFromURL: z.array(
      z.object({
        nodeName: z.string(),
        filePath: z.string(),
        url: z.string(),
      }),
    ),
    resources: z.array(
      z.object({
        nodeName: z.string(),
        limits: z.object({
          cpu: z.string(),
          memory: z.string(),
        }),
        requests: z.object({
          cpu: z.string(),
          memory: z.string(),
        }),
      }),
    ),
  }),
  imagePull: z.object({
    pullSecrets: z.array(z.string()),
    insecureRegistries: z.array(z.string()),
    pullThroughOverride: z
      .enum(imagePullThroughOverrideModeValues)
      .default(imagePullThroughOverrideModeValues[0]),
    dockerDaemonConfig: z.string(),
    dockerConfig: z.string(),
  }),
  naming: z.string(),
});

const formDefaultValues = {
  name: "",
  namespace: "default",
  definition: "",
  expose: {
    disable: false,
    disableAutoExpose: false,
  },
  deployment: {
    filesFromConfigmap: [],
    filesFromURL: [],
    resources: [],
  },
  imagePull: {
    pullSecrets: [],
    insecureRegistries: [],
    pullThroughOverride: imagePullThroughOverrideModeValues[0],
    dockerDaemonConfig: "",
    dockerConfig: "",
  },
  naming: "",
};

export function CreateSheet(): ReactElement {
  const [alerts, setAlerts] = useState<CreateAlertDialogProps[]>([]);
  const [exposeExpanded, setExposeExpanded] = useState<boolean>(false);
  const [deploymentExpanded, setDeploymentExpanded] = useState<boolean>(false);
  const [imagePullExpanded, setImagePullExpanded] = useState<boolean>(false);

  const form = useForm<z.infer<typeof formSchema>>({
    defaultValues: formDefaultValues,
    resolver: zodResolver(formSchema),
  });

  function addFileFromConfigmap(): void {
    form.setValue("deployment.filesFromConfigmap", [
      ...form.getValues("deployment.filesFromConfigmap"),
      {
        nodeName: "",
        filePath: "",
        configmap: "",
        configmapPath: "",
        mode: configmapModeValues[0],
      },
    ]);
  }

  function deleteFileFromConfigmap(index: number): void {
    const filesFromConfigmap = form.getValues("deployment.filesFromConfigmap");
    filesFromConfigmap.splice(index, 1);
    form.setValue("deployment.filesFromConfigmap", filesFromConfigmap);
  }

  function addFileFromUrl(): void {
    form.setValue("deployment.filesFromURL", [
      ...form.getValues("deployment.filesFromURL"),
      {
        nodeName: "",
        filePath: "",
        url: "",
      },
    ]);
  }

  function deleteFileFromUrl(index: number): void {
    const filesFromURL = form.getValues("deployment.filesFromURL");
    filesFromURL.splice(index, 1);
    form.setValue("deployment.filesFromURL", filesFromURL);
  }

  function addResources(): void {
    form.setValue("deployment.resources", [
      ...form.getValues("deployment.resources"),
      {
        nodeName: "",
        limits: {
          cpu: "",
          memory: "",
        },
        requests: {
          cpu: "",
          memory: "",
        },
      },
    ]);
  }

  function deleteResources(index: number): void {
    const resources = form.getValues("deployment.resources");
    resources.splice(index, 1);
    form.setValue("deployment.resources", resources);
  }

  function addInsecureRegistries(): void {
    form.setValue("imagePull.insecureRegistries", [
      ...form.getValues("imagePull.insecureRegistries"),
      "",
    ]);
  }

  function deleteInsecureRegistries(index: number): void {
    const registries = form.getValues("imagePull.insecureRegistries");
    registries.splice(index, 1);
    form.setValue("imagePull.insecureRegistries", registries);
  }

  async function onSubmitWrapper(values: z.infer<typeof formSchema>): Promise<void> {
    const resources: Record<string, object> = {};

    for (const nodeResources of values.deployment.resources) {
      resources[nodeResources.nodeName] = {
        limits: {
          cpu: nodeResources.limits.cpu,
          memory: nodeResources.limits.memory,
        },
        requests: {
          cpu: nodeResources.requests.cpu,
          memory: nodeResources.requests.memory,
        },
      };
    }

    const filesFromConfigmap: Record<string, object[]> = {};

    for (const fileFromConfigmap of values.deployment.filesFromConfigmap) {
      filesFromConfigmap[fileFromConfigmap.nodeName] =
        filesFromConfigmap[fileFromConfigmap.nodeName] || [];

      filesFromConfigmap[fileFromConfigmap.nodeName].push({
        configMapName: fileFromConfigmap.configmap,
        configMapPath: fileFromConfigmap.configmapPath,
        filePath: fileFromConfigmap.filePath,
        mode: fileFromConfigmap.mode,
      });
    }

    const filesFromURL: Record<string, object[]> = {};

    for (const fileFromUrl of values.deployment.filesFromURL) {
      filesFromURL[fileFromUrl.nodeName] = filesFromURL[fileFromUrl.nodeName] || [];

      filesFromURL[fileFromUrl.nodeName].push({
        filePath: fileFromUrl.filePath,
        url: fileFromUrl.url,
      });
    }

    const obj = {
      apiVersion: "c9s.run/v1alpha1",
      kind: "Topology",
      metadata: {
        name: values.name,
        namespace: values.namespace,
      },
      spec: {
        definition: {
          containerlab: values.definition,
        },
        expose: {
          disableExpose: values.expose.disable,
          disableAutoExpose: values.expose.disableAutoExpose,
        },
        deployment: {
          resources: resources,
          filesFromConfigMap: filesFromConfigmap,
          // biome-ignore lint/style/useNamingConvention: comes from k8s, its ok biome dont worry
          filesFromURL: filesFromURL,
        },
        imagePull: {
          pullSecrets: values.imagePull.pullSecrets,
          insecureRegistries: values.imagePull.insecureRegistries,
          dockerConfig: values.imagePull.dockerConfig,
          dockerDaemonConfig: values.imagePull.dockerDaemonConfig,
          pullThroughOverride: values.imagePull.pullThroughOverride,
        },
        naming: "global",
      },
    } as C9sRunTopologyV1Alpha1;

    const response = await createTopology(values.namespace, JSON.stringify(obj));

    const parsedResponse = JSON.parse(response);

    if (parsedResponse.error) {
      AddAlert(
        {
          title: "Create Topology Failed! :(",
          description: `Error: ${parsedResponse.error.message}`,
        },
        alerts,
        setAlerts,
      );

      return;
    }

    form.reset();

    getQueryClient()
      .invalidateQueries({
        queryKey: ["topologies", {}],
      })
      .catch((invalidateError: unknown) => {
        throw invalidateError;
      });
  }

  return (
    <>
      {alerts.map((alert): ReactElement => {
        return (
          <AlertDialog
            key="alert"
            onOpenChange={(): void => {
              const clonedAlerts = [...alerts];

              if (alerts.includes(alert)) {
                setAlerts(
                  clonedAlerts.filter((element): boolean => {
                    return element !== alert;
                  }),
                );
                return;
              }

              clonedAlerts.push(alert);
              setAlerts(clonedAlerts);
            }}
            open={alerts.length > 0}
          >
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{alert.title}</AlertDialogTitle>
                <AlertDialogDescription>{alert.description}</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Ok</AlertDialogCancel>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        );
      })}
      <SheetContent
        className="max-h-screen overflow-y-auto"
        style={{ maxWidth: "50vw" }}
        onSubmit={form.handleSubmit(onSubmitWrapper)}
      >
        <Form {...form}>
          <form className="space-y-8">
            <SheetHeader>
              <SheetTitle>Create a Topology</SheetTitle>
              <SheetDescription>
                A Topology holds a valid containerlab topology and runs it in Kubernetes
              </SheetDescription>
            </SheetHeader>
            <div className="grid gap-4 py-4">
              <FormField
                control={form.control}
                name="name"
                render={({ field: { onChange, value } }): ReactElement => {
                  return (
                    <FormItem>
                      <div className="grid grid-cols-4 items-center gap-4">
                        <FormLabel
                          className="text-right"
                          htmlFor="name"
                        >
                          Name
                        </FormLabel>
                        <FormControl>
                          <Input
                            className="col-span-3"
                            onChange={onChange}
                            value={value}
                          />
                        </FormControl>
                      </div>
                      <FormDescription>The name of the clabernetes Topology</FormDescription>
                    </FormItem>
                  );
                }}
              />
              <FormField
                control={form.control}
                name="namespace"
                render={({ field: { onChange, value } }): ReactElement => {
                  return (
                    <FormItem>
                      <div className="grid grid-cols-4 items-center gap-4">
                        <FormLabel
                          className="text-right"
                          htmlFor="namespace"
                        >
                          Namespace
                        </FormLabel>
                        <FormControl>
                          <NamespaceSelector
                            namespace={value}
                            placeholder=""
                            setNamespace={onChange}
                          />
                        </FormControl>
                      </div>
                      <FormDescription>The namespace to create the Topology in</FormDescription>
                    </FormItem>
                  );
                }}
              />
              <FormField
                control={form.control}
                name="definition"
                render={({ field: { onChange, value } }): ReactElement => {
                  return (
                    <FormItem>
                      <div className="grid grid-cols-4 items-center gap-4">
                        <FormLabel
                          className="text-right"
                          htmlFor="definition"
                        >
                          Definition
                        </FormLabel>
                        <FormControl>
                          <Textarea
                            className="col-span-3"
                            onChange={onChange}
                            value={value}
                          />
                        </FormControl>
                      </div>
                      <FormDescription>
                        The containerlab topology to run in Kubernetes
                      </FormDescription>
                    </FormItem>
                  );
                }}
              />
              <Separator />
              <Collapsible
                onOpenChange={(): void => setExposeExpanded(!exposeExpanded)}
                open={exposeExpanded}
              >
                <div>
                  <div className="grid grid-cols-4 items-center gap-4">
                    <SheetTitle className="text-md col-span-3">Expose</SheetTitle>
                    <CollapsibleTrigger asChild={true}>
                      <Button
                        size="sm"
                        variant="ghost"
                      >
                        {getCollapsableFolderIcon(exposeExpanded)}
                      </Button>
                    </CollapsibleTrigger>
                  </div>
                  <SheetDescription>
                    Control how Clabernetes exposes nodes in your Topology
                  </SheetDescription>
                </div>
                <CollapsibleContent>
                  <FormField
                    control={form.control}
                    name="expose.disable"
                    render={({ field: { onChange, value } }): ReactElement => {
                      return (
                        <FormItem>
                          <div className="grid grid-cols-4 items-center gap-4 pt-4">
                            <FormLabel
                              className="text-right"
                              htmlFor="expose.disable"
                            >
                              Disable Expose
                            </FormLabel>
                            <FormControl>
                              <Switch
                                checked={value}
                                onCheckedChange={onChange}
                              />
                            </FormControl>
                          </div>
                          <FormDescription>
                            Fully disable exposing nodes via LoadBalancer service
                          </FormDescription>
                        </FormItem>
                      );
                    }}
                  />
                  <FormField
                    control={form.control}
                    name="expose.disableAutoExpose"
                    render={({ field: { onChange, value } }): ReactElement => {
                      return (
                        <FormItem>
                          <div className="grid grid-cols-4 items-center gap-4 pt-4">
                            <FormLabel
                              className="text-right"
                              htmlFor="expose.disableAutoExpose"
                            >
                              Disable Auto Expose
                            </FormLabel>
                            <FormControl>
                              <Switch
                                checked={value}
                                onCheckedChange={onChange}
                              />
                            </FormControl>
                          </div>
                          <FormDescription>
                            Disable automatically exposing default ports
                          </FormDescription>
                        </FormItem>
                      );
                    }}
                  />
                </CollapsibleContent>
              </Collapsible>
              <Separator />
              <Collapsible
                onOpenChange={(): void => setDeploymentExpanded(!deploymentExpanded)}
                open={deploymentExpanded}
              >
                <div>
                  <div className="grid grid-cols-4 items-center gap-4">
                    <SheetTitle className="text-md col-span-3">Deployment</SheetTitle>
                    <CollapsibleTrigger asChild={true}>
                      <Button
                        size="sm"
                        variant="ghost"
                      >
                        {getCollapsableFolderIcon(deploymentExpanded)}
                      </Button>
                    </CollapsibleTrigger>
                  </div>
                  <SheetDescription>
                    Control configuration of the Deployments Clabernetes creates to represent each
                    of the topology's nodes
                  </SheetDescription>
                </div>
                <CollapsibleContent>
                  <>
                    <div className="grid grid-cols-4 items-center gap-4 pt-4">
                      <FormLabel
                        className="text-right"
                        htmlFor="deployment.filesFromConfigmap"
                      >
                        Files From Configmaps
                      </FormLabel>
                      <Button
                        className="col-span-3"
                        onClick={addFileFromConfigmap}
                        type="button"
                        variant="outline"
                      >
                        Add File from Configmap
                      </Button>
                    </div>
                    <FormDescription>
                      Files that should be mounted to nodes in the Topology from Configmap(s) in the
                      namespace
                    </FormDescription>
                    <FormField
                      control={form.control}
                      name="deployment.filesFromConfigmap"
                      render={(): ReactElement => {
                        return (
                          <div className="p-4">
                            {form
                              .getValues("deployment.filesFromConfigmap")
                              .map((fileFromConfigmap, index): ReactElement => {
                                return (
                                  <Card
                                    className="grid gap-4 p-4"
                                    key={`${fileFromConfigmap.nodeName}-${fileFromConfigmap.filePath}`}
                                  >
                                    <FormField
                                      control={form.control}
                                      name={`deployment.filesFromConfigmap.${index}.nodeName`}
                                      render={({ field: { value, onChange } }): ReactElement => {
                                        return (
                                          <FormItem>
                                            <div className="grid grid-cols-4 items-center gap-4">
                                              <FormLabel
                                                className="text-right"
                                                htmlFor={`deployment.filesFromConfigmap.${index}.nodeName`}
                                              >
                                                Node Name
                                              </FormLabel>
                                              <FormControl>
                                                <Input
                                                  className="col-span-3"
                                                  onChange={onChange}
                                                  value={value}
                                                />
                                              </FormControl>
                                            </div>
                                            <FormDescription>
                                              The name of the node to mount the file to
                                            </FormDescription>
                                          </FormItem>
                                        );
                                      }}
                                    />
                                    <FormField
                                      control={form.control}
                                      name={`deployment.filesFromConfigmap.${index}.filePath`}
                                      render={({ field: { value, onChange } }): ReactElement => {
                                        return (
                                          <FormItem>
                                            <div className="grid grid-cols-4 items-center gap-4">
                                              <FormLabel
                                                className="text-right"
                                                htmlFor={`deployment.filesFromConfigmap.${index}.filePath`}
                                              >
                                                File Path
                                              </FormLabel>
                                              <FormControl>
                                                <Input
                                                  className="col-span-3"
                                                  onChange={onChange}
                                                  value={value}
                                                />
                                              </FormControl>
                                            </div>
                                            <FormDescription>
                                              The path to mount the file
                                            </FormDescription>
                                          </FormItem>
                                        );
                                      }}
                                    />
                                    <FormField
                                      control={form.control}
                                      name={`deployment.filesFromConfigmap.${index}.configmap`}
                                      render={({ field: { value, onChange } }): ReactElement => {
                                        return (
                                          <FormItem>
                                            <div className="grid grid-cols-4 items-center gap-4">
                                              <FormLabel
                                                className="text-right"
                                                htmlFor={`deployment.filesFromConfigmap.${index}.configmap`}
                                              >
                                                Configmap
                                              </FormLabel>
                                              <FormControl>
                                                <Input
                                                  className="col-span-3"
                                                  onChange={onChange}
                                                  value={value}
                                                />
                                              </FormControl>
                                            </div>
                                            <FormDescription>
                                              The name of the configmap to mount
                                            </FormDescription>
                                          </FormItem>
                                        );
                                      }}
                                    />
                                    <FormField
                                      control={form.control}
                                      name={`deployment.filesFromConfigmap.${index}.configmapPath`}
                                      render={({ field: { value, onChange } }): ReactElement => {
                                        return (
                                          <FormItem>
                                            <div className="grid grid-cols-4 items-center gap-4">
                                              <FormLabel
                                                className="text-right"
                                                htmlFor={`deployment.filesFromConfigmap.${index}.configmapPath`}
                                              >
                                                Configmap Path
                                              </FormLabel>
                                              <FormControl>
                                                <Input
                                                  className="col-span-3"
                                                  onChange={onChange}
                                                  value={value}
                                                />
                                              </FormControl>
                                            </div>
                                            <FormDescription>
                                              The path/key in the configmap to mount, if not
                                              specified the configmap will be mounted without a
                                              sub-path
                                            </FormDescription>
                                          </FormItem>
                                        );
                                      }}
                                    />
                                    <FormField
                                      control={form.control}
                                      name={`deployment.filesFromConfigmap.${index}.mode`}
                                      render={({ field: { value, onChange } }): ReactElement => {
                                        return (
                                          <FormItem>
                                            <div className="grid grid-cols-4 items-center gap-4">
                                              <FormLabel
                                                className="text-right"
                                                htmlFor={`deployment.filesFromConfigmap.${index}.mode`}
                                              >
                                                Mode
                                              </FormLabel>
                                              <FormControl>
                                                <DropdownMenu>
                                                  <DropdownMenuTrigger
                                                    asChild={true}
                                                    className="col-span-3"
                                                  >
                                                    <div className="flex justify-end">
                                                      <Button
                                                        className="flex w-full justify-between"
                                                        variant="outline"
                                                      >
                                                        {value}
                                                      </Button>
                                                    </div>
                                                  </DropdownMenuTrigger>
                                                  <DropdownMenuContent style={{ minWidth: "30vw" }}>
                                                    <DropdownMenuRadioGroup
                                                      onValueChange={onChange}
                                                      value={value}
                                                    >
                                                      <DropdownMenuRadioItem value="read">
                                                        read
                                                      </DropdownMenuRadioItem>
                                                      <DropdownMenuRadioItem value="execute">
                                                        execute
                                                      </DropdownMenuRadioItem>
                                                    </DropdownMenuRadioGroup>
                                                  </DropdownMenuContent>
                                                </DropdownMenu>
                                              </FormControl>
                                            </div>
                                            <FormDescription>
                                              The mode to mount the file with -- either read or with
                                              execute bits set
                                            </FormDescription>
                                          </FormItem>
                                        );
                                      }}
                                    />
                                    <div className="grid grid-cols-3 items-center gap-4">
                                      <div />
                                      <Button
                                        onClick={(): void => {
                                          deleteFileFromConfigmap(index);
                                        }}
                                        type="button"
                                        variant="destructive"
                                      >
                                        Delete
                                      </Button>
                                      <div />
                                    </div>
                                  </Card>
                                );
                              })}
                          </div>
                        );
                      }}
                    />
                  </>
                  <>
                    <div className="grid grid-cols-4 items-center gap-4 pt-4">
                      <FormLabel
                        className="text-right"
                        htmlFor="deployment.filesFromURL"
                      >
                        Files From Url
                      </FormLabel>
                      <Button
                        className="col-span-3"
                        onClick={addFileFromUrl}
                        type="button"
                        variant="outline"
                      >
                        Add File from Url
                      </Button>
                    </div>
                    <FormDescription>
                      Files that should be downloaded from a Url and mounted to nodes in the
                      Topology
                    </FormDescription>
                    <FormField
                      control={form.control}
                      name="deployment.filesFromURL"
                      render={(): ReactElement => {
                        return (
                          <div className="p-4">
                            {form
                              .getValues("deployment.filesFromURL")
                              .map((fileFromConfigmap, index): ReactElement => {
                                return (
                                  <Card
                                    className="grid gap-4 p-4"
                                    key={`${fileFromConfigmap.nodeName}-${fileFromConfigmap.filePath}`}
                                  >
                                    <FormField
                                      control={form.control}
                                      name={`deployment.filesFromURL.${index}.nodeName`}
                                      render={({ field: { value, onChange } }): ReactElement => {
                                        return (
                                          <FormItem>
                                            <div className="grid grid-cols-4 items-center gap-4">
                                              <FormLabel
                                                className="text-right"
                                                htmlFor={`deployment.filesFromURL.${index}.nodeName`}
                                              >
                                                Node Name
                                              </FormLabel>
                                              <FormControl>
                                                <Input
                                                  className="col-span-3"
                                                  onChange={onChange}
                                                  value={value}
                                                />
                                              </FormControl>
                                            </div>
                                            <FormDescription>
                                              The name of the node to download the file to
                                            </FormDescription>
                                          </FormItem>
                                        );
                                      }}
                                    />
                                    <FormField
                                      control={form.control}
                                      name={`deployment.filesFromURL.${index}.url`}
                                      render={({ field: { value, onChange } }): ReactElement => {
                                        return (
                                          <FormItem>
                                            <div className="grid grid-cols-4 items-center gap-4">
                                              <FormLabel
                                                className="text-right"
                                                htmlFor={`deployment.filesFromURL.${index}.url`}
                                              >
                                                File Path
                                              </FormLabel>
                                              <FormControl>
                                                <Input
                                                  className="col-span-3"
                                                  onChange={onChange}
                                                  value={value}
                                                />
                                              </FormControl>
                                            </div>
                                            <FormDescription>
                                              The Url at which to download the file
                                            </FormDescription>
                                          </FormItem>
                                        );
                                      }}
                                    />
                                    <div className="grid grid-cols-3 items-center gap-4">
                                      <div />
                                      <Button
                                        onClick={(): void => {
                                          deleteFileFromUrl(index);
                                        }}
                                        type="button"
                                        variant="destructive"
                                      >
                                        Delete
                                      </Button>
                                      <div />
                                    </div>
                                  </Card>
                                );
                              })}
                          </div>
                        );
                      }}
                    />
                  </>
                  <>
                    <div className="grid grid-cols-4 items-center gap-4 pt-4">
                      <FormLabel
                        className="text-right"
                        htmlFor="deployment.resources"
                      >
                        Resources
                      </FormLabel>
                      <Button
                        className="col-span-3"
                        onClick={addResources}
                        type="button"
                        variant="outline"
                      >
                        Add Resources
                      </Button>
                    </div>
                    <FormDescription>
                      Resource settings for each node in the Topology
                    </FormDescription>
                    <FormField
                      control={form.control}
                      name="deployment.resources"
                      render={(): ReactElement => {
                        return (
                          <div className="p-4">
                            {form.getValues("deployment.resources").map((resources, index) => {
                              return (
                                <Card
                                  className="grid gap-4 p-4"
                                  key={`${resources.nodeName}`}
                                >
                                  <FormField
                                    control={form.control}
                                    name={`deployment.resources.${index}.nodeName`}
                                    render={({ field: { value, onChange } }): ReactElement => {
                                      return (
                                        <FormItem>
                                          <div className="grid grid-cols-4 items-center gap-4">
                                            <FormLabel
                                              className="text-right"
                                              htmlFor={`deployment.resources.${index}.nodeName`}
                                            >
                                              Node Name
                                            </FormLabel>
                                            <FormControl>
                                              <Input
                                                className="col-span-3"
                                                onChange={onChange}
                                                value={value}
                                              />
                                            </FormControl>
                                          </div>
                                          <FormDescription>
                                            The name of the node to configure resources for
                                          </FormDescription>
                                        </FormItem>
                                      );
                                    }}
                                  />
                                  <FormField
                                    control={form.control}
                                    name={`deployment.resources.${index}.requests.cpu`}
                                    render={({ field: { value, onChange } }): ReactElement => {
                                      return (
                                        <FormItem>
                                          <div className="grid grid-cols-4 items-center gap-4">
                                            <FormLabel
                                              className="text-right"
                                              htmlFor={`deployment.resources.${index}.requests.cpu`}
                                            >
                                              CPU Requests
                                            </FormLabel>
                                            <FormControl>
                                              <Input
                                                className="col-span-3"
                                                onChange={onChange}
                                                value={value}
                                              />
                                            </FormControl>
                                          </div>
                                          <FormDescription>
                                            The value to set as the CPU request, must conform to{" "}
                                            <Link
                                              className="underline"
                                              href="https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#meaning-of-cpu"
                                            >
                                              standard kubernetes CPU resource units
                                            </Link>
                                          </FormDescription>
                                        </FormItem>
                                      );
                                    }}
                                  />
                                  <FormField
                                    control={form.control}
                                    name={`deployment.resources.${index}.requests.memory`}
                                    render={({ field: { value, onChange } }): ReactElement => {
                                      return (
                                        <FormItem>
                                          <div className="grid grid-cols-4 items-center gap-4">
                                            <FormLabel
                                              className="text-right"
                                              htmlFor={`deployment.resources.${index}.requests.memory`}
                                            >
                                              Memory Requests
                                            </FormLabel>
                                            <FormControl>
                                              <Input
                                                className="col-span-3"
                                                onChange={onChange}
                                                value={value}
                                              />
                                            </FormControl>
                                          </div>
                                          <FormDescription>
                                            The value to set as the memory request, must conform to{" "}
                                            <Link
                                              className="underline"
                                              href="https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#meaning-of-memory"
                                            >
                                              standard kubernetes memory resource units
                                            </Link>
                                          </FormDescription>
                                        </FormItem>
                                      );
                                    }}
                                  />
                                  <FormField
                                    control={form.control}
                                    name={`deployment.resources.${index}.limits.cpu`}
                                    render={({ field: { value, onChange } }): ReactElement => {
                                      return (
                                        <FormItem>
                                          <div className="grid grid-cols-4 items-center gap-4">
                                            <FormLabel
                                              className="text-right"
                                              htmlFor={`deployment.resources.${index}.limits.cpu`}
                                            >
                                              CPU Limits
                                            </FormLabel>
                                            <FormControl>
                                              <Input
                                                className="col-span-3"
                                                onChange={onChange}
                                                value={value}
                                              />
                                            </FormControl>
                                          </div>
                                          <FormDescription>
                                            The value to set as the CPU limit, must conform to{" "}
                                            <Link
                                              className="underline"
                                              href="https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#meaning-of-cpu"
                                            >
                                              standard kubernetes CPU resource units
                                            </Link>
                                          </FormDescription>
                                        </FormItem>
                                      );
                                    }}
                                  />
                                  <FormField
                                    control={form.control}
                                    name={`deployment.resources.${index}.limits.memory`}
                                    render={({ field: { value, onChange } }): ReactElement => {
                                      return (
                                        <FormItem>
                                          <div className="grid grid-cols-4 items-center gap-4">
                                            <FormLabel
                                              className="text-right"
                                              htmlFor={`deployment.resources.${index}.limits.memory`}
                                            >
                                              Memory Limits
                                            </FormLabel>
                                            <FormControl>
                                              <Input
                                                className="col-span-3"
                                                onChange={onChange}
                                                value={value}
                                              />
                                            </FormControl>
                                          </div>
                                          <FormDescription>
                                            The value to set as the memory limit, must conform to{" "}
                                            <Link
                                              className="underline"
                                              href="https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#meaning-of-memory"
                                            >
                                              standard kubernetes memory resource units
                                            </Link>
                                          </FormDescription>
                                        </FormItem>
                                      );
                                    }}
                                  />
                                  <div className="grid grid-cols-3 items-center gap-4">
                                    <div />
                                    <Button
                                      onClick={(): void => {
                                        deleteResources(index);
                                      }}
                                      type="button"
                                      variant="destructive"
                                    >
                                      Delete
                                    </Button>
                                    <div />
                                  </div>
                                </Card>
                              );
                            })}
                          </div>
                        );
                      }}
                    />
                  </>
                </CollapsibleContent>
              </Collapsible>
              <Separator />
              <Collapsible
                onOpenChange={(): void => setImagePullExpanded(!imagePullExpanded)}
                open={imagePullExpanded}
              >
                <div>
                  <div className="grid grid-cols-4 items-center gap-4">
                    <SheetTitle className="text-md col-span-3">Image Pull</SheetTitle>
                    <CollapsibleTrigger asChild={true}>
                      <Button
                        size="sm"
                        variant="ghost"
                      >
                        {getCollapsableFolderIcon(imagePullExpanded)}
                      </Button>
                    </CollapsibleTrigger>
                  </div>
                  <SheetDescription>
                    Control how Clabernetes launchers pull images into their local Docker daemon
                  </SheetDescription>
                </div>
                <CollapsibleContent>
                  <FormField
                    control={form.control}
                    name="imagePull.pullSecrets"
                    render={({ field: { value, onChange } }): ReactElement => {
                      return (
                        <FormItem>
                          <div className="grid grid-cols-4 items-center gap-4 pt-4">
                            <FormLabel
                              className="text-right"
                              htmlFor="imagePull.pullSecrets"
                            >
                              Pull Secrets
                            </FormLabel>
                            <FormControl>
                              <PullSecretsSelector
                                namespace={form.getValues("namespace")}
                                pullSecrets={value}
                                placeholder="select pull secret(s)"
                                setPullSecrets={onChange}
                              />
                            </FormControl>
                          </div>
                          <div className="relative group">
                            <FormDescription className="overflow-hidden text-ellipsis">
                              Set pull secrets to use when pulling images -- hover for more details
                            </FormDescription>
                            <span className="pointer-events-none absolute left-1/2 -translate-x-1/2 bottom-full mb-2 w-max max-w-xs bg-gray-800 text-white text-sm rounded px-2 py-1 opacity-0 group-hover:opacity-100 transition-opacity whitespace-normal">
                              Pull Secrets allows for providing secret(s) to use when pulling the
                              image. This is only applicable *if* ImagePullThrough mode is auto or
                              always. The secret is used by the launcher pod to pull the image via
                              the cluster CRI. The secret is *not* mounted to the pod, but instead
                              is used in conjunction with a job that spawns a pod using the
                              specified secret. The job will kill the pod as soon as the image has
                              been pulled -- we do this because we don't care if the pod runs, we
                              only care that the image gets pulled on a specific node. Note that
                              just like "normal" pull secrets, the secret needs to be in the
                              namespace that the topology is in.
                            </span>
                          </div>
                        </FormItem>
                      );
                    }}
                  />
                  <>
                    <div className="grid grid-cols-4 items-center gap-4 pt-4">
                      <FormLabel
                        className="text-right"
                        htmlFor="imagePull.insecureRegistries"
                      >
                        Insecure Registries
                      </FormLabel>
                      <Button
                        className="col-span-3"
                        onClick={addInsecureRegistries}
                        type="button"
                        variant="outline"
                      >
                        Add Insecure Registry
                      </Button>
                    </div>
                    <FormDescription>
                      A list of registries to configure as insecure in the launcher docker daemon
                    </FormDescription>
                    <FormField
                      control={form.control}
                      name="imagePull.insecureRegistries"
                      render={(): ReactElement => {
                        return (
                          <div className="p-4">
                            {form
                              .getValues("imagePull.insecureRegistries")
                              .map((registry, index) => {
                                return (
                                  <Card
                                    className="grid gap-4 p-4"
                                    key={registry}
                                  >
                                    <FormField
                                      control={form.control}
                                      name={`imagePull.insecureRegistries.${index}`}
                                      render={({ field: { value, onChange } }): ReactElement => {
                                        return (
                                          <FormItem>
                                            <div className="grid grid-cols-4 items-center gap-4">
                                              <FormLabel
                                                className="text-right"
                                                htmlFor={`imagePull.insecureRegistries.${index}`}
                                              >
                                                Node Name
                                              </FormLabel>
                                              <FormControl>
                                                <Input
                                                  className="col-span-3"
                                                  onChange={onChange}
                                                  value={value}
                                                />
                                              </FormControl>
                                            </div>
                                            <FormDescription>
                                              The registry to configure as insecure
                                            </FormDescription>
                                          </FormItem>
                                        );
                                      }}
                                    />
                                    <div className="grid grid-cols-3 items-center gap-4">
                                      <div />
                                      <Button
                                        onClick={(): void => {
                                          deleteInsecureRegistries(index);
                                        }}
                                        type="button"
                                        variant="destructive"
                                      >
                                        Delete
                                      </Button>
                                      <div />
                                    </div>
                                  </Card>
                                );
                              })}
                          </div>
                        );
                      }}
                    />
                  </>
                  <FormField
                    control={form.control}
                    name="imagePull.pullThroughOverride"
                    render={({ field: { value, onChange } }): ReactElement => {
                      return (
                        <FormItem>
                          <div className="grid grid-cols-4 items-center gap-4">
                            <FormLabel
                              className="text-right"
                              htmlFor="imagePull.pullThroughOverride"
                            >
                              Pull Through Mode
                            </FormLabel>
                            <FormControl>
                              <DropdownMenu>
                                <DropdownMenuTrigger
                                  asChild={true}
                                  className="col-span-3"
                                >
                                  <div className="flex justify-end">
                                    <Button
                                      className="flex w-full justify-between"
                                      variant="outline"
                                    >
                                      {value}
                                    </Button>
                                  </div>
                                </DropdownMenuTrigger>
                                <DropdownMenuContent style={{ minWidth: "30vw" }}>
                                  <DropdownMenuRadioGroup
                                    onValueChange={onChange}
                                    value={value}
                                  >
                                    <DropdownMenuRadioItem value="auto">auto</DropdownMenuRadioItem>
                                    <DropdownMenuRadioItem value="always">
                                      always
                                    </DropdownMenuRadioItem>
                                    <DropdownMenuRadioItem value="never">
                                      never
                                    </DropdownMenuRadioItem>
                                  </DropdownMenuRadioGroup>
                                </DropdownMenuContent>
                              </DropdownMenu>
                            </FormControl>
                          </div>
                          <FormDescription>
                            Set the image pull through mode for this Topology
                          </FormDescription>
                        </FormItem>
                      );
                    }}
                  />
                  <FormField
                    control={form.control}
                    name="imagePull.dockerDaemonConfig"
                    render={({ field: { value, onChange } }): ReactElement => {
                      return (
                        <FormItem>
                          <div className="grid grid-cols-4 items-center gap-4 pt-4">
                            <FormLabel
                              className="text-right"
                              htmlFor="imagePull.dockerDaemonConfig"
                            >
                              Docker Daemon Config Secret
                            </FormLabel>
                            <FormControl>
                              <SecretSelector
                                namespace={form.getValues("namespace")}
                                secret={value}
                                placeholder="select secret for docker daemon config"
                                setSecret={onChange}
                              />
                            </FormControl>
                          </div>
                          <div className="relative group">
                            <FormDescription className="overflow-hidden text-ellipsis">
                              DockerDaemonConfig allows for setting the docker daemon config for all
                              launchers in this topology, hover for more details
                            </FormDescription>
                            <span className="pointer-events-none absolute left-1/2 -translate-x-1/2 bottom-full mb-2 w-max max-w-xs bg-gray-800 text-white text-sm rounded px-2 py-1 opacity-0 group-hover:opacity-100 transition-opacity whitespace-normal">
                              DockerDaemonConfig allows for setting the docker daemon config for all
                              launchers in this topology. The secret *must be present in the
                              namespace of this topology*. The secret *must* contain a key
                              "daemon.json" -- as this secret will be mounted to /etc/docker and
                              docker will be expecting the config at /etc/docker/daemon.json.
                            </span>
                          </div>
                        </FormItem>
                      );
                    }}
                  />
                  <FormField
                    control={form.control}
                    name="imagePull.dockerConfig"
                    render={({ field: { value, onChange } }): ReactElement => {
                      return (
                        <FormItem>
                          <div className="grid grid-cols-4 items-center gap-4 pt-4">
                            <FormLabel
                              className="text-right"
                              htmlFor="imagePull.dockerConfig"
                            >
                              Docker (User)) Config Secret
                            </FormLabel>
                            <FormControl>
                              <SecretSelector
                                namespace={form.getValues("namespace")}
                                secret={value}
                                placeholder="select secret for docker (user)) config"
                                setSecret={onChange}
                              />
                            </FormControl>
                          </div>
                          <div className="relative group">
                            <FormDescription className="overflow-hidden text-ellipsis">
                              DockerConfig allows for setting the docker user (for root) config for
                              all launchers in this topology, hover for more details
                            </FormDescription>
                            <span className="pointer-events-none absolute left-1/2 -translate-x-1/2 bottom-full mb-2 w-max max-w-xs bg-gray-800 text-white text-sm rounded px-2 py-1 opacity-0 group-hover:opacity-100 transition-opacity whitespace-normal">
                              DockerConfig allows for setting the docker user (for root) config for
                              all launchers in this topology. The secret *must be present in the
                              namespace of this topology*. The secret *must* contain a key
                              "config.json" -- as this secret will be mounted to
                              /root/.docker/config.json and as such wil be utilized when doing
                              docker-y things -- this means you can put auth things in here in the
                              event your cluster doesn't support the preferred image pull through
                              option.
                            </span>
                          </div>
                        </FormItem>
                      );
                    }}
                  />
                </CollapsibleContent>
              </Collapsible>
            </div>
            <SheetFooter>
              <Button
                onClick={(): void => {
                  form.reset();
                }}
                type="button"
                variant="destructive"
              >
                Reset
              </Button>
              <SheetClose asChild={true}>
                <Button type="submit">Submit</Button>
              </SheetClose>
            </SheetFooter>
          </form>
        </Form>
      </SheetContent>
    </>
  );
}
