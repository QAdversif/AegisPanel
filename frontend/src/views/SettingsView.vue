<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  SettingsView placeholder. Operator-level
  settings (panel sub-path rotation, backup,
  audit log, operator profile) land in v0.2.
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import Card from '@/components/ui/Card.vue'
import CardHeader from '@/components/ui/CardHeader.vue'
import CardTitle from '@/components/ui/CardTitle.vue'
import CardDescription from '@/components/ui/CardDescription.vue'
import CardContent from '@/components/ui/CardContent.vue'
import Badge from '@/components/ui/Badge.vue'

const { t } = useI18n()
</script>

<template>
  <section class="settings-placeholder">
    <header class="settings-placeholder__header">
      <h1 class="settings-placeholder__title">
        {{ t('settings.title') }}
      </h1>
      <p class="settings-placeholder__subtitle">
        {{ t('settings.subtitle') }}
      </p>
    </header>

    <Card>
      <CardHeader>
        <CardTitle>{{ t('settings.placeholderTitle') }}</CardTitle>
        <CardDescription>{{ t('settings.placeholderDescription') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <Badge variant="warning">
          v0.2
        </Badge>
      </CardContent>
    </Card>
  </section>
</template>

<style scoped>
.settings-placeholder {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.settings-placeholder__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.settings-placeholder__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}
</style>
