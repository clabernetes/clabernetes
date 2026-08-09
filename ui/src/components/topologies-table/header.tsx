import { Input } from "@/components/ui/input";
import type { Column } from "@tanstack/react-table";
import type { ReactElement } from "react";
import type { C9sRunTopologyV1Alpha1 } from "@/lib/clabernetes-client";
import { Sheet, SheetTrigger } from "@/components/ui/sheet.tsx";
import { CirclePlus } from "lucide-react";
import { Button } from "@/components/ui/button.tsx";
import { CreateSheet } from "@/components/topologies-table/create-sheet.tsx";

interface HeaderProps {
  readonly getColumn: (
    columnId: string,
  ) => Column<C9sRunTopologyV1Alpha1> | undefined;
}

export function Header(props: HeaderProps): ReactElement {
  const { getColumn } = props;

  return (
    <div className="relative flex items-center">
      <div className="flex-1" />
      <div className="absolute left-1/2 transform -translate-x-1/2 flex w-3/5 max-w-2xl justify-center space-x-4 px-4">
        <Input
          className="grow"
          onChange={(event): void => {
            getColumn("namespace")?.setFilterValue(event.target.value);
          }}
          placeholder="Filter by namespace..."
          value={getColumn("namespace")?.getFilterValue() as string}
        />
        <Input
          className="grow"
          onChange={(event): void => {
            getColumn("name")?.setFilterValue(event.target.value);
          }}
          placeholder="Filter by name..."
          value={getColumn("name")?.getFilterValue() as string}
        />
      </div>
      <div className="flex items-center justify-end py-4">
        <Sheet>
          <SheetTrigger asChild={true}>
            <Button
              size="sm"
              variant="outline"
            >
              <CirclePlus className="h-4 w-4" />
            </Button>
          </SheetTrigger>
          <CreateSheet />
        </Sheet>
      </div>
    </div>
  );
}
