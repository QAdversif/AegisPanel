<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  ProfileView. v0.2.0 ships the operator profile
  page + the change-password dialog. The page is
  reachable from the topbar user menu (the
  "profileSoon" placeholder is replaced with a real
  link in AppLayout.vue).

  The view is read-mostly: it shows the caller's
  identity (id, username, scopes) and offers a
  "change password" button that opens the
  change-password dialog. The dialog is gated on
  the current + new password fields and calls
  `changePassword(...)` from the auth service.

  The change-password endpoint also writes a row
  to the audit log; v0.2.0 ships the read surface
  only so the entry is not yet visible in the
  AuditsView table. v0.3 wires the call-sites
  for the other mutating handlers.
-->
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { KeyRound, RefreshCw, ShieldCheck, User } from 'lucide-vue-next'
import { z } from 'zod'

import { changePassword, me } from '@/api/services'
import { useAuthStore } from '@/stores/auth'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'
import { useZodForm } from '@/composables/useZodForm'

import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import CardHeader from '@/components/ui/CardHeader.vue'
import CardTitle from '@/components/ui/CardTitle.vue'
import CardDescription from '@/components/ui/CardDescription.vue'
import CardContent from '@/components/ui/CardContent.vue'
import Dialog from '@/components/ui/Dialog.vue'
import DialogContent from '@/components/ui/DialogContent.vue'
import DialogHeader from '@/components/ui/DialogHeader.vue'
import DialogTitle from '@/components/ui/DialogTitle.vue'
import DialogDescription from '@/components/ui/DialogDescription.vue'
import DialogFooter from '@/components/ui/DialogFooter.vue'
import DialogClose from '@/components/ui/DialogClose.vue'
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'
import Input from '@/components/ui/Input.vue'

const { t } = useI18n()
const auth = useAuthStore()
const toast = useToastStore()

const pwdOpen = ref(false)
const loading = ref(false)
const me_ = ref<{ userId: string; username: string; scopes: string[] } | null>(null)

