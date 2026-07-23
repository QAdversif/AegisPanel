<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  SettingsView. v0.2.0 ships the panel-wide
  sub_path rotation UI; the rest of the operator
  surface (audit log, backup, profile) lands in
  later milestones (KNOWN_LIMITATIONS.md).

  v0.2.0 scope:

    * Show the active sub_path row (or "default"
      when the panel is on the empty default).
    * Rotate to a fresh random sub_path.
    * Rotate to an operator-supplied sub_path
      (validated client-side too: 4-64 chars,
      [a-z0-9-] charset).
    * Reset to the default empty sub_path.

  The v0.1.0 placeholder badge is gone; the route
  is now functional and the nav item is enabled.
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RotateCw, RotateCcw, ShieldAlert, Sparkles } from 'lucide-vue-next'

import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import {
  getActivePanelPath,
  resetPanelPath,
  rotatePanelPath,
  rotatePanelPathTo,
} from '@/api/services'
import type { PanelPathConfig } from '@/types'

import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import CardHeader from '@/components/ui/CardHeader.vue'
import CardTitle from '@/components/ui/CardTitle.vue'
import CardDescription from '@/components/ui/CardDescription.vue'
import CardContent from '@/components/ui/CardContent.vue'
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'
import Input from '@/components/ui/Input.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import Badge from '@/components/ui/Badge.vue'
import { useZodForm } from '@/composables/useZodForm'
import { z } from 'zod'

const { t } = useI18n()
const toast = useToastStore()

const active = ref<PanelPathConfig | null>(null)
const loading = ref(false)
const rotating = ref(false)
const resetting = ref(false)
const explicitPath = ref('')

const isDefault = computed(() => !active.value?.subPath)

async function refresh(): Promise<void> {
  loading.value = true
  try {
    active.value = await getActivePanelPath()
  } catch (error) {
    toast.add({
      title: t('settings.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

async function rotateRandom(): Promise<void> {
  rotating.value = true
  try {
    active.value = await rotatePanelPath()
    toast.add({ title: t('settings.rotated'), variant: 'success' })
  } catch (error) {
    toast.add({
      title: t('settings.rotateFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    rotating.value = false
  }
}

async function reset(): Promise<void> {
  if (!window.confirm(t('settings.confirmReset'))) return
  resetting.value = true
  try {
    active.value = await resetPanelPath()
    toast.add({ title: t('settings.reset'), variant: 'success' })
  } catch (error) {
    toast.add({
      title: t('settings.resetFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    resetting.value = false
  }
}

const explicitSchema = z.object({
  subPath: z
    .string()
    .min(4, t('settings.pathMinLength'))
    .max(64, t('settings.pathMaxLength'))
    .regex(/^[a-z0-9-]+$/, t('settings.pathInvalidChars')),
})

const explicitForm = useZodForm({
  schema: explicitSchema,
  initialValues: { subPath: '' },
  onSubmit: async (values) => {
    try {
      active.value = await rotatePanelPathTo({ subPath: values.subPath })
      explicitForm.resetForm({ values: { subPath: '' } })
      toast.add({ title: t('settings.rotated'), variant: 'success' })
    } catch (error) {
      toast.add({
        title: t('settings.rotateFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

onMounted(() => {
  void refresh()
})
</script>

<template>
  <section class="settings">
    <header class="settings__header">
      <div>
        <h1 class="settings__title">
          {{ t('settings.title') }}
        </h1>
        <p class="settings__subtitle">
          {{ t('settings.subtitle') }}
        </p>
      </div>
    </header>

    <!-- Active sub_path -->
    <Card>
      <CardHeader>
        <CardTitle>{{ t('settings.activePath') }}</CardTitle>
        <CardDescription>
          {{ t('settings.activePathDescription') }}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div
          v-if="loading"
          class="settings__stack"
        >
          <Skeleton class="h-4 w-1/2" />
          <Skeleton class="h-4 w-1/3" />
        </div>
        <div
          v-else-if="active"
          class="settings__stack"
        >
          <div class="settings__row">
            <code class="settings__path">{{ active.subPath || t('settings.defaultPath') }}</code>
            <Badge
              v-if="isDefault"
              variant="secondary"
            >
              {{ t('settings.default') }}
            </Badge>
          </div>
          <small class="settings__meta">
            {{ t('settings.lastRotated') }}:
            {{ new Date(active.createdAt).toLocaleString() }}
          </small>
        </div>
      </CardContent>
    </Card>

    <!-- Rotate actions -->
    <Card>
      <CardHeader>
        <CardTitle>{{ t('settings.rotateTitle') }}</CardTitle>
        <CardDescription>{{ t('settings.rotateDescription') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <div class="settings__actions">
          <Button
            variant="default"
            :disabled="rotating || resetting"
            @click="rotateRandom"
          >
            <Sparkles class="h-4 w-4" />
            {{ t('settings.rotateRandom') }}
          </Button>
          <Button
            variant="outline"
            :disabled="resetting || rotating || isDefault"
            @click="reset"
          >
            <RotateCcw class="h-4 w-4" />
            {{ t('settings.resetToDefault') }}
          </Button>
        </div>
      </CardContent>
    </Card>

    <!-- Explicit rotate-to -->
    <Card>
      <CardHeader>
        <CardTitle>{{ t('settings.explicitTitle') }}</CardTitle>
        <CardDescription>{{ t('settings.explicitDescription') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <Form
          :is-submitting="explicitForm.isSubmitting.value"
          @submit="explicitForm.handleSubmit"
        >
          <FormField
            name="subPath"
            :label="t('settings.explicitLabel')"
            required
            :hint="t('settings.explicitHint')"
          >
            <template #default="{ id, onBlur, hasError }">
              <Input
                :id="id"
                v-model="explicitPath"
                :class="hasError && 'border-destructive'"
                placeholder="aegis-prod-2026"
                @update:model-value="(v: string) => explicitForm.setFieldValue('subPath', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <Button
            type="submit"
            :disabled="explicitForm.isSubmitting.value"
            class="w-fit"
          >
            <RotateCw class="h-4 w-4" />
            {{ t('settings.explicitSubmit') }}
          </Button>
        </Form>
      </CardContent>
    </Card>

    <!-- Warning -->
    <Card class="settings__warning">
      <CardHeader>
        <CardTitle>
          <span class="settings__warning-title">
            <ShieldAlert class="h-4 w-4" />
            {{ t('settings.warningTitle') }}
          </span>
        </CardTitle>
        <CardDescription>{{ t('settings.warningDescription') }}</CardDescription>
      </CardHeader>
    </Card>
  </section>
</template>

<style scoped>
.settings {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.settings__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.settings__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}

.settings__stack {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.settings__row {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
}

.settings__path {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 1rem;
  background: hsl(var(--muted));
  padding: 0.25rem 0.625rem;
  border-radius: 0.375rem;
  word-break: break-all;
}

.settings__meta {
  color: hsl(var(--muted-foreground));
}

.settings__actions {
  display: flex;
  gap: 0.75rem;
  flex-wrap: wrap;
}

.settings__warning {
  border-color: hsl(var(--destructive) / 0.3);
}

.settings__warning-title {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  color: hsl(var(--destructive));
}
</style>
