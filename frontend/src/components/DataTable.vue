<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue DataTable. A typed wrapper around
  @tanstack/vue-table with the Aegis rhythm:
  sortable headers, global search, pagination,
  empty state.

  All user-facing strings go through vue-i18n
  (the `dataTable.*` namespace). The component
  also accepts override props for column /
  caller-specific copy (searchPlaceholder,
  caption, emptyLabel).

  Usage:

    <DataTable
      :columns="columns"
      :data="nodes"
      :loading="isLoading"
      search-key="nodes.search"
    />

  `search-key` is a vue-i18n key; the placeholder
  reads from `t(searchKey)`. `empty-key` does
  the same for the empty-state label. Passing
  the raw string (searchPlaceholder / emptyLabel)
  is also supported for non-i18n callers but the
  eslint rule flags it.
-->
<script setup lang="ts" generic="T extends Record<string, unknown>">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  FlexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useVueTable,
  type ColumnDef,
  type SortingState,
} from '@tanstack/vue-table'
import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Search } from 'lucide-vue-next'

import Button from './ui/Button.vue'
import Input from './ui/Input.vue'
import Skeleton from './ui/Skeleton.vue'
import Table from './ui/Table.vue'
import TableHeader from './ui/TableHeader.vue'
import TableBody from './ui/TableBody.vue'
import TableRow from './ui/TableRow.vue'
import TableHead from './ui/TableHead.vue'
import TableCell from './ui/TableCell.vue'
import TableCaption from './ui/TableCaption.vue'
import { cn } from '@/lib/utils'

const { t } = useI18n()

const props = withDefaults(
  defineProps<{
    columns: ColumnDef<T, unknown>[]
    data: T[]
    loading?: boolean
    caption?: string
    /** Override the search placeholder. Prefer
     * `search-key` so the string is translated.
     */
    searchPlaceholder?: string
    /** vue-i18n key for the search placeholder. */
    searchKey?: string
    initialPageSize?: number
    /** Override the empty-state label. Prefer
     * `empty-key`.
     */
    emptyLabel?: string
    /** vue-i18n key for the empty-state label. */
    emptyKey?: string
  }>(),
  {
    loading: false,
    caption: '',
    searchPlaceholder: '',
    searchKey: '',
    initialPageSize: 20,
    emptyLabel: '',
    emptyKey: '',
  },
)

const sorting = ref<SortingState>([])
const globalFilter = ref('')
const rowSelection = ref<Record<string, boolean>>({})

const table = useVueTable<T>({
  get data() {
    return props.data
  },
  get columns() {
    return props.columns
  },
  state: {
    get sorting() {
      return sorting.value
    },
    get globalFilter() {
      return globalFilter.value
    },
    get rowSelection() {
      return rowSelection.value
    },
  },
  onSortingChange: (updater) => {
    sorting.value = typeof updater === 'function' ? updater(sorting.value) : updater
  },
  onGlobalFilterChange: (updater) => {
    globalFilter.value = typeof updater === 'function' ? updater(globalFilter.value) : updater
  },
  onRowSelectionChange: (updater) => {
    rowSelection.value = typeof updater === 'function' ? updater(rowSelection.value) : updater
  },
  getCoreRowModel: getCoreRowModel(),
  getSortedRowModel: getSortedRowModel(),
  getFilteredRowModel: getFilteredRowModel(),
  getPaginationRowModel: getPaginationRowModel(),
  initialState: { pagination: { pageSize: props.initialPageSize } },
  enableRowSelection: true,
  getRowId: (row, index) => (row.id as string | number | undefined)?.toString() ?? `row-${index}`,
})

const hasSearch = computed(() => props.data.length > 0 || globalFilter.value.length > 0)

const computedSearchPlaceholder = computed(() => {
  if (props.searchPlaceholder) return props.searchPlaceholder
  if (props.searchKey) return t(props.searchKey)
  return t('dataTable.search')
})

const computedEmptyLabel = computed(() => {
  if (props.emptyLabel) return props.emptyLabel
  if (props.emptyKey) return t(props.emptyKey)
  return t('dataTable.empty')
})
</script>

