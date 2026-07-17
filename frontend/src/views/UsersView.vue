<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  UsersView placeholder. The full CRUD lands in
  v0.2 alongside the user module backend; v0.1.0
  ships the route + this stub so the nav entry
  resolves and the team has a single tracked
  place to drop the real implementation.

  v0.2 scope: list + create (per-plan) + edit
  (status / plan / expiry / device limit) +
  delete (soft, via status=deleted). The per-user
  subscription URL is rendered via the same
  subscription service the public endpoint uses.
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
  <section class="users-placeholder">
    <header class="users-placeholder__header">
      <h1 class="users-placeholder__title">
        {{ t('users.title') }}
      </h1>
      <p class="users-placeholder__subtitle">
        {{ t('users.subtitle') }}
      </p>
    </header>

    <Card>
      <CardHeader>
        <CardTitle>{{ t('users.placeholderTitle') }}</CardTitle>
        <CardDescription>{{ t('users.placeholderDescription') }}</CardDescription>
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
.users-placeholder {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.users-placeholder__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.users-placeholder__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}
</style>
