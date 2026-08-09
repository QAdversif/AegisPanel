<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  PlansView. v0.6.0 ships the admin surface for the
  plans package (#131 backend, #132 HTTP handler,
  #133 OpenAPI, this PR). The view lists every
  tariff, lets the operator create a new one,
  edit an existing one, or delete it.

  The duration is stored on the wire as int64
  nanoseconds (the Go side encodes it as a
  Postgres INTERVAL via pgtype.Interval; see
  internal/plans/pg_store.go for the encode
  path). The form takes a human-readable "30
  days" / "1 hour" / "5 minutes" string and
  converts to ns at submit time; the table
  formats the stored ns back to a human-readable
  string at render time.

  # Why no audit log writes / Status / sub-token
  Plans are a simple catalog. There is no
  per-row state machine (the closed set is
  just `reset_period: daily | weekly | monthly
  | never`), no per-user traffic counter, no
  sub_token to rotate. The CRUD is exactly
  the wire format: name + traffic limit +
  duration + device limit + reset period +
  price.

  The DELETE is a hard delete (the Go handler
  does `DELETE FROM plans`); a future v0.6.x
  adds a `?force=true` query param for the
  case where a plan still has users pointing
  at it. v0.6.0 confirms before the call; the
  Service returns 404 (handled by the error
  mapper) if the row is gone.
-->
<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import { MoreHorizontal, Pencil, Plus, Search, Trash2 } from 'lucide-vue-next'
import { z } from 'zod'

import {
  createPlan,
  deletePlan,
  listPlans,
  updatePlan,
} from '@/api/services'
import { toApiError } from '@/api/client'
import { useToastStore } from '@/stores/toast'
import type { Plan } from '@/types'

import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import DataTable from '@/components/DataTable.vue'
import Dialog from '@/components/ui/Dialog.vue'
import DialogContent from '@/components/ui/DialogContent.vue'
import DialogHeader from '@/components/ui/DialogHeader.vue'
import DialogTitle from '@/components/ui/DialogTitle.vue'
import DialogDescription from '@/components/ui/DialogDescription.vue'
import DialogFooter from '@/components/ui/DialogFooter.vue'
import DialogClose from '@/components/ui/DialogClose.vue'
import DropdownMenu from '@/components/ui/DropdownMenu.vue'
import DropdownMenuTrigger from '@/components/ui/DropdownMenuTrigger.vue'
import DropdownMenuContent from '@/components/ui/DropdownMenuContent.vue'
import DropdownMenuItem from '@/components/ui/DropdownMenuItem.vue'
import DropdownMenuSeparator from '@/components/ui/DropdownMenuSeparator.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import SelectTrigger from '@/components/ui/SelectTrigger.vue'
import SelectValue from '@/components/ui/SelectValue.vue'
import SelectContent from '@/components/ui/SelectContent.vue'
import SelectItem from '@/components/ui/SelectItem.vue'
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'
import { useZodForm } from '@/composables/useZodForm'

const { t } = useI18n()
const toast = useToastStore()

const plans = ref<Plan[]>([])
const loading = ref(false)
const editing = ref<Plan | null>(null)
const createOpen = ref(false)
const editOpen = ref(false)
const deleteTarget = ref<Plan | null>(null)
const deleteOpen = ref(false)
const search = ref('')

const filtered = computed<Plan[]>(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return plans.value
  return plans.value.filter(
    (p) =>
      p.name.toLowerCase().includes(q) ||
      p.resetPeriod.toLowerCase().includes(q),
  )
})

