import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { getQueryClient } from "@/lib/get-query-client";
import { deleteTopology } from "@/lib/kubernetes";
import type { ReactElement } from "react";
import type React from "react";
import type { Row } from "@tanstack/react-table";
import type { C9sRunTopologyV1Alpha1 } from "@/lib/clabernetes-client";

interface AlertDeleteProps {
  readonly row: Row<C9sRunTopologyV1Alpha1> | undefined;
  readonly isDeleteDialogOpen: boolean;
  readonly setIsDeleteDialogOpen: React.Dispatch<React.SetStateAction<boolean>>;
}

export function AlertDelete(props: AlertDeleteProps): ReactElement {
  const { row, isDeleteDialogOpen, setIsDeleteDialogOpen } = props;

  if (row === undefined) {
    return <></>;
  }

  const name = row.original.metadata?.name as string;
  const namespace = row.original.metadata?.namespace as string;

  return (
    <AlertDialog
      onOpenChange={setIsDeleteDialogOpen}
      open={isDeleteDialogOpen}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            Are you absolutely sure you want to delete {namespace}/{name}?
          </AlertDialogTitle>
          <AlertDialogDescription className="py-4">
            This will delete the object, this is permanent...
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={(): void => {
              deleteTopology(namespace, name).catch((deleteError: unknown) => {
                throw deleteError;
              });

              getQueryClient()
                .invalidateQueries({
                  queryKey: ["topologies", {}],
                })
                .catch((invalidateError: unknown) => {
                  throw invalidateError;
                });
            }}
          >
            Continue
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
