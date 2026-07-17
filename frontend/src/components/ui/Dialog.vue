<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Dialog root. Wraps `DialogRoot` from
  radix-vue with the Aegis look. The actual modal
  panel is `DialogContent`; this component is only
  the open/close state owner.
-->
<script setup lang="ts">
import { DialogRoot, type DialogRootEmits, type DialogRootProps } from 'radix-vue'

const props = withDefaults(defineProps<DialogRootProps>(), {
  open: undefined,
  defaultOpen: false,
})

const emit = defineEmits<DialogRootEmits>()
</script>

<template>
  <DialogRoot
    v-bind="props"
    @update:open="(value) => emit('update:open', value)"
  >
    <slot />
  </DialogRoot>
</template>
