<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  NodeRotateDialog. v0.8.4 ships the rotate-panel-key
  dialog for /api/v1/nodes/{id}/rotate-panel-key.
  Extracted from NodesView.vue in v0.9.x to keep the
  view's per-action dialogs in separate, focused
  files. Self-contained: owns its own form state, the
  SSH credentials form (ssh_user / ssh_port /
  ssh_private_key), the wire-payload builder, and the
  post-submit success card (public key line + SHA256
  fingerprint). The dialog does NOT own the toast
  store or the list state — the view does.

  v0.8.x form surface:
    * ssh_user (text, optional override; the panel
                falls back to the service-wide
                AgentSSHUser if empty)
    * ssh_port (number, optional override; the panel
                falls back to 22 if empty)
    * ssh_private_key (PEM textarea, required — the
                panel SSHes in with the operator's
                existing key, generates a fresh
                ed25519 pair, appends the public half
                to authorized_keys, and stores the
                encrypted private half on the panel)

  v0.8.x success surface (after the 200 lands):
    * public_key_line (read-only Textarea) — the line
                the operator pastes into
                `ssh-add` / out-of-band verification
    * fingerprint (read-only Input) — SHA256:...
                fingerprint of the new public key; the
                operator can compare with `ssh-keygen
                -lf` on the node

  Lifecycle:
    * The parent sets `node` + flips `open` -> the
      dialog's watcher hydrates / resets the form and
      clears any stale `rotationResult` from a
      previous open. The dialog is a controlled
      component: it never opens / closes itself
      except via `update:open` from the Dialog UI.
    * On submit success the dialog stashes the
      response in `rotationResult` so the success
      card can render the public key line +
      fingerprint side-by-side. The dialog stays
      open (rotateOpen stays true) so the operator
      can copy the fingerprint before closing. The
      form is hidden (`v-if`) so the submit button
      is not visible alongside the success card.
      The dialog emits `rotated` (with the row) so
      the parent can toast.
    * On cancel / ESC / backdrop click / Close
      button the dialog emits `update:open` ->
      false and the parent flips its `rotateOpen`
      ref. The `rotating` row pointer is the
      parent's responsibility.

  List refresh: the row's state machine did not
  change on rotation (only the encrypted
  ssh_private_key_ciphertext column changed, which
  is not in the wire shape of Node). The parent
  does NOT refresh the list on `rotated` — the
  success card is the only post-rotation surface.
-->
<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { KeyRound, RefreshCw } from "lucide-vue-next";

import { rotateNodePanelKey } from "@/api/services";
import { toApiError } from "@/api/client";
import type { Node, NodeRotatePanelKeyResponse } from "@/types";
import {
  nodeRotatePanelKeySchema,
  type NodeRotatePanelKeyInput,
} from "@/schemas";
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

const props = defineProps<{
  open: boolean;
  node: Node | null;
}>();

const emit = defineEmits<{
  "update:open": [value: boolean];
  rotated: [node: Node];
}>();

const { t } = useI18n();
const toast = useToastStore();

const rotationResult = ref<NodeRotatePanelKeyResponse | null>(null);

const rotateForm = useZodForm({
  schema: nodeRotatePanelKeySchema,
  initialValues: {
    ssh_private_key: "",
    ssh_port: undefined,
    ssh_user: "",
  } as NodeRotatePanelKeyInput,
  onSubmit: async (values) => {
    if (!props.node) return;
    try {
      const res = await rotateNodePanelKey(props.node.id, {
        ssh_private_key: values.ssh_private_key,
        ssh_port: values.ssh_port,
        ssh_user: values.ssh_user,
      });
      // Stash the response so the success card
      // can render the public key line +
      // fingerprint side-by-side. The dialog
      // stays open (`open` stays true) so the
      // operator can copy the fingerprint before
      // closing — the form is "submitted" but
      // the success card is the closing surface.
      // The parent decides what to do with
      // `rotated` (toast + decide on list
      // refresh).
      rotationResult.value = res;
      emit("rotated", props.node);
    } catch (error) {
      const apiErr = toApiError(error);
      toast.add({
        title: t("nodes.rotateFailed"),
        description: apiErr.message,
        variant: "destructive",
      });
    }
  },
});

// Hydrate the form every time the dialog opens.
// The form is intentionally sparse on first paint
// (the operator pastes a PEM, the rest is optional
// overrides). On a fresh open we clear the
// previous response object so a stale
// `rotationResult` from a prior open does not
// leak across nodes / sessions.
watch(
  () => [props.open, props.node] as const,
  ([isOpen, node]) => {
    if (isOpen && node) {
      rotationResult.value = null;
      rotateForm.resetForm({
        values: {
          ssh_private_key: "",
          ssh_port: undefined,
          ssh_user: "",
        } as NodeRotatePanelKeyInput,
      });
    }
  },
  { immediate: true },
);

