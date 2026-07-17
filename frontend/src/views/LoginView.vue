<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  LoginView. v0.1.0 ships a single username +
  password form against the /api/v1/auth/login
  endpoint. The Aegis default admin is provisioned
  by the backend's first-run migration; the
  credentials are surfaced in the backend README
  for the dev environment.

  The view bypasses AppLayout and renders inside
  the root <RouterView>, so the chrome (sidebar,
  topbar) is not shown. This is intentional — a
  half-chromed login screen reads as broken.
-->
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { LogIn } from 'lucide-vue-next'
import { z } from 'zod'

import { useAuthStore } from '@/stores/auth'
import { useToastStore } from '@/stores/toast'
import { toApiError } from '@/api/client'

import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import CardHeader from '@/components/ui/CardHeader.vue'
import CardTitle from '@/components/ui/CardTitle.vue'
import CardDescription from '@/components/ui/CardDescription.vue'
import CardContent from '@/components/ui/CardContent.vue'
import Form from '@/components/Form.vue'
import FormField from '@/components/FormField.vue'
import Input from '@/components/ui/Input.vue'
import { useZodForm } from '@/composables/useZodForm'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const toast = useToastStore()

const loginSchema = z.object({
  username: z.string().min(1, t('login.usernameRequired')),
  password: z.string().min(1, t('login.passwordRequired')),
})

const { handleSubmit, isSubmitting, setFieldValue } = useZodForm({
  schema: loginSchema,
  initialValues: { username: '', password: '' },
  onSubmit: async (values) => {
    try {
      await auth.login(values.username, values.password)
      toast.add({
        title: t('login.welcome'),
        description: values.username,
        variant: 'success',
      })
      const redirect = (route.query.redirect as string | undefined) ?? '/'
      await router.replace(redirect)
    } catch (error) {
      const apiErr = toApiError(error)
      toast.add({
        title: t('login.failed'),
        description: apiErr.message,
        variant: 'destructive',
      })
    }
  },
})
</script>

<template>
  <div class="login">
    <Card class="login__card">
      <CardHeader>
        <CardTitle class="login__brand">
          <span
            class="login__logo"
            aria-hidden="true"
          >⛨</span>
          {{ t('login.title') }}
        </CardTitle>
        <CardDescription>{{ t('login.subtitle') }}</CardDescription>
      </CardHeader>
      <CardContent>
        <Form
          class="login__form"
          :is-submitting="isSubmitting"
          @submit="handleSubmit"
        >
          <FormField
            name="username"
            :label="t('login.username')"
            required
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                :class="hasError && 'border-destructive'"
                autocomplete="username"
                :placeholder="t('login.username')"
                @update:model-value="(v: string) => setFieldValue('username', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>

          <FormField
            name="password"
            :label="t('login.password')"
            required
          >
            <template #default="{ id, value, onBlur, hasError }">
              <Input
                :id="id"
                :model-value="value"
                type="password"
                :class="hasError && 'border-destructive'"
                autocomplete="current-password"
                :placeholder="t('login.password')"
                @update:model-value="(v: string) => setFieldValue('password', v)"
                @blur="onBlur"
              />
            </template>
          </FormField>

          <Button
            type="submit"
            class="w-full"
            :disabled="isSubmitting"
          >
            <LogIn class="h-4 w-4" />
            {{ t('login.submit') }}
          </Button>
        </Form>
      </CardContent>
    </Card>
  </div>
</template>

<style scoped>
.login {
  display: flex;
  min-height: 100vh;
  align-items: center;
  justify-content: center;
  padding: 1.5rem;
  background: hsl(var(--background));
}

.login__card {
  width: 100%;
  max-width: 24rem;
}

.login__brand {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.login__logo {
  font-size: 1.25rem;
  color: hsl(var(--primary));
}

.login__form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
</style>
