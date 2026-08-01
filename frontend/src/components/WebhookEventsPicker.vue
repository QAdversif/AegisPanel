<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  WebhookEventsPicker. A grid of checkboxes for
  the 18 closed event types the panel publishes,
  grouped by entity (user / plan / node / host /
  backup / inbound). Pairs with `useZodForm` via
  the `value` + `change` contract — the parent
  form binds the value through `setFieldValue`
  so the zod schema stays the single source of
  truth.

  # Why a custom picker

  Radix-Vue's `Select` is single-select by design
  (the `SelectRoot` `modelValue` is a single
  string). For 18 fixed options we want a grid of
  checkboxes so the operator can pick many at
  once without an extra "open the dropdown"
  interaction. A native checkbox grid is also
  accessible (the label is the text, the input
  is the control) without us having to wire up
  roving tabindex.

  # All-events wildcard

  The backend treats an empty `events` slice as
  "all events" (see `webhooks.Endpoint.MatchesEvent`).
  The picker shows this as an explicit "all"
  badge when the value is empty. There is no
  dedicated "select all" checkbox — picking
  every category by hand is fine, and the
  wildcard is a wire-level shortcut, not a
  separate UI concept.
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { WebhookEventType } from '@/api/services/webhooks'
import Badge from '@/components/ui/Badge.vue'
import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    /** Current selection. `[]` means the
     * "all events" wildcard. */
    value: WebhookEventType[]
    /** Show a destructive ring when validation
     * fails. Optional — the parent form passes
     * the field's `hasError` through. */
    hasError?: boolean
  }>(),
  {
    hasError: false,
  },
)

const emit = defineEmits<{
  /** Fired with the new selection after a toggle.
   * The parent forwards this to
   * `setFieldValue('events', v)`. */
  change: [value: WebhookEventType[]]
}>()

const { t } = useI18n()

/**
 * Closed list of event types, 1:1 with
 * `webhooks.AllowedEventTypes` in
 * `backend/internal/webhooks/endpoint.go`.
 * Grouped by entity so the operator can scan
 * the matrix top-to-bottom instead of reading
 * 18 unsorted rows. The grouping also makes
 * it obvious which events are "user" vs
 * "plan" etc., so the operator can choose to
 * subscribe to a whole entity's lifecycle
 * without listing each row.
 */
const eventGroups = computed<Array<{ key: string; events: WebhookEventType[] }>>(
  () => [
    {
      key: 'user',
      events: ['user.created', 'user.updated', 'user.deleted'],
    },
    {
      key: 'plan',
      events: ['plan.created', 'plan.updated', 'plan.deleted'],
    },
    {
      key: 'node',
      events: ['node.created', 'node.updated', 'node.deleted'],
    },
    {
      key: 'host',
      events: ['host.created', 'host.updated', 'host.deleted'],
    },
    {
      key: 'backup',
      events: ['backup.created', 'backup.completed', 'backup.failed'],
    },
    {
      key: 'inbound',
      events: ['inbound.created', 'inbound.updated', 'inbound.deleted'],
    },
  ],
)

/** Set lookup for O(1) toggle. Recomputed
 * whenever the parent pushes a new `value`. */
const selectedSet = computed(() => new Set(props.value))

function isSelected(event: WebhookEventType): boolean {
  return selectedSet.value.has(event)
}

/** Toggle an event in / out of the selection.
 * We always emit a fresh array (never mutate
 * the parent's array) so vee-validate's
 * `setFieldValue` re-runs the schema. */
function onToggle(event: WebhookEventType, checked: boolean): void {
  if (checked) {
    if (selectedSet.value.has(event)) return
    emit('change', [...props.value, event])
    return
  }
  emit('change', props.value.filter((e) => e !== event))
}
</script>

<template>
  <div
    :class="
      cn(
        'space-y-3 rounded-md border bg-muted/30 p-3',
        props.hasError && 'border-destructive',
      )
    "
  >
    <div class="flex items-center justify-between gap-2">
      <p class="text-sm font-medium">
        {{ t('webhooks.eventsPicker.heading') }}
      </p>
      <Badge variant="secondary">
        {{
          props.value.length === 0
            ? t('webhooks.eventsAll')
            : t('webhooks.eventsPicker.selectedCount', {
                count: props.value.length,
                total: 18,
              })
        }}
      </Badge>
    </div>

    <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      <fieldset
        v-for="group in eventGroups"
        :key="group.key"
        class="space-y-1.5"
      >
        <legend class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          {{ t(`webhooks.eventsPicker.groups.${group.key}`) }}
        </legend>
        <label
          v-for="event in group.events"
          :key="event"
          class="flex cursor-pointer items-center gap-2 rounded-sm px-1.5 py-1 text-sm hover:bg-background"
        >
          <input
            type="checkbox"
            class="size-4 rounded border-input text-primary focus:ring-2 focus:ring-ring"
            :checked="isSelected(event)"
            @change="(e) => onToggle(event, (e.target as HTMLInputElement).checked)"
          />
          <code class="font-mono text-xs">{{ event }}</code>
        </label>
      </fieldset>
    </div>
  </div>
</template>
