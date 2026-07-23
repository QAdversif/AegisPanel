<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  AuditsView. v0.2.0 ships the audit log read surface
  so the operator can see who changed what and when.
  The view is read-only — the in-handler
  `audits.Record(...)` call-sites for the mutating
  nodes / hosts / inbounds / users / panelcfg
  handlers land in v0.3 alongside the v0.3 work.

  The list path returns entries with `before` /
  `after` elided (bandwidth-conscious). Clicking a
  row opens a detail dialog that calls the `/{id}`
  endpoint to fetch the full entry with the JSONB
  blobs.

  The filters (action, resource_type, actorId,
  since, until, limit) drive the query string on
  the GET / endpoint. Empty filters return the
  most recent 100 entries.
-->
<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ColumnDef } from '@tanstack/vue-table'
import { Filter, RefreshCw, X } from 'lucide-vue-next'
import { z } from 'zod'

import { getAudit, listAudits } from '@/api/services'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import type { AuditEntry } from '@/types'
import { useZodForm } from '@/composables/useZodForm'

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
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'
import Input from '@/components/ui/Input.vue'

const { t } = useI18n()
const toast = useToastStore()

const entries = ref<AuditEntry[]>([])
const loading = ref(false)
const detailOpen = ref(false)
const detail = ref<AuditEntry | null>(null)
const detailLoading = ref(false)
const filterOpen = ref(false)

