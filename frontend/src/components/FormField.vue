<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  shadcn-vue FormField. A self-contained
  label + input + error block, bound to a
  vee-validate field by `name`.

  The `name` prop must match the field name in the
  parent `useZodForm` schema. The component:

    1. Renders a Label whose `for` matches the
       generated input id.
    2. Renders the slot for the actual control
       (Input, Textarea, Select, …). The slot is
       passed `field` and `errorMessage` props
       so the consumer does not have to re-wire
       `useField` itself.
    3. Renders the error message under the control
       (uses FormFieldError).
    4. Renders a hint under the control when the
       `hint` prop is set (optional).

  The pattern is the shadcn-vue form convention:
  the form is a typed shape, the field is a named
  binding, the control is whatever fits.
-->
<script setup lang="ts">
import { computed, useId } from 'vue'
import { useField } from 'vee-validate'

import FormFieldError from './FormFieldError.vue'
import Label from './ui/Label.vue'
import { cn } from '@/lib/utils'

const props = withDefaults(
  defineProps<{
    name: string
    label: string
    hint?: string
    required?: boolean
    class?: string
  }>(),
  {
    hint: '',
    required: false,
    class: undefined,
  },
)

const generatedId = useId()
const fieldId = computed(() => `field-${props.name}-${generatedId}`)

const { value, errorMessage, handleBlur } = useField<string | number | boolean>(
  () => props.name,
  undefined,
  { syncVModel: false },
)

const hasError = computed(() => Boolean(errorMessage.value))
</script>

<template>
  <div :class="cn('flex flex-col gap-1.5', props.class)">
    <Label :for="fieldId">
      {{ props.label }}
      <span
        v-if="props.required"
        class="text-destructive"
        aria-hidden="true"
      >*</span>
    </Label>
    <slot
      :id="fieldId"
      :name="props.name"
      :value="value"
      :on-blur="handleBlur"
      :has-error="hasError"
      :error-message="errorMessage"
    />
    <small
      v-if="props.hint && !hasError"
      class="text-xs text-muted-foreground"
    >
      {{ props.hint }}
    </small>
    <FormFieldError :name="props.name" />
  </div>
</template>
