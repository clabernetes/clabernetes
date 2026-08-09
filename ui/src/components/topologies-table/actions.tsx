import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import Editor from "@monaco-editor/react";
import type { Row } from "@tanstack/react-table";
import { MoreHorizontal } from "lucide-react";
import { type ReactElement, useRef, useState } from "react";
import type { editor } from "monaco-editor";
import type { C9sRunTopologyV1Alpha1 } from "@/lib/clabernetes-client";
import { parse as parseYaml, stringify as stringifyYaml } from "yaml";
import { useTheme } from "next-themes";
import { getQueryClient } from "@/lib/get-query-client.ts";
import { updateTopology } from "@/lib/kubernetes.ts";

function getEditorTheme(resolvedTheme: string | undefined): string {
  if (typeof resolvedTheme === "undefined") {
    return "";
  }

  if (resolvedTheme === "light") {
    return "";
  }

  return "vs-dark";
}

interface ActionsProps {
  readonly row: Row<C9sRunTopologyV1Alpha1>;
  readonly setCurrentRow: (value: Row<C9sRunTopologyV1Alpha1>) => void;
  readonly setIsDeleteDialogOpen: (value: boolean) => void;
}

export function Actions(props: ActionsProps): ReactElement {
  const theme = useTheme();

  const { row, setCurrentRow, setIsDeleteDialogOpen } = props;

  const [isEditDialogOpen, setIsEditDialogOpen] = useState(false);

  const editorRef = useRef<null | editor.IStandaloneCodeEditor>(null);

  function handleEditorDidMount(mountedEditor: editor.IStandaloneCodeEditor): void {
    editorRef.current = mountedEditor;
  }

  const topologyNamespace = row.original.metadata?.namespace as string;
  const topologyName = row.original.metadata?.name as string;

  return (
    <Dialog>
      <DropdownMenu>
        <DropdownMenuTrigger asChild={true}>
          <Button
            className="h-8 w-8 p-0"
            variant="ghost"
          >
            <span className="sr-only">Open menu</span>
            <MoreHorizontal className="h-4 w-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuLabel>Actions</DropdownMenuLabel>
          <DialogTrigger asChild={true}>
            <DropdownMenuItem
              onClick={(): void => {
                setCurrentRow(row);
              }}
            >
              View
            </DropdownMenuItem>
          </DialogTrigger>
          <DialogTrigger asChild={true}>
            <DropdownMenuItem
              onClick={(): void => {
                setCurrentRow(row);
                setIsEditDialogOpen(true);
              }}
            >
              Edit
            </DropdownMenuItem>
          </DialogTrigger>
          <DropdownMenuItem
            onClick={(): void => {
              setCurrentRow(row);
              setIsDeleteDialogOpen(true);
            }}
          >
            Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <DialogContent className="max-h-screen overflow-y-scroll lg:max-w-(--breakpoint-lg)">
        <DialogHeader>
          <DialogTitle>{`${topologyNamespace}/${topologyName}`}</DialogTitle>
          <DialogDescription />
        </DialogHeader>
        <Editor
          defaultLanguage="yaml"
          defaultValue={stringifyYaml(row.original)}
          height="75vh"
          onMount={handleEditorDidMount}
          theme={getEditorTheme(theme.resolvedTheme)}
        />
        <DialogFooter className="sm:end">
          <DialogClose
            asChild={true}
            onClick={(): void => {
              setIsEditDialogOpen(false);
            }}
          >
            <Button
              size="sm"
              variant="outline"
            >
              Close
            </Button>
          </DialogClose>
          <DialogClose
            asChild={true}
            disabled={!isEditDialogOpen}
            onClick={(): void => {
              setIsEditDialogOpen(false);
            }}
          >
            <Button
              disabled={!isEditDialogOpen}
              onClick={(): void => {
                const body = JSON.stringify(parseYaml(editorRef.current?.getValue() ?? ""));

                updateTopology(topologyNamespace, topologyName, body).catch(
                  (updateError: unknown) => {
                    throw updateError;
                  },
                );

                getQueryClient()
                  .invalidateQueries({
                    queryKey: ["topologies", {}],
                  })
                  .catch((invalidateError: unknown) => {
                    throw invalidateError;
                  });
              }}
              size="sm"
              type="submit"
              variant="outline"
            >
              Submit
            </Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
