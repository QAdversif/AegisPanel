<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  ConfirmDialog. Thin shadcn-vue wrapper around the existing
  Dialog primitives for the "yes/no destructive action" pattern.
  Replaces `window.confirm()` calls in views.

  Built on top of `Dialog` (radix-vue) rather than the separate
  `AlertDialog` primitive on purpose: in shadcn-vue the
  `AlertDialog` is just a styled variant of `Dialog` and our
  installed shadcn-vue surface only ships the `Dialog` family.
  Consumers get the same ARIA + focus-trap + ESC behaviour
  without dragging a second primitive tree through the bundle.

  Props:
    - open: boolean — v-model:open wired through to DialogRoot
    - title: string — required, DialogTitle text
    - description?: string — optional, DialogDescription text
    - confirmLabel?: string — default "Confirm"
    - cancelLabel?: string — default "Cancel"
    - variant?: 'default' | 'destructive' — default "default"
      Destructive maps to Button `variant="destructive"`.

  Emits:
    - (update:open, value: boolean) — fired by DialogRoot
    - confirm() — fired when the user clicks the confirm button
    - cancel() — fired when the user dismisses via the cancel
      button, overlay click, or ESC.

  The component does NOT auto-close on `confirm` — the parent
  owns the v-model:open state and decides when to flip it back
  to `false` (typically after the async action resolves). This
  keeps optimistic / rollback flows under the caller's control.
-->
<script setup lang="ts">
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import DialogClose from '@/components/ui/DialogClose.vue'
import DialogContent from '@/components/ui/DialogContent.vue'
import DialogDescription from '@/components/ui/DialogDescription.vue'
import DialogFooter from '@/components/ui/DialogFooter.vue'
import DialogHeader from '@/components/ui/DialogHeader.vue'
import DialogTitle from '@/components/ui/DialogTitle.vue'

type Variant = 'default' | 'destructive'

const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    description?: string
    confirmLabel?: string
    cancelLabel?: string
    variant?: Variant
  }>(),
  {
    confirmLabel: 'Confirm',
    cancelLabel: 'Cancel',
    variant: 'default',
  },
)

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'confirm'): void
  (e: 'cancel'): void
}>()

function onOpenChange(value: boolean): void {
  emit('update:open', value)
  if (!value) {
    // ESC, overlay click, or the cancel button — any non-confirm
    // dismissal is a cancel from the caller's perspective.
    emit('cancel')
  }
}
</script>

<template>
  <Dialog
    :open="props.open"
    @update:open="onOpenChange"
  >
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ props.title }}</DialogTitle>
        <DialogDescription v-if="props.description">
          {{ props.description }}
        </DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <DialogClose as-child>
          <Button
            type="button"
            variant="outline"
          >
            {{ props.cancelLabel }}
          </Button>
        </DialogClose>
        <Button
          type="button"
          :variant="props.variant"
          @click="emit('confirm')"
        >
          {{ props.confirmLabel }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
