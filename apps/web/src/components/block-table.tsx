import { flexRender, getCoreRowModel, getSortedRowModel, useReactTable, type ColumnDef, type SortingState } from "@tanstack/react-table";
import { AnimatePresence, motion } from "motion/react";
import { ArrowDownUp } from "lucide-react";
import { useState } from "react";
import type { Block } from "@/lib/api";
import { short } from "@/lib/utils";

const columns: ColumnDef<Block>[] = [
  { accessorKey: "number", header: "Height", cell: (c) => <span className="font-mono">{c.getValue<number>().toLocaleString()}</span> },
  {
    accessorKey: "hash",
    header: "Hash",
    enableSorting: false,
    cell: (c) => <span className="font-mono text-muted">{short(c.getValue<string>(), 10, 6)}</span>,
  },
  { accessorKey: "txCount", header: "Txs" },
  { accessorKey: "timestamp", header: "Time", cell: (c) => new Date(c.getValue<number>() * 1000).toISOString().slice(11, 19) + " UTC" },
];

export function BlockTable({ blocks }: { blocks: Block[] }) {
  const [sorting, setSorting] = useState<SortingState>([{ id: "number", desc: true }]);
  // eslint-disable-next-line react-hooks/incompatible-library -- TanStack Table is intentionally non-memoized
  const table = useReactTable({
    data: blocks,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getRowId: (b) => b.hash,
  });
  return (
    <table className="w-full text-left text-sm" aria-label="Recent blocks">
      <thead className="text-xs uppercase text-muted">
        {table.getHeaderGroups().map((hg) => (
          <tr key={hg.id}>
            {hg.headers.map((h) => (
              <th key={h.id} className="pb-2 font-medium">
                {h.column.getCanSort() ? (
                  <button className="inline-flex items-center gap-1" onClick={h.column.getToggleSortingHandler()}>
                    {flexRender(h.column.columnDef.header, h.getContext())}
                    <ArrowDownUp className="size-3" aria-hidden />
                  </button>
                ) : (
                  flexRender(h.column.columnDef.header, h.getContext())
                )}
              </th>
            ))}
          </tr>
        ))}
      </thead>
      <tbody>
        <AnimatePresence initial={false}>
          {table.getRowModel().rows.map((r) => (
            <motion.tr
              key={r.id}
              layout
              initial={{ opacity: 0, y: -8 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0 }}
              className="border-t border-line"
            >
              {r.getVisibleCells().map((c) => (
                <td key={c.id} className="py-2 pr-4">
                  {flexRender(c.column.columnDef.cell, c.getContext())}
                </td>
              ))}
            </motion.tr>
          ))}
        </AnimatePresence>
      </tbody>
    </table>
  );
}