async function refreshMe(): Promise<void> {
  loading.value = true
  try {
    me_.value = await me()
  } catch (error) {
    toast.add({
      title: t('profile.loadFailed'),
      description: toApiError(error).message,
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void refreshMe()
})

const pwdSchema = z
  .object({
    current_password: z.string().min(1, t('profile.currentRequired')),
    new_password: z.string().min(8, t('profile.newPasswordMinLength')),
    confirm_password: z.string().min(8, t('profile.newPasswordMinLength')),
  })
  .refine((data) => data.new_password === data.confirm_password, {
    message: t('profile.passwordsDoNotMatch'),
    path: ['confirm_password'],
  })
  .refine((data) => data.new_password !== data.current_password, {
    message: t('profile.newMustDiffer'),
    path: ['new_password'],
  })

const pwdForm = useZodForm({
  schema: pwdSchema,
  initialValues: {
    current_password: '',
    new_password: '',
    confirm_password: '',
  },
  onSubmit: async (values) => {
    try {
      await changePassword({
        current_password: values.current_password,
        new_password: values.new_password,
      })
      pwdOpen.value = false
      pwdForm.resetForm({ values: { current_password: '', new_password: '', confirm_password: '' } })
      toast.add({ title: t('profile.passwordChanged'), variant: 'success' })
      // Refresh the topbar identity. The MeResponse
      // shape is unchanged on rotation, but the
      // store keeps the username + scopes cached
      // and a re-read is cheap.
      await auth.refreshMe()
      await refreshMe()
    } catch (error) {
      toast.add({
        title: t('profile.changeFailed'),
        description: toApiError(error).message,
        variant: 'destructive',
      })
    }
  },
})

const scopeLabel = (scope: string): string => t(`profile.scopes.${scope}`, scope)

const sortedScopes = computed(() => {
  const s = me_.value?.scopes ?? []
  return [...s].sort()
})
</script>

<template>
  <section class="profile">
    <header class="profile__header">
      <div>
        <h1 class="profile__title">{{ t('profile.title') }}</h1>
        <p class="profile__subtitle">{{ t('profile.subtitle') }}</p>
      </div>
    </header>

    <Card>
      <CardHeader>
        <CardTitle>
          <span class="profile__card-title">
            <User class="h-4 w-4" />
            {{ t('profile.identity') }}
          </span>
        </CardTitle>
        <CardDescription>{{ t('profile.identityDescription') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <div v-if="loading" class="profile__loading">{{ t('profile.loading') }}</div>
        <dl v-else-if="me_" class="profile__meta-list">
          <div class="profile__meta-row">
            <dt>{{ t('profile.username') }}</dt>
            <dd class="profile__mono">{{ me_.username }}</dd>
          </div>
          <div class="profile__meta-row">
            <dt>{{ t('profile.userId') }}</dt>
            <dd class="profile__mono">{{ me_.userId }}</dd>
          </div>
          <div class="profile__meta-row">
            <dt>{{ t('profile.scopesLabel') }}</dt>
            <dd>
              <div class="profile__scopes">
                <Badge
                  v-for="scope in sortedScopes"
                  :key="scope"
                  variant="secondary"
                >
                  {{ scopeLabel(scope) }}
                </Badge>
              </div>
            </dd>
          </div>
        </dl>
      </CardContent>
    </Card>

    <Card>
      <CardHeader>
        <CardTitle>
          <span class="profile__card-title">
            <ShieldCheck class="h-4 w-4" />
            {{ t('profile.security') }}
          </span>
        </CardTitle>
        <CardDescription>{{ t('profile.securityDescription') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <div class="profile__actions">
          <Button @click="pwdOpen = true">
            <KeyRound class="h-4 w-4" />
            {{ t('profile.changePassword') }}
          </Button>
        </div>
      </CardContent>
    </Card>

    <!-- Change-password dialog -->
    <Dialog v-model:open="pwdOpen">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('profile.changePasswordTitle') }}</DialogTitle>
          <DialogDescription>{{ t('profile.changePasswordDescription') }}</DialogDescription>
        </DialogHeader>
        <Form
          :is-submitting="pwdForm.isSubmitting.value"
          @submit="pwdForm.handleSubmit"
        >
          <FormField
            name="current_password"
            :label="t('profile.currentPassword')"
            required
            :hint="t('profile.currentPasswordHint')"
          >
            <template #default="{ id, onBlur, hasError }">
              <Input
                :id="id"
                type="password"
                autocomplete="current-password"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => pwdForm.setFieldValue('current_password', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="new_password"
            :label="t('profile.newPassword')"
            required
            :hint="t('profile.newPasswordHint')"
          >
            <template #default="{ id, onBlur, hasError }">
              <Input
                :id="id"
                type="password"
                autocomplete="new-password"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => pwdForm.setFieldValue('new_password', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <FormField
            name="confirm_password"
            :label="t('profile.confirmPassword')"
            required
          >
            <template #default="{ id, onBlur, hasError }">
              <Input
                :id="id"
                type="password"
                autocomplete="new-password"
                :class="hasError && 'border-destructive'"
                @update:model-value="(v: string) => pwdForm.setFieldValue('confirm_password', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>
          <DialogFooter>
            <DialogClose>
              <Button type="button" variant="outline">
                <RefreshCw class="h-4 w-4" />
                {{ t('common.cancel') }}
              </Button>
            </DialogClose>
            <Button
              type="submit"
              :disabled="pwdForm.isSubmitting.value"
            >
              {{ t('profile.changePasswordSubmit') }}
            </Button>
          </DialogFooter>
        </Form>
      </DialogContent>
    </Dialog>
  </section>
</template>

<style scoped>
.profile {
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
}

.profile__title {
  margin: 0;
  font-size: 1.5rem;
  font-weight: 600;
}

.profile__subtitle {
  margin: 0.25rem 0 0;
  color: hsl(var(--muted-foreground));
}

.profile__card-title {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
}

.profile__loading {
  color: hsl(var(--muted-foreground));
  font-size: 0.875rem;
}

.profile__meta-list {
  margin: 0;
  display: grid;
  grid-template-columns: 8rem 1fr;
  gap: 0.5rem 1rem;
  font-size: 0.875rem;
}

.profile__meta-row {
  display: contents;
}

.profile__meta-row dt {
  color: hsl(var(--muted-foreground));
}

.profile__meta-row dd {
  margin: 0;
}

.profile__mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.8125rem;
}

.profile__scopes {
  display: flex;
  flex-wrap: wrap;
  gap: 0.375rem;
}

.profile__actions {
  display: flex;
  gap: 0.5rem;
}
</style>
