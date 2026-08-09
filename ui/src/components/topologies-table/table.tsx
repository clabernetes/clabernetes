"use client";
import { useQuery } from "@tanstack/react-query";
import { type ReactElement, useState } from "react";
import {
  type ColumnDef,
  type ColumnFiltersState,
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  type Row,
  type RowModel,
  type SortingState,
  type Updater,
  useReactTable,
} from "@tanstack/react-table";
import { Footer } from "@/components/topologies-table/footer.tsx";
import { Header } from "@/components/topologies-table/header.tsx";
import { columns } from "@/components/topologies-table/columns.tsx";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table.tsx";
import { listTopologies } from "@/lib/kubernetes.ts";
import type { C9sRunTopologyV1Alpha1 } from "@/lib/clabernetes-client";
import { Actions } from "@/components/topologies-table/actions.tsx";
import { Expand } from "@/components/topologies-table/expand.tsx";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible.tsx";
import { Button } from "@/components/ui/button.tsx";
import { FolderMinus, FolderPlus } from "lucide-react";
import { AlertDelete } from "@/components/topologies-table/alert-delete.tsx";

const milliSecondsInSecond = 1_000;

export enum RefetchInterval {
  Never = "Never",
  ThreeSeconds = "3s",
  TenSeconds = "10s",
  OneMinute = "60s",
  FiveMinutes = "300s",
}

function refetchIntervalToMilliSeconds(refetch: RefetchInterval): false | number {
  if (refetch === RefetchInterval.Never) {
    return false;
  }

  return milliSecondsInSecond * Number.parseInt(refetch.replace("s", ""), 10);
}

export function getExpandCollapseIcon(isExpanded: boolean | undefined): ReactElement {
  if (isExpanded) {
    return <FolderMinus className="h-4 w-4" />;
  }

  return <FolderPlus className="h-4 w-4" />;
}

function renderRow(
  columns: ColumnDef<C9sRunTopologyV1Alpha1>[],
  expandedRows: Record<string, boolean>,
  getRowModel: () => RowModel<C9sRunTopologyV1Alpha1>,
  setCurrentRow: (value: Row<C9sRunTopologyV1Alpha1>) => void,
  setIsDeleteDialogOpen: (value: boolean) => void,
  setExpandedRows: (value: Record<string, boolean>) => void,
): ReactElement | ReactElement[] {
  if (getRowModel().rows.length === 0) {
    return (
      <TableRow>
        <TableCell
          className="h-24 text-center"
          colSpan={columns.length}
        >
          No results...
        </TableCell>
      </TableRow>
    );
  }

  return getRowModel().rows.map((row): ReactElement => {
    return (
      <Collapsible
        asChild={true}
        key={row.id}
        open={row.id in expandedRows}
      >
        <>
          <TableRow
            data-state={row.getIsSelected() && "selected"}
            key={row.id}
          >
            {row.getVisibleCells().map((cell) => {
              switch (cell.column.id) {
                case "expand":
                  return (
                    <TableCell key={cell.id}>
                      <CollapsibleTrigger asChild={true}>
                        <Button
                          onClick={(): void => {
                            const isExpanded = expandedRows[row.id];

                            const newExpandedRows = { ...expandedRows };

                            if (isExpanded) {
                              delete newExpandedRows[row.id];
                            } else {
                              newExpandedRows[row.id] = true;
                            }

                            setExpandedRows(newExpandedRows);
                          }}
                          size="sm"
                          variant="ghost"
                        >
                          {getExpandCollapseIcon(expandedRows[row.id])}
                        </Button>
                      </CollapsibleTrigger>
                    </TableCell>
                  );
                case "actions":
                  return (
                    <TableCell key={cell.id}>
                      <Actions
                        row={row}
                        setCurrentRow={setCurrentRow}
                        setIsDeleteDialogOpen={setIsDeleteDialogOpen}
                      />
                    </TableCell>
                  );
                default:
                  return (
                    <TableCell key={cell.id}>
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </TableCell>
                  );
              }
            })}
          </TableRow>
          <CollapsibleContent asChild={true}>
            <TableRow>
              <TableCell colSpan={columns.length + 2}>
                <Expand row={row} />
              </TableCell>
            </TableRow>
          </CollapsibleContent>
        </>
      </Collapsible>
    );
  });
}

export function TopologiesTable(): ReactElement {
  const [refetch, setRefetch] = useState<RefetchInterval>(RefetchInterval.OneMinute);

  const [pagination, setPagination] = useState({
    pageIndex: 0,
    pageSize: 10,
  });
  const [sorting, setSorting] = useState<SortingState>([]);
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([]);

  const [expandedRows, setExpandedRows] = useState<Record<string, boolean>>({});
  const [isDeleteDialogOpen, setIsDeleteDialogOpen] = useState(false);
  const [currentRow, setCurrentRow] = useState<Row<C9sRunTopologyV1Alpha1>>();

  const { isPending, isError, data, error } = useQuery({
    enabled: true,
    // biome-ignore lint/suspicious/noExplicitAny: matching queryFn expectations/json.parse return
    queryFn: async (): Promise<any> => {
      const response = await listTopologies();

      return JSON.parse(response);
    },
    queryKey: ["topologies", {}],
    refetchInterval: refetchIntervalToMilliSeconds(refetch),
    refetchIntervalInBackground: false,
    refetchOnReconnect: true,
    refetchOnWindowFocus: true,
    retry: true,
    throwOnError: true,
  });

  const table = useReactTable({
    columns: columns,
    data: data,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getSortedRowModel: getSortedRowModel(),
    onColumnFiltersChange: (value: Updater<ColumnFiltersState>): void => {
      setExpandedRows({});
      setColumnFilters(value);
    },
    onPaginationChange: setPagination,
    onSortingChange: setSorting,
    state: {
      columnFilters: columnFilters,
      pagination: pagination,
      sorting: sorting,
    },
  });

  if (isPending) {
    return <span>Loading...</span>;
  }

  if (isError) {
    return <span>Error: {error.message}</span>;
  }

  return (
    <div className="w-[85vw]">
      <Header getColumn={table.getColumn} />
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id}>
                {headerGroup.headers.map((header) => {
                  return (
                    <TableHead key={header.id}>
                      {header.isPlaceholder
                        ? null
                        : flexRender(header.column.columnDef.header, header.getContext())}
                    </TableHead>
                  );
                })}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {renderRow(
              columns,
              expandedRows,
              table.getRowModel,
              setCurrentRow,
              setIsDeleteDialogOpen,
              setExpandedRows,
            )}
          </TableBody>
        </Table>
      </div>
      <Footer
        getCanNextPage={table.getCanNextPage}
        getCanPreviousPage={table.getCanPreviousPage}
        nextPage={table.nextPage}
        previousPage={table.previousPage}
        refetch={refetch}
        setRefetch={setRefetch}
      />
      <AlertDelete
        row={currentRow}
        isDeleteDialogOpen={isDeleteDialogOpen}
        setIsDeleteDialogOpen={setIsDeleteDialogOpen}
      />
    </div>
  );
}
