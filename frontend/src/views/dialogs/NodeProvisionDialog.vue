<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  NodeProvisionDialog. v0.3.0 ships the BYO Node
  provision dialog for /api/v1/nodes/{id}/provision.
  Extracted from NodesView.vue in v0.9.x to keep the
  view's list / per-row action flows in separate,
  focused files. Self-contained: owns its own form
  state, the v0.8.x auth-method radio (key / password
  / stored), the wire-payload builder that XORs the
  auth fields before the Go API call, and the
  per-row context card.

  v0.8.x form surface:
    * ssh_user (text)
    * ssh_port (number, optional override)
    * authMethod radio:
        - 'key'      -> render ssh_private_key (PEM textarea)
        - 'password' -> render ssh_password (password input)
        - 'stored'   -> render nothing (the panel re-uses
                        its own encrypted key). Disabled for
                        state 'new' (no stored key yet).
    * tofu_policy (select: 'reject' | 'accept-and-append')
    * expected_fingerprint (text, required when
                             tofu_policy === 'reject')

  The default authMethod is decided from `props.node.state`:
    * 'offline' -> 'stored' (re-provision; the panel has
                  a key to re-use).
    * anything else (including 'new') -> 'key' (first
                  install; the operator pastes a PEM or
                  switches the radio to 'password').

  The wire-payload builder (in the onSubmit handler)
  mirrors the Go provisioner: `ssh_private_key` XOR
  `ssh_password`. The 'stored' path sends no auth
  fields at all and the Go side falls back to the
  panel's encrypted key.

  Lifecycle:
    * The parent sets `node` + flips `open` -> the
      dialog's watch hydrates / resets the form.
    * On submit success the dialog emits `provisioned`
      (with the row + the new state) and the parent
      closes its own `provisionOpen` ref, toasts, and
      refreshes the list. The dialog does not own the
      toast store or the list state.
    * On cancel / ESC / backdrop click the dialog emits
      `update:open` -> false and the parent flips its
      `provisionOpen` ref.
-->
<script setup lang="ts">
import { watch } from "vue";
import { useI18n } from "vue-i18n";
import { Server, KeyRound, KeySquare, Lock } from "lucide-vue-next";

import { provisionNode } from "@/api/services";
import { toApiError } from "@/api/client";
import type { Node } from "@/types";
import { nodeProvisionSchema, type NodeProvisionInput } from "@/schemas";
import { useZodForm } from "@/composables/useZodForm";
import { useToastStore } from "@/stores/toast";

import Button from "@/components/ui/Button.vue";
import Dialog from "@/components/ui/Dialog.vue";
import DialogContent from "@/components/ui/DialogContent.vue";
import DialogHeader from "@/components/ui/DialogHeader.vue";
import DialogTitle from "@/components/ui/DialogTitle.vue";
import DialogDescription from "@/components/ui/DialogDescription.vue";
import DialogFooter from "@/components/ui/DialogFooter.vue";
import DialogClose from "@/components/ui/DialogClose.vue";
import Form from "@/components/Form.vue";
import FormField from "@/components/FormField.vue";
import Input from "@/components/ui/Input.vue";
import Textarea from "@/components/ui/Textarea.vue";
import RadioGroup from "@/components/ui/RadioGroup.vue";
import RadioGroupItem from "@/components/ui/RadioGroupItem.vue";

const props = defineProps<{
  open: boolean;
  node: Node | null;
}>();

const emit = defineEmits<{
  "update:open": [value: boolean];
  provisioned: [node: Node, newState: string];
}>();

const { t } = useI18n();
const toast = useToastStore();

