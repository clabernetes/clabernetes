import { Button } from "@/components/ui/button";
import type { Column, ColumnDef } from "@tanstack/react-table";
import { ArrowUpDown } from "lucide-react";
import type { ReactElement } from "react";
import type { C9sRunTopologyV1Alpha1 } from "@/lib/clabernetes-client";

export const columns: ColumnDef<C9sRunTopologyV1Alpha1>[] = [
  {
    header: "Expand",
    id: "expand",
  },
  {
    accessorKey: "metadata.namespace",
    header: ({
      column,
    }: { column: Column<C9sRunTopologyV1Alpha1> }): ReactElement => {
      return (
        <Button
          onClick={(): void => {
            column.toggleSorting(column.getIsSorted() === "asc");
          }}
          variant="ghost"
        >
          Namespace
          <ArrowUpDown className="ml-2 h-4 w-4" />
        </Button>
      );
    },
    id: "namespace",
  },
  {
    accessorKey: "metadata.name",
    header: ({
      column,
    }: { column: Column<C9sRunTopologyV1Alpha1> }): ReactElement => {
      return (
        <Button
          onClick={(): void => {
            column.toggleSorting(column.getIsSorted() === "asc");
          }}
          variant="ghost"
        >
          Name
          <ArrowUpDown className="ml-2 h-4 w-4" />
        </Button>
      );
    },
    id: "name",
  },
  {
    header: "Actions",
    id: "actions",
  },
];
