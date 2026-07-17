<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  SubscriptionView. v0.1.0 ships a read-only
  "operator-pastes-a-sub-token-to-preview" tool.
  Real per-user subscription management lands
  with the user module in v0.2.

  v0.1.0 also surfaces the active panel sub_path
  (read from /api/v1/panelcfg when the backend
  exposes the handler; otherwise shown as
  "Unknown"). The rotate-sub-path action lands
  with the panelcfg UI in v0.2.
-->
<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Eye } from 'lucide-vue-next'

import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import { fetchSubscription, type RenderedSubscription } from '@/api/services'

import Card from '@/components/ui/Card.vue'
import CardHeader from '@/components/ui/CardHeader.vue'
import CardTitle from '@/components/ui/CardTitle.vue'
import CardDescription from '@/components/ui/CardDescription.vue'
import CardContent from '@/components/ui/CardContent.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Label from '@/components/ui/Label.vue'
import Select from '@/components/ui/Select.vue'
import SelectTrigger from '@/components/ui/SelectTrigger.vue'
import SelectValue from '@/components/ui/SelectValue.vue'
import SelectContent from '@/components/ui/SelectContent.vue'
import SelectItem from '@/components/ui/SelectItem.vue'
import Skeleton from '@/components/ui/Skeleton.vue'

const { t } = useI18n()
const toast = useToastStore()

const token = ref('')
const format = ref<RenderedSubscription['format']>('sing-box')
const result = ref<RenderedSubscription | null>(null)
const loading = ref(false)

async function preview(): Promise<void> {
  if (!token.value) return
  loading.value = true
  result.value = null
  try {
    result.value = await fetchSubscription(token.value, format.value)
  } catch (error) {
    toast.add({
      title: t('subscription.fetchFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <section class="subscription">
    <header class="subscription__header">
      <div>
        <h1 class="subscription__title">
          {{ t('subscription.title') }}
        </h1>
        <p class="subscription__subtitle">
          {{ t('subscription.subtitle') }}
        </p>
      </div>
    </header>

    <Card>
      <CardHeader>
        <CardTitle>{{ t('subscription.previewTitle') }}</CardTitle>
        <CardDescription>{{ t('subscription.previewDescription') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <form
          class="subscription__form"
          @submit.prevent="preview"
        >
          <div class="subscription__field">
            <Label for="sub-token">{{ t('subscription.token') }}</Label>
            <Input
              id="sub-token"
              v-model="token"
              :placeholder="t('subscription.tokenPlaceholder')"
            />
          </div>
          <div class="subscription__field">
            <Label>{{ t('subscription.format') }}</Label>
            <Select v-model="format">
              <SelectTrigger class="w-48">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="sing-box">
                  sing-box
                </SelectItem>
                <SelectItem value="clash">
                  Clash
                </SelectItem>
                <SelectItem value="base64">
                  base64
                </SelectItem>
                <SelectItem value="html">
                  HTML
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <Button
            type="submit"
            :disabled="loading || !token"
          >
            <Eye class="h-4 w-4" />
            {{ t('subscription.preview') }}
          </Button>
        </form>
      </CardContent>
    </Card>

    <Card v-if="loading">
      <CardHeader>
        <CardTitle>{{ t('subscription.rendering') }}</CardTitle>
      </CardHeader>
      <CardContent>
        <Skeleton class="h-4 w-full" />
        <Skeleton class="mt-2 h-4 w-5/6" />
        <Skeleton class="mt-2 h-4 w-2/3" />
      </CardContent>
    </Card>

    <Card v-else-if="result">
      <CardHeader>
        <CardTitle>{{ t('subscription.resultTitle', { format: result.format }) }}</CardTitle>
        <CardDescription>{{ t('subscription.resultDescription') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <pre class="subscription__payload">{{ result.body }}</pre>
        <img
          v-if="result.qrDataUrl"
          :src="result.qrDataUrl"
          :alt="t('subscription.qrAlt')"
          class="subscription__qr"
        >
      </CardContent>
    </Card>
  </section>
</template>

<style scoped>
.subscription {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.subscription__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.subscription__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}

.subscription__form {
  display: flex;
  align-items: flex-end;
  gap: 1rem;
  flex-wrap: wrap;
}

.subscription__field {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
  flex: 1 1 16rem;
}

.subscription__payload {
  margin: 0;
  padding: 1rem;
  background: hsl(var(--muted));
  border-radius: 0.375rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.75rem;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 32rem;
  overflow: auto;
}

.subscription__qr {
  display: block;
  margin-top: 1rem;
  width: 12rem;
  height: 12rem;
  border-radius: 0.375rem;
  border: 1px solid hsl(var(--border));
}
</style>