const provisionForm = useZodForm({
  schema: nodeProvisionSchema,
  initialValues: {
    authMethod: "key" as const,
    ssh_port: undefined,
    ssh_user: "",
    ssh_private_key: "",
    ssh_password: "",
    tofu_policy: "reject" as const,
    expected_fingerprint: "",
  } as NodeProvisionInput,
  onSubmit: async (values) => {
    if (!props.node) return;
    // v0.8.x: the UI's authMethod radio is a local
    // form state; the Go API's wire format is the
    // two-field XOR `ssh_private_key` /
    // `ssh_password`. Build the wire payload from
    // the auth method:
    //   'key'      -> { ssh_private_key }
    //   'password' -> { ssh_password }
    //   'stored'   -> {} (empty auth; the Go
    //                 provisioner falls back to the
    //                 encrypted panel key it stored
    //                 on the first install).
    const wirePayload: {
      ssh_private_key?: string;
      ssh_password?: string;
      ssh_port?: number;
      ssh_user?: string;
      tofu_policy?: "reject" | "accept-and-append";
      expected_fingerprint?: string;
    } = {
      ssh_port: values.ssh_port,
      ssh_user: values.ssh_user,
      tofu_policy: values.tofu_policy,
      expected_fingerprint: values.expected_fingerprint,
    };
    if (values.authMethod === "key") {
      wirePayload.ssh_private_key = values.ssh_private_key;
    } else if (values.authMethod === "password") {
      wirePayload.ssh_password = values.ssh_password;
    }
    // 'stored' path: no auth fields. The Go side
    // falls back to the encrypted panel key.
    try {
      const res = await provisionNode(
        props.node.id,
        wirePayload as Parameters<typeof provisionNode>[1],
      );
      emit("provisioned", props.node, res.new_state);
    } catch (error) {
      toast.add({
        title: t("nodes.provisionFailed"),
        description: toApiError(error).message,
        variant: "destructive",
      });
    }
  },
});

// Hydrate the form every time the dialog opens.
// v0.8.x: the auth-method radio default depends on
// the row's state — re-provisions (state 'offline')
// default to 'stored' (the panel re-uses its own
// key, the operator clicks submit with no input);
// first-time installs (state 'new') default to
// 'key' (the operator pastes a PEM or switches
// the radio to 'password').
watch(
  () => [props.open, props.node] as const,
  ([isOpen, node]) => {
    if (isOpen && node) {
      const defaultAuth = node.state === "offline" ? "stored" : "key";
      provisionForm.resetForm({
        values: {
          authMethod: defaultAuth,
          ssh_port: undefined,
          ssh_user: "",
          ssh_private_key: "",
          ssh_password: "",
          tofu_policy: "reject" as const,
          expected_fingerprint: "",
        } as NodeProvisionInput,
      });
    }
  },
  { immediate: true },
);

function onOpenChange(value: boolean): void {
  emit("update:open", value);
}
</script>