function onOpenChange(value: boolean): void {
  emit("update:open", value);
}

function onClose(): void {
  // Closing the success card flips `open` back
  // to false; the parent clears its `rotating`
  // pointer in its own `update:open` handler.
  emit("update:open", false);
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
          <RefreshCw class="h-4 w-4 inline-block mr-2 align-text-bottom" />
          {{ t("nodes.rotateTitle") }}
        </DialogTitle>
        <DialogDescription>
          {{
            t("nodes.rotateDescription")
          }}
        </DialogDescription>
      </DialogHeader>
      <p class="nodes__provision-target">
        <strong>{{ node?.name }}</strong>
        ({{ node?.address }}) —
        {{ node ? t(`nodes.states.${node.state}`) : "" }}
      </p>
      <Form
        v-if="!rotationResult"
        :is-submitting="rotateForm.isSubmitting.value"
        @submit="rotateForm.handleSubmit"
      >
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
                (v: string) => rotateForm.setFieldValue('ssh_user', v)
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
                  rotateForm.setFieldValue(
                    'ssh_port',
                    v === '' ? undefined : Number(v),
                  )
              "
              @blur="onBlur"
            />
          </template>
        </FormField>
        <FormField
          name="ssh_private_key"
          :label="t('nodes.rotateSshPrivateKey')"
          required
          :hint="t('nodes.rotateSshPrivateKeyHint')"
        >
          <template #default="{ id, value, onBlur, hasError }">
            <Textarea
              :id="id"
              :model-value="String(value ?? '')"
              :rows="10"
              :class="hasError && 'border-destructive'"
              spellcheck="false"
              placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
              @update:model-value="
                (v: string) => rotateForm.setFieldValue('ssh_private_key', v)
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
              :disabled="rotateForm.isSubmitting.value"
            >
              {{ t("common.cancel") }}
            </Button>
          </DialogClose>
          <Button
            type="submit"
            :disabled="rotateForm.isSubmitting.value"
          >
            <RefreshCw class="h-4 w-4" />
            {{ t("nodes.rotateAction") }}
          </Button>
        </DialogFooter>
      </Form>
      <!-- Success card. Renders after a
           200 response. The dialog stays
           open so the operator can copy
           the fingerprint before closing.
           The form is hidden (`v-if`) so
           the submit button is not
           visible alongside the success
           card. The `rotationResult` ref
           is the dialog's internal state
           (not a prop); the form / success
           card branch on its presence. -->
      <div
        v-else
        class="nodes__rotation-result"
      >
        <h3 class="nodes__rotation-result-title">
          <KeyRound class="h-4 w-4 inline-block mr-2 align-text-bottom" />
          {{ t("nodes.rotateResultTitle") }}
        </h3>
        <p class="nodes__rotation-result-help">
          {{ t("nodes.rotateResultHelp") }}
        </p>
        <FormField
          name="rotate-public-key"
          :label="t('nodes.rotatePublicKeyLine')"
        >
          <template #default="{ id }">
            <Textarea
              :id="id"
              :model-value="rotationResult.public_key_line"
              :rows="4"
              readonly
              spellcheck="false"
              @update:model-value="() => {}"
            />
          </template>
        </FormField>
        <FormField
          name="rotate-fingerprint"
          :label="t('nodes.rotateFingerprint')"
        >
          <template #default="{ id }">
            <Input
              :id="id"
              :model-value="rotationResult.fingerprint"
              readonly
              @update:model-value="() => {}"
            />
          </template>
        </FormField>
        <DialogFooter>
          <Button
            type="button"
            @click="onClose"
          >
            {{ t("common.close") }}
          </Button>
        </DialogFooter>
      </div>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
/* v0.9.x: colocation with the dialog markup.
   The same `.nodes__rotation-result*` styles
   still live in NodesView.vue for the inspect
   dialog (which reuses the success-card layout
   for the stored-key surface). PR 6 (InspectDialog
   extract) will drop the duplicate from
   NodesView. */
.nodes__rotation-result {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  margin-top: 0.5rem;
}

.nodes__rotation-result-title {
  font-size: 0.95rem;
  font-weight: 600;
  margin: 0;
}

.nodes__rotation-result-help {
  font-size: 0.85rem;
  color: hsl(var(--muted-foreground));
  margin: 0;
}

/* v0.9.x: colocation with the dialog markup.
   The same `.nodes__provision-target` style
   still lives in NodesView.vue for the
   inspect / refresh dialogs (which are not
   extracted in this PR). */
.nodes__provision-target {
  margin: 0 0 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.375rem;
  background: hsl(var(--muted));
  font-size: 0.875rem;
}
</style>
