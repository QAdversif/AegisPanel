<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Tabs root. Wraps `TabsRoot` from
  radix-vue. Pair with TabsList, TabsTrigger and
  TabsContent.

  Default value (if uncontrolled) must be the
  string id of one of the TabsTrigger children.
-->
<script setup lang="ts">
import { TabsRoot, type TabsRootEmits, type TabsRootProps } from 'radix-vue'

const props = withDefaults(defineProps<TabsRootProps>(), {
  modelValue: undefined,
  defaultValue: undefined,
})

const emit = defineEmits<TabsRootEmits>()
</script>

<template>
  <TabsRoot
    v-bind="props"
    @update:model-value="(value) => emit('update:modelValue', value as string)"
  >
    <slot />
  </TabsRoot>
</template>
