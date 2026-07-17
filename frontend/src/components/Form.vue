<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue Form. A thin wrapper around
  vee-validate's submit handler + a `<form>` root
  with the Aegis rhythm. Pair with `useZodForm`
  in `<script setup>` of the page and slot
  `FormField`s as the body.

  Usage:

    const { handleSubmit, isSubmitting } = useZodForm({
      schema: nodeCreateSchema,
      onSubmit: async (values) => { ... },
    })

    <Form @submit="handleSubmit" :is-submitting="isSubmitting">
      <FormField name="name" label="Name" required>
        <template #default="{ id, value, onBlur, hasError }">
          <Input
            :id="id"
            :model-value="value"
            :class="hasError && 'border-destructive'"
            @update:model-value="(v) => setFieldValue('name', v)"
            @blur="onBlur"
          />
        </template>
      </FormField>
    </Form>
-->
<script setup lang="ts">
import { cn } from '@/lib/utils'

defineProps<{
  isSubmitting?: boolean
  class?: string
}>()

defineEmits<{
  submit: []
}>()
</script>

<template>
  <form
    :class="cn('flex flex-col gap-4', $props.class)"
    @submit.prevent="$emit('submit')"
  >
    <slot />
    <slot
      name="footer"
      :is-submitting="isSubmitting"
    />
  </form>
</template>