async function refresh(filters: ListFilters = {}): Promise<void> {
  loading.value = true
  try {
    entries.value = await listAudits(filters)
  } catch (error) {
    toast.add({
      title: t('audits.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

interface ListFilters {
  action?: string
  resourceType?: string
  since?: string
  until?: string
}

const currentFilters = ref<ListFilters>({})

const filterSchema = z.object({
  action: z.string().optional(),
  resourceType: z.string().optional(),
  since: z.string().optional(),
  until: z.string().optional(),
})

const filterForm = useZodForm({
  schema: filterSchema,
  initialValues: {
    action: currentFilters.value.action ?? '',
    resourceType: currentFilters.value.resourceType ?? '',
    since: currentFilters.value.since ?? '',
    until: currentFilters.value.until ?? '',
  },
  onSubmit: async (values) => {
    const next: ListFilters = {}
    if (values.action) next.action = values.action
    if (values.resourceType) next.resourceType = values.resourceType
    if (values.since) next.since = new Date(values.since).toISOString()
    if (values.until) next.until = new Date(values.until).toISOString()
    currentFilters.value = next
    filterOpen.value = false
    await refresh(next)
  },
})

function clearFilters(): void {
  currentFilters.value = {}
  filterForm.resetForm({ values: { action: '', resourceType: '', since: '', until: '' } })
  void refresh({})
}

const hasActiveFilters = computed(
  () =>
    !!currentFilters.value.action ||
    !!currentFilters.value.resourceType ||
    !!currentFilters.value.since ||
    !!currentFilters.value.until,
)

async function showDetail(entry: AuditEntry): Promise<void> {
  detail.value = entry
  detailOpen.value = true
  // The list path returns entries with before/after
  // elided; re-fetch the full row so the dialog
  // shows the diff.
  detailLoading.value = true
  try {
    const full = await getAudit(entry.id)
    if (full) detail.value = full
  } catch (error) {
    toast.add({
      title: t('audits.detailLoadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    detailLoading.value = false
  }
}

function formatTimestamp(iso: string): string {
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

function resourceLabel(entry: AuditEntry): string {
  if (entry.resourceId) return `${entry.resourceType}/${entry.resourceId.slice(0, 8)}`
  return entry.resourceType
}

function asJsonString(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

const columns: ColumnDef<AuditEntry, unknown>[] = [
  {
    accessorKey: 'createdAt',
    header: () => t('audits.timestamp'),
    cell: ({ row }) => h('span', { class: 'audits__mono' }, formatTimestamp(row.original.createdAt)),
  },
  {
    accessorKey: 'actorUsername',
    header: () => t('audits.actor'),
    cell: ({ row }) =>
      h(
        'span',
        { class: 'audits__mono' },
        row.original.actorUsername ?? t('audits.systemActor'),
      ),
  },
  {
    accessorKey: 'action',
    header: () => t('audits.action'),
    cell: ({ row }) => h('code', { class: 'audits__action' }, row.original.action),
  },
  {
    id: 'resource',
    header: () => t('audits.resource'),
    cell: ({ row }) =>
      h('span', { class: 'audits__mono' }, resourceLabel(row.original)),
  },
  {
    accessorKey: 'ip',
    header: () => t('audits.ip'),
    cell: ({ row }) =>
      h('span', { class: 'audits__mono' }, row.original.ip ?? '—'),
  },
  {
    id: 'inspect',
    header: () => h('span', { class: 'sr-only' }, t('common.actions')),
    cell: ({ row }) =>
      h(
        Button,
        {
          variant: 'ghost',
          size: 'sm',
          onClick: () => showDetail(row.original),
        },
        () => t('audits.inspect'),
      ),
  },
]

onMounted(() => {
  void refresh({})
})
</script>

<template>
  <section class="audits">
    <header class="audits__header">
      <div>
        <h1 class="audits__title">
          {{ t('audits.title') }}
        </h1>
        <p class="audits__subtitle">
          {{ t('audits.subtitle') }}
        </p>
      </div>
      <div class="audits__toolbar">
        <Button
          variant="outline"
          @click="filterOpen = true"
        >
          <Filter class="h-4 w-4" />
          {{ t('audits.filter') }}
          <Badge
            v-if="hasActiveFilters"
            variant="secondary"
            class="ml-1"
          >
            {{ t('audits.filterActive') }}
          </Badge>
        </Button>
        <Button
          v-if="hasActiveFilters"
          variant="ghost"
          @click="clearFilters"
        >
          <X class="h-4 w-4" />
          {{ t('audits.clearFilters') }}
        </Button>
        <Button
          variant="outline"
          :disabled="loading"
          @click="refresh(currentFilters)"
        >
          <RefreshCw class="h-4 w-4" />
          {{ t('common.refresh') }}
        </Button>
      </div>
    </header>

    <DataTable
      :columns="columns"
      :data="entries"
      :loading="loading"
      :search-key="'audits.search'"
      :empty-key="'audits.empty'"
    />

    <!-- Filter dialog -->
    <Dialog v-model:open="filterOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('audits.filterTitle') }}</DialogTitle>
          <DialogDescription>{{ t('audits.filterDescription') }}</DialogDescription>
        </DialogHeader>
        <Form
          :is-submitting="filterForm.isSubmitting.value"
          @submit="filterForm.handleSubmit"
        >
          <FormField
            name="action"
            :label="t('audits.action')"
            :hint="t('audits.filterActionHint')"
          >
            <template #default="{ id, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="filterForm.values.action ?? ''"
                :class="hasError && 'border-destructive'"
                placeholder="user.create"
                @update:model-value="(v: string) => filterForm.setFieldValue('action', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="resourceType"
            :label="t('audits.resourceType')"
            :hint="t('audits.filterResourceTypeHint')"
          >
            <template #default="{ id, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="filterForm.values.resourceType ?? ''"
                :class="hasError && 'border-destructive'"
                placeholder="user"
                @update:model-value="(v: string) => filterForm.setFieldValue('resourceType', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="since"
            :label="t('audits.since')"
            :hint="t('audits.filterSinceHint')"
          >
            <template #default="{ id, onBlur, hasError }">
              <Input
                :id="id"
                type="datetime-local"
                :model-value="filterForm.values.since ?? ''"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => filterForm.setFieldValue('since', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="until"
            :label="t('audits.until')"
            :hint="t('audits.filterUntilHint')"
          >
            <template #default="{ id, onBlur, hasError }">
              <Input
                :id="id"
                type="datetime-local"
                :model-value="filterForm.values.until ?? ''"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => filterForm.setFieldValue('until', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <DialogFooter>
            <DialogClose>
              <Button
                type="button"
                variant="outline"
              >
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button
              type="submit"
              :disabled="filterForm.isSubmitting.value"
            >
              {{ t('audits.applyFilters') }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>

    <!-- Detail dialog -->
    <Dialog v-model:open="detailOpen">
      <DialogContent v-if="detail">
        <DialogHeader>
          <DialogTitle>{{ t('audits.detailTitle') }}</DialogTitle>
          <DialogDescription>
            {{ t('audits.detailDescription') }}
          </DialogDescription>
        </DialogHeader>
        <div class="audits__detail">
          <dl class="audits__meta-list">
            <div class="audits__meta-row">
              <dt>{{ t('audits.timestamp') }}</dt>
              <dd class="audits__mono">
                {{ formatTimestamp(detail.createdAt) }}
              </dd>
            </div>
            <div class="audits__meta-row">
              <dt>{{ t('audits.actor') }}</dt>
              <dd class="audits__mono">
                {{ detail.actorUsername ?? t('audits.systemActor') }}
                <span
                  v-if="detail.actorId"
                  class="audits__dim"
                >({{ detail.actorId }})</span>
              </dd>
            </div>
            <div class="audits__meta-row">
              <dt>{{ t('audits.action') }}</dt>
              <dd><code class="audits__action">{{ detail.action }}</code></dd>
            </div>
            <div class="audits__meta-row">
              <dt>{{ t('audits.resource') }}</dt>
              <dd class="audits__mono">
                {{ detail.resourceType }}<span v-if="detail.resourceId">/{{ detail.resourceId }}</span>
              </dd>
            </div>
            <div
              v-if="detail.ip"
              class="audits__meta-row"
            >
              <dt>{{ t('audits.ip') }}</dt>
              <dd class="audits__mono">
                {{ detail.ip }}
              </dd>
            </div>
            <div
              v-if="detail.userAgent"
              class="audits__meta-row"
            >
              <dt>{{ t('audits.userAgent') }}</dt>
              <dd class="audits__mono audits__ua">
                {{ detail.userAgent }}
              </dd>
            </div>
          </dl>

          <div
            v-if="detailLoading"
            class="audits__loading"
          >
            {{ t('audits.detailLoading') }}
          </div>
          <template v-else>
            <div
              v-if="detail.before !== undefined"
              class="audits__diff"
            >
              <h4>{{ t('audits.before') }}</h4>
              <pre class="audits__code">{{ asJsonString(detail.before) || '—' }}</pre>
            </div>
            <div
              v-if="detail.after !== undefined"
              class="audits__diff"
            >
              <h4>{{ t('audits.after') }}</h4>
              <pre class="audits__code">{{ asJsonString(detail.after) || '—' }}</pre>
            </div>
            <p
              v-if="detail.before === undefined && detail.after === undefined"
              class="audits__no-diff"
            >
              {{ t('audits.noDiff') }}
            </p>
          </template>
        </div>
        <DialogFooter>
          <DialogClose>
            <Button
              type="button"
              variant="outline"
            >
              {{ t('common.close') }}
            </Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </section>
</template>

<style scoped>
.audits {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.audits__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  flex-wrap: wrap;
}

.audits__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.audits__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}

.audits__toolbar {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.audits__mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.8125rem;
}

.audits__action {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.8125rem;
  background: hsl(var(--muted));
  padding: 0.125rem 0.5rem;
  border-radius: 0.25rem;
}

.audits__detail {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.audits__meta-list {
  margin: 0;
  display: grid;
  grid-template-columns: 7rem 1fr;
  gap: 0.5rem 1rem;
  font-size: 0.875rem;
}

.audits__meta-row {
  display: contents;
}

.audits__meta-row dt {
  color: hsl(var(--muted-foreground));
}

.audits__meta-row dd {
  margin: 0;
  word-break: break-all;
}

.audits__dim {
  color: hsl(var(--muted-foreground));
  margin-left: 0.25rem;
}

.audits__ua {
  word-break: break-all;
}

.audits__diff {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.audits__diff h4 {
  margin: 0;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: hsl(var(--muted-foreground));
}

.audits__code {
  margin: 0;
  padding: 0.75rem;
  background: hsl(var(--muted));
  border-radius: 0.375rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.75rem;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 16rem;
  overflow: auto;
}

.audits__loading,
.audits__no-diff {
  color: hsl(var(--muted-foreground));
  font-size: 0.875rem;
}
</style>