<template>
  <Dialog
    :open="open"
    @update:open="onOpenChange"
  >
    <DialogContent>
      <DialogHeader>
        <DialogTitle>
          <Server class="h-4 w-4 inline-block mr-2 align-text-bottom" />
          {{ t("nodes.provisionTitle") }}
        </DialogTitle>
        <DialogDescription>
          {{
            t("nodes.provisionDescription")
          }}
        </DialogDescription>
      </DialogHeader>
      <Form
        :is-submitting="provisionForm.isSubmitting.value"
        @submit="provisionForm.handleSubmit"
      >
        <p class="nodes__provision-target">
          <strong>{{ node?.name }}</strong>
          ({{ node?.address }}) —
          {{ node ? t(`nodes.states.${node.state}`) : "" }}
        </p>
        <FormField
          name="ssh_user"
          :label="t('nodes.sshUser')"
          :hint="t('nodes.sshUserHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Input
              :id="id"
              :model-value="value"
              :class="hasError && 'border-destructive'"
              placeholder="root"
              @update:model-value="
                (v: string) => provisionForm.setFieldValue('ssh_user', v)
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <FormField
          name="ssh_port"
          :label="t('nodes.sshPort')"
          :hint="t('nodes.sshPortHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Input
              :id="id"
              type="number"
              :model-value="value"
              :class="hasError && 'border-destructive'"
              placeholder="22"
              @update:model-value="
                (v: string) =>
                  provisionForm.setFieldValue(
                    'ssh_port',
                    v === '' ? undefined : Number(v),
                  )
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <!-- v0.8.x: auth-method radio. The three-way
             picker drives the conditional rendering
             of the key / password fields below, and
             the wire payload built in the onSubmit
             handler. The "stored" option is only
             available for re-provisions (state
             'offline'); for first-time installs (state
             'new') the radio is disabled because the
             panel has no key to re-use yet. -->
        <FormField
          name="authMethod"
          :label="t('nodes.authMethod')"
          required
          :hint="t('nodes.authMethodHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <RadioGroup
              :id="id"
              :model-value="(value as string) ?? 'key'"
              :aria-label="t('nodes.authMethod')"
              :class="hasError && 'rounded-md border border-destructive p-1'"
              @update:model-value="
                (v: string) => {
                  provisionForm.setFieldValue(
                    'authMethod',
                    v as 'key' | 'password' | 'stored',
                  );
                  provisionForm.setFieldValue('ssh_private_key', '');
                  provisionForm.setFieldValue('ssh_password', '');
                  onBlur();
                }
              "
            >
              <RadioGroupItem value="key">
                <KeySquare class="h-4 w-4" />
                <span>{{ t("nodes.authMethodKey") }}</span>
              </RadioGroupItem>
              <RadioGroupItem value="password">
                <Lock class="h-4 w-4" />
                <span>{{ t("nodes.authMethodPassword") }}</span>
              </RadioGroupItem>
              <RadioGroupItem
                value="stored"
                :disabled="node?.state !== 'offline'"
                :title="
                  node?.state !== 'offline'
                    ? t('nodes.authMethodStoredDisabledTitle')
                    : ''
                "
              >
                <KeyRound class="h-4 w-4" />
                <span>{{ t("nodes.authMethodStored") }}</span>
              </RadioGroupItem>
            </RadioGroup>
          </template>
        </FormField>
        <FormField
          v-if="provisionForm.values.authMethod === 'key'"
          name="ssh_private_key"
          :label="t('nodes.sshPrivateKey')"
          required
          :hint="t('nodes.sshPrivateKeyHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Textarea
              :id="id"
              :model-value="String(value ?? '')"
              :rows="8"
              :class="hasError && 'border-destructive'"
              spellcheck="false"
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              @update:model-value="
                (v: string) =>
                  provisionForm.setFieldValue('ssh_private_key', v)
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <FormField
          v-if="provisionForm.values.authMethod === 'password'"
          name="ssh_password"
          :label="t('nodes.sshPassword')"
          required
          :hint="t('nodes.sshPasswordHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Input
              :id="id"
              type="password"
              autocomplete="off"
              :model-value="String(value ?? '')"
              :class="hasError && 'border-destructive'"
              spellcheck="false"
              @update:model-value="
                (v: string) => provisionForm.setFieldValue('ssh_password', v)
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <FormField
          name="tofu_policy"
          :label="t('nodes.tofuPolicy')"
          :hint="t('nodes.tofuPolicyHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <select
              :id="id"
              :value="value"
              :class="['nodes__select', hasError && 'border-destructive']"
              @change="
                (event: Event) => {
                  const v = (event.target as HTMLSelectElement).value;
                  provisionForm.setFieldValue(
                    'tofu_policy',
                    v === ''
                      ? undefined
                      : (v as 'reject' | 'accept-and-append'),
                  );
                  onBlur();
                }
              "
            >
              <option value="reject">
                {{ t("nodes.tofuReject") }}
              </option>
              <option value="accept-and-append">
                {{ t("nodes.tofuAcceptAndAppend") }}
              </option>
            </select>
          </template>
        </FormField>
        <FormField
          name="expected_fingerprint"
          :label="t('nodes.expectedFingerprint')"
          :hint="t('nodes.expectedFingerprintHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Input
              :id="id"
              :model-value="value"
              :class="hasError && 'border-destructive'"
              placeholder="SHA256:abc123..."
              @update:model-value="
                (v: string) =>
                  provisionForm.setFieldValue('expected_fingerprint', v)
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <DialogFooter>
          <DialogClose>
            <Button
              type="button"
              variant="outline"
              :disabled="provisionForm.isSubmitting.value"
            >
              {{ t("common.cancel") }}
            </Button>
          </DialogClose>
          <Button
            type="submit"
            :disabled="provisionForm.isSubmitting.value"
          >
            <Server class="h-4 w-4 mr-2" />
            {{ t("nodes.provision") }}
          </Button>
        </DialogFooter>
      </Form>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
/* v0.9.x: colocation with the dialog markup. The
   same `.nodes__provision-target` style still lives
   in NodesView.vue for the rotate / refresh / inspect
   dialogs (which are not extracted in this PR). */
.nodes__provision-target {
  margin: 0 0 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.375rem;
  background: hsl(var(--muted));
  font-size: 0.875rem;
}

.nodes__select {
  display: block;
  width: 100%;
  border: 1px solid hsl(var(--input));
  border-radius: 0.375rem;
  background: transparent;
  padding: 0.5rem 0.75rem;
  font-size: 0.875rem;
}

.nodes__select:focus-visible {
  outline: none;
  box-shadow: 0 0 0 2px hsl(var(--ring));
  border-color: hsl(var(--ring));
}
</style>