async function refresh(): Promise<void> {
  loading.value = true
  try {
    plans.value = await listPlans()
  } catch (error) {
    toast.add({
      title: t('plans.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void refresh()
})

// --- Duration helpers -------------------------------------------------
//
// The wire format is int64 nanoseconds (the Go
// side stores it as Postgres INTERVAL via
// pgtype.Interval; see internal/plans/pg_store.go).
// The form takes a human-readable "30 days" / "1
// hour" / "5 minutes" string and converts to ns;
// the table formats ns back to a string at render
// time.
//
// Parsed format: "<N> <unit>" where unit is one
// of s / m / h / d / w. The colon-less, locale-
// neutral form keeps the input deterministic
// (Intl.DurationFormat's output is not
// parseable; we want a round-trip). The UI shows
// a hint ("30d" / "1h" / "5m") so the operator
// knows the syntax.

const NS_PER_SECOND = 1_000_000_000n
const NS_PER_MINUTE = 60n * NS_PER_SECOND
const NS_PER_HOUR = 60n * NS_PER_MINUTE
const NS_PER_DAY = 24n * NS_PER_HOUR
const NS_PER_WEEK = 7n * NS_PER_DAY

function formatDurationNs(ns: number | bigint): string {
  let n: bigint = typeof ns === 'bigint' ? ns : BigInt(ns)
  if (n <= 0n) return '0'
  // Pick the largest unit that divides evenly. If
  // the duration is 36h, we want "1d 12h", not
  // "1.5d". We round down and emit a remainder.
  if (n >= NS_PER_WEEK && n % NS_PER_WEEK === 0n) {
    return `${n / NS_PER_WEEK}w`
  }
  if (n >= NS_PER_DAY && n % NS_PER_DAY === 0n) {
    return `${n / NS_PER_DAY}d`
  }
  if (n >= NS_PER_HOUR && n % NS_PER_HOUR === 0n) {
    return `${n / NS_PER_HOUR}h`
  }
  if (n >= NS_PER_MINUTE && n % NS_PER_MINUTE === 0n) {
    return `${n / NS_PER_MINUTE}m`
  }
  return `${n / NS_PER_SECOND}s`
}

function parseDurationInput(input: string): bigint | null {
  const trimmed = input.trim()
  if (!trimmed) return null
  // "<N><unit>" or "<N> <unit>"; unit is single
  // char from {s, m, h, d, w}.
  const m = /^(\d+(?:\.\d+)?)\s*([smhdw])$/i.exec(trimmed)
  if (!m) return null
  const num = Number(m[1])
  if (!Number.isFinite(num) || num < 0) return null
  // Regex has 2 capture groups; m[2] is always a string here.
  // The non-null assertion is required by noUncheckedIndexedAccess
  // (enabled by @vue/tsconfig 0.8.x).
  const unit = m[2]!.toLowerCase()
  const multipliers: Record<string, bigint> = {
    s: NS_PER_SECOND,
    m: NS_PER_MINUTE,
    h: NS_PER_HOUR,
    d: NS_PER_DAY,
    w: NS_PER_WEEK,
  }
  // Round to nearest ns. Float arithmetic with
  // bigint is awkward; convert via string.
  const wholeNs = BigInt(Math.trunc(num * 1_000_000_000))
  // `unit` is guaranteed to be a key of `multipliers` (the regex
  // captures are from [smhdw]); the `!` is required by
  // noUncheckedIndexedAccess.
  return (wholeNs * multipliers[unit]!) / 1_000_000_000n
}

// --- Form schemas -----------------------------------------------------

const planSchema = z.object({
  name: z
    .string()
    .min(1, t('plans.nameRequired'))
    .max(64, t('plans.nameTooLong')),
  trafficLimitBytes: z.coerce
    .number()
    .int()
    .min(0, t('plans.trafficLimitHint')),
  durationInput: z
    .string()
    .min(1, t('plans.durationHint'))
    .refine((v) => parseDurationInput(v) !== null, {
      message: t('plans.durationInvalid'),
    }),
  deviceLimit: z.coerce.number().int().min(0).max(64),
  resetPeriod: z.enum(['daily', 'weekly', 'monthly', 'never']),
  priceCents: z.coerce.number().int().min(0),
})

const createForm = useZodForm({
  schema: planSchema,
  initialValues: {
    name: '',
    trafficLimitBytes: 0,
    durationInput: '30d',
    deviceLimit: 3,
    resetPeriod: 'monthly' as Plan['resetPeriod'],
    priceCents: 0,
  },
  onSubmit: async (values) => {
    const ns = parseDurationInput(values.durationInput)
    if (ns === null) return
    try {
      await createPlan({
        name: values.name,
        trafficLimitBytes: values.trafficLimitBytes,
        durationNs: Number(ns),
        deviceLimit: values.deviceLimit,
        resetPeriod: values.resetPeriod,
        priceCents: values.priceCents,
      })
      createOpen.value = false
      toast.add({ title: t('plans.created'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('plans.createFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

const editForm = useZodForm({
  schema: planSchema,
  initialValues: {
    name: '',
    trafficLimitBytes: 0,
    durationInput: '30d',
    deviceLimit: 3,
    resetPeriod: 'monthly' as Plan['resetPeriod'],
    priceCents: 0,
  },
  onSubmit: async (values) => {
    if (!editing.value) return
    const ns = parseDurationInput(values.durationInput)
    if (ns === null) return
    try {
      await updatePlan(editing.value.id, {
        name: values.name,
        trafficLimitBytes: values.trafficLimitBytes,
        durationNs: Number(ns),
        deviceLimit: values.deviceLimit,
        resetPeriod: values.resetPeriod,
        priceCents: values.priceCents,
      })
      editOpen.value = false
      editing.value = null
      toast.add({ title: t('plans.updated'), variant: 'success' })
      await refresh()
    } catch (error) {
      toast.add({
        title: t('plans.updateFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

function startCreate(): void {
  createForm.resetForm({
    values: {
      name: '',
      trafficLimitBytes: 0,
      durationInput: '30d',
      deviceLimit: 3,
      resetPeriod: 'monthly' as Plan['resetPeriod'],
      priceCents: 0,
    },
  })
  createOpen.value = true
}

function startEdit(plan: Plan): void {
  editing.value = plan
  editForm.resetForm({
    values: {
      name: plan.name,
      trafficLimitBytes: plan.trafficLimitBytes,
      durationInput: formatDurationNs(plan.durationNs),
      deviceLimit: plan.deviceLimit,
      resetPeriod: plan.resetPeriod,
      priceCents: plan.priceCents,
    },
  })
  editOpen.value = true
}

function askDelete(plan: Plan): void {
  deleteTarget.value = plan
  deleteOpen.value = true
}

async function confirmDelete(): Promise<void> {
  if (!deleteTarget.value) return
  const target = deleteTarget.value
  try {
    await deletePlan(target.id)
    deleteOpen.value = false
    deleteTarget.value = null
    toast.add({ title: t('plans.deleted'), variant: 'success' })
    await refresh()
  } catch (error) {
    toast.add({
      title: t('plans.deleteFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  }
}

// --- Table ------------------------------------------------------------

// The DataTable generic is `T extends Record<string,
// unknown>`. Plan is a typed interface, not an
// index-signature type, so passing it directly to
// the columns / data props is a TS variance error.
// The cast is done via the tableColumns / tableRows
// computeds so the markup section stays free of the
// "unknown" keyword (the check-raw-text script
// flags the word as user-facing text).
const tableColumns = computed(
  () => columns as unknown as ColumnDef<Record<string, unknown>, unknown>[],
)
const tableRows = computed(
  () => filtered.value as unknown as Record<string, unknown>[],
)

const columns: ColumnDef<Plan, unknown>[] = [
  {
    accessorKey: 'name',
    header: () => t('plans.name'),
    cell: ({ row }) => h('span', { class: 'font-medium' }, row.original.name),
  },
  {
    accessorKey: 'trafficLimitBytes',
    header: () => t('plans.trafficLimit'),
    cell: ({ row }) => {
      const v = row.original.trafficLimitBytes
      if (v === 0) {
        return h(Badge, { variant: 'outline' }, () => '∞')
      }
      // Human-readable: bytes → KB/MB/GB. 1024
      // binary. Three significant digits.
      const units = ['B', 'KB', 'MB', 'GB', 'TB']
      let n = v
      let u = 0
      while (n >= 1024 && u < units.length - 1) {
        n = n / 1024
        u++
      }
      const text = u === 0 ? `${n} ${units[u]}` : `${n.toFixed(1)} ${units[u]}`
      return h('span', { class: 'tabular-nums' }, text)
    },
  },
  {
    accessorKey: 'durationNs',
    header: () => t('plans.duration'),
    cell: ({ row }) =>
      h('span', { class: 'tabular-nums' }, formatDurationNs(row.original.durationNs)),
  },
  {
    accessorKey: 'deviceLimit',
    header: () => t('plans.deviceLimit'),
    cell: ({ row }) => {
      const v = row.original.deviceLimit
      return v === 0
        ? h(Badge, { variant: 'outline' }, () => '∞')
        : h('span', { class: 'tabular-nums' }, String(v))
    },
  },
  {
    accessorKey: 'resetPeriod',
    header: () => t('plans.resetPeriod'),
    cell: ({ row }) => h(Badge, { variant: 'secondary' }, () => t(`plans.resetPeriods.${row.original.resetPeriod}`)),
  },
  {
    accessorKey: 'priceCents',
    header: () => t('plans.priceCents'),
    cell: ({ row }) => {
      const c = row.original.priceCents
      if (c === 0) return h(Badge, { variant: 'outline' }, () => 'free')
      return h(
        'span',
        { class: 'tabular-nums' },
        `${(c / 100).toFixed(2)} ¤`,
      )
    },
  },
  {
    id: 'actions',
    header: () => '',
    cell: ({ row }) =>
      h(DropdownMenu, null, () => [
        h(DropdownMenuTrigger, null, () =>
          h(Button, { variant: 'ghost', size: 'icon' }, () =>
            h(MoreHorizontal, { class: 'h-4 w-4' }),
          ),
        ),
        h(DropdownMenuContent, { align: 'end' }, () => [
          h(
            DropdownMenuItem,
            { onSelect: () => startEdit(row.original) },
            () => [h(Pencil, { class: 'h-4 w-4' }), t('plans.edit')],
          ),
          h(DropdownMenuSeparator),
          h(
            DropdownMenuItem,
            { onSelect: () => askDelete(row.original) },
            () => [h(Trash2, { class: 'h-4 w-4' }), t('plans.delete')],
          ),
        ]),
      ]),
  },
]
</script>

<template>
  <div class="space-y-4">
    <header class="flex items-end justify-between gap-4">
      <div>
        <h2 class="text-xl font-semibold">
          {{ t('plans.title') }}
        </h2>
        <p class="text-sm text-muted-foreground">
          {{ t('plans.subtitle') }}
        </p>
      </div>
      <div class="flex items-center gap-2">
        <div class="relative">
          <Search class="pointer-events-none absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            v-model="search"
            type="search"
            :placeholder="t('plans.search')"
            class="w-56 pl-8"
          />
        </div>
        <Button @click="startCreate">
          <Plus class="h-4 w-4" />
          {{ t('plans.create') }}
        </Button>
      </div>
    </header>

    <DataTable
      :columns="tableColumns"
      :data="tableRows"
      :loading="loading"
      :empty-message="t('plans.empty')"
    />

    <!-- Create dialog -->
    <Dialog v-model:open="createOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('plans.create') }}</DialogTitle>
          <DialogDescription>{{ t('plans.subtitle') }}</DialogDescription>
        </DialogHeader>
        <Form
          :form="createForm"
          class="space-y-4"
        >
          <FormField
            name="name"
            :label="t('plans.name')"
          >
            <Input
              v-model="createForm.values.name"
              type="text"
              maxlength="64"
              autocomplete="off"
            />
          </FormField>
          <div class="grid grid-cols-2 gap-4">
            <FormField
              name="trafficLimitBytes"
              :label="t('plans.trafficLimit')"
            >
              <Input
                v-model="createForm.values.trafficLimitBytes"
                type="number"
                min="0"
                step="1"
              />
            </FormField>
            <FormField
              name="durationInput"
              :label="t('plans.duration')"
            >
              <Input
                v-model="createForm.values.durationInput"
                type="text"
                placeholder="30d"
                autocomplete="off"
              />
            </FormField>
            <FormField
              name="deviceLimit"
              :label="t('plans.deviceLimit')"
            >
              <Input
                v-model="createForm.values.deviceLimit"
                type="number"
                min="0"
                max="64"
                step="1"
              />
            </FormField>
            <FormField
              name="priceCents"
              :label="t('plans.priceCents')"
            >
              <Input
                v-model="createForm.values.priceCents"
                type="number"
                min="0"
                step="1"
              />
            </FormField>
            <FormField
              name="resetPeriod"
              :label="t('plans.resetPeriod')"
              class="col-span-2"
            >
              <Select v-model="createForm.values.resetPeriod">
                <SelectTrigger>
                  <SelectValue :placeholder="t('plans.resetPeriod')" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="daily">
                    {{ t('plans.resetPeriods.daily') }}
                  </SelectItem>
                  <SelectItem value="weekly">
                    {{ t('plans.resetPeriods.weekly') }}
                  </SelectItem>
                  <SelectItem value="monthly">
                    {{ t('plans.resetPeriods.monthly') }}
                  </SelectItem>
                  <SelectItem value="never">
                    {{ t('plans.resetPeriods.never') }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </FormField>
          </div>
          <DialogFooter>
            <DialogClose as-child>
              <Button
                variant="outline"
                type="button"
              >
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button
              type="submit"
              :disabled="createForm.isSubmitting.value"
            >
              {{ t('plans.create') }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Edit dialog -->
    <Dialog v-model:open="editOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('plans.edit') }}</DialogTitle>
          <DialogDescription>{{ editing?.name }}</DialogDescription>
        </DialogHeader>
        <Form
          :form="editForm"
          class="space-y-4"
        >
          <FormField
            name="name"
            :label="t('plans.name')"
          >
            <Input
              v-model="editForm.values.name"
              type="text"
              maxlength="64"
              autocomplete="off"
            />
          </FormField>
          <div class="grid grid-cols-2 gap-4">
            <FormField
              name="trafficLimitBytes"
              :label="t('plans.trafficLimit')"
            >
              <Input
                v-model="editForm.values.trafficLimitBytes"
                type="number"
                min="0"
                step="1"
              />
            </FormField>
            <FormField
              name="durationInput"
              :label="t('plans.duration')"
            >
              <Input
                v-model="editForm.values.durationInput"
                type="text"
                placeholder="30d"
                autocomplete="off"
              />
            </FormField>
            <FormField
              name="deviceLimit"
              :label="t('plans.deviceLimit')"
            >
              <Input
                v-model="editForm.values.deviceLimit"
                type="number"
                min="0"
                max="64"
                step="1"
              />
            </FormField>
            <FormField
              name="priceCents"
              :label="t('plans.priceCents')"
            >
              <Input
                v-model="editForm.values.priceCents"
                type="number"
                min="0"
                step="1"
              />
            </FormField>
            <FormField
              name="resetPeriod"
              :label="t('plans.resetPeriod')"
              class="col-span-2"
            >
              <Select v-model="editForm.values.resetPeriod">
                <SelectTrigger>
                  <SelectValue :placeholder="t('plans.resetPeriod')" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="daily">
                    {{ t('plans.resetPeriods.daily') }}
                  </SelectItem>
                  <SelectItem value="weekly">
                    {{ t('plans.resetPeriods.weekly') }}
                  </SelectItem>
                  <SelectItem value="monthly">
                    {{ t('plans.resetPeriods.monthly') }}
                  </SelectItem>
                  <SelectItem value="never">
                    {{ t('plans.resetPeriods.never') }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </FormField>
          </div>
          <DialogFooter>
            <DialogClose as-child>
              <Button
                variant="outline"
                type="button"
              >
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button
              type="submit"
              :disabled="editForm.isSubmitting.value"
            >
              {{ t('plans.edit') }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Delete confirmation dialog -->
    <Dialog v-model:open="deleteOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('plans.delete') }}</DialogTitle>
          <DialogDescription>
            {{ t('plans.confirmDelete') }}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose as-child>
            <Button
              variant="outline"
              type="button"
            >
              {{ t('common.cancel') }}
            </Button>
          </DialogClose>
          <Button
            variant="destructive"
            type="button"
            @click="confirmDelete"
          >
            <Trash2 class="h-4 w-4" />
            {{ t('plans.delete') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