<template>
  <div class="flex flex-col gap-3">
    <!-- Toolbar -->
    <div
      v-if="hasSearch || $slots.toolbar"
      class="flex flex-wrap items-center gap-2"
    >
      <div
        v-if="hasSearch"
        class="relative max-w-xs flex-1"
      >
        <Search
          class="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          v-model="globalFilter"
          :placeholder="computedSearchPlaceholder"
          class="pl-8"
        />
      </div>
      <div
        v-if="$slots.toolbar"
        class="ml-auto flex items-center gap-2"
      >
        <slot
          name="toolbar"
          :table="table"
        />
      </div>
    </div>

    <!-- Table -->
    <div class="rounded-md border">
      <Table>
        <TableCaption v-if="props.caption">
          {{ props.caption }}
        </TableCaption>
        <TableHeader>
          <TableRow
            v-for="headerGroup in table.getHeaderGroups()"
            :key="headerGroup.id"
          >
            <TableHead
              v-for="header in headerGroup.headers"
              :key="header.id"
              :class="cn(header.column.getCanSort() && 'cursor-pointer select-none')"
              @click="header.column.getCanSort() && header.column.toggleSorting()"
            >
              <FlexRender
                v-if="!header.isPlaceholder"
                :render="header.column.columnDef.header"
                :props="header.getContext()"
              />
              <span
                v-if="header.column.getIsSorted()"
                class="ml-1 text-xs"
                aria-hidden="true"
              >
                {{ header.column.getIsSorted() === 'asc' ? '▲' : '▼' }}
              </span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <template v-if="props.loading">
            <TableRow
              v-for="i in 5"
              :key="`skel-${i}`"
            >
              <TableCell
                v-for="col in props.columns"
                :key="col.id ?? ''"
              >
                <Skeleton class="h-4 w-3/4" />
              </TableCell>
            </TableRow>
          </template>
          <template v-else-if="table.getRowModel().rows.length > 0">
            <TableRow
              v-for="row in table.getRowModel().rows"
              :key="row.id"
              :data-state="row.getIsSelected() ? 'selected' : undefined"
            >
              <TableCell
                v-for="cell in row.getVisibleCells()"
                :key="cell.id"
              >
                <FlexRender
                  :render="cell.column.columnDef.cell"
                  :props="cell.getContext()"
                />
              </TableCell>
            </TableRow>
          </template>
          <template v-else>
            <TableRow>
              <TableCell
                :colspan="props.columns.length"
                class="h-24 text-center text-muted-foreground"
              >
                {{ computedEmptyLabel }}
              </TableCell>
            </TableRow>
          </template>
        </TableBody>
      </Table>
    </div>

    <!-- Footer / pagination -->
    <div
      v-if="!props.loading && table.getRowModel().rows.length > 0"
      class="flex flex-wrap items-center justify-between gap-2 px-2 text-sm text-muted-foreground"
    >
      <div>
        {{
          t('dataTable.selectedOf', {
            filtered: table.getFilteredRowModel().rows.length,
            total: props.data.length,
          })
        }}
      </div>
      <div class="flex items-center gap-2">
        <Button
          variant="outline"
          size="icon"
          :disabled="!table.getCanPreviousPage()"
          :aria-label="t('dataTable.firstPage')"
          @click="table.setPageIndex(0)"
        >
          <ChevronsLeft class="h-4 w-4" />
        </Button>
        <Button
          variant="outline"
          size="icon"
          :disabled="!table.getCanPreviousPage()"
          :aria-label="t('dataTable.previousPage')"
          @click="table.previousPage()"
        >
          <ChevronLeft class="h-4 w-4" />
        </Button>
        <span>
          {{
            t('dataTable.pageOf', {
              page: table.getState().pagination.pageIndex + 1,
              total: table.getPageCount(),
            })
          }}
        </span>
        <Button
          variant="outline"
          size="icon"
          :disabled="!table.getCanNextPage()"
          :aria-label="t('dataTable.nextPage')"
          @click="table.nextPage()"
        >
          <ChevronRight class="h-4 w-4" />
        </Button>
        <Button
          variant="outline"
          size="icon"
          :disabled="!table.getCanNextPage()"
          :aria-label="t('dataTable.lastPage')"
          @click="table.setPageIndex(table.getPageCount() - 1)"
        >
          <ChevronsRight class="h-4 w-4" />
        </Button>
      </div>
    </div>
  </div>
</template>
