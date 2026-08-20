<!--
  SPDX-License-Identifier: AGPL-3.0-or-later

  NodeInspectDialog. v0.8.5 ships the show-stored-key
  dialog for /api/v1/nodes/{id}/stored-key. Extracted
  from NodesView.vue in v0.9.x to keep the view's
  per-action dialogs in separate, focused files.
  Self-contained: owns its own loading/empty/error/
  result state, the fire-and-forget GET on dialog
  open, and the success card with the public key
  line + fingerprint.

  The dialog has four states: loading, no-stored-key
  (empty), error, success. The success card reuses
  the rotation-result styling for visual consistency
  with the rotate-panel-key dialog's success surface.

  Lifecycle:
    * The parent sets `node` + flips `open` -> the
      dialog's watch hydrates / resets the loading
      state and fires the GET (fire-and-forget). The
      spinner is the only content until the response
      lands.
    * On 200: stash the response in `inspection`.
      The template branches on `has_stored_key` to
      render the empty state or the success card.
    * On 4xx/5xx: stash the error message in
      `inspectionError`, show inline error, emit
      `failed` (with row + error) so the parent
      can toast. The dialog stays open with the
      error.
    * On cancel / ESC / backdrop click / Close
      button the dialog emits `update:open` ->
      false. The parent flips its `inspectOpen`
      ref.

  List refresh: the inspect endpoint is a read, not
  a write. The parent does NOT refresh the list on
  `failed` — the dialog is the only post-inspect
  surface.

  v0.9.x: the `.nodes__inspection-*` style block
  (previously in NodesView.vue) is colocated with
  the dialog markup. The `.nodes__rotation-result*`
  rules are duplicated here from NodeRotateDialog
  for self-containment (PR 6 may drop the original
  from NodesView); the inspect success card reuses
  the same class names as rotate so the two
  success surfaces look identical.
-->
<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { Eye, KeyRound } from "lucide-vue-next";

import { getStoredNodeKey } from "@/api/services";
import { toApiError } from "@/api/client";
import type { Node, NodeStoredKey, UUID } from "@/types";

import Button from "@/components/ui/Button.vue";
import Dialog from "@/components/ui/Dialog.vue";
import DialogContent from "@/components/ui/DialogContent.vue";
import DialogHeader from "@/components/ui/DialogHeader.vue";
import DialogTitle from "@/components/ui/DialogTitle.vue";
import DialogDescription from "@/components/ui/DialogDescription.vue";
import DialogFooter from "@/components/ui/DialogFooter.vue";
import FormField from "@/components/FormField.vue";
import Input from "@/components/ui/Input.vue";
import Textarea from "@/components/ui/Textarea.vue";

const props = defineProps<{
  open: boolean;
  node: Node | null;
}>();

const emit = defineEmits<{
  "update:open": [value: boolean];
  failed: [node: Node, error: string];
}>();

const { t } = useI18n();

// Internal state. Owned by the dialog; the view
// never reads these. Reset every time the dialog
// opens so stale result/error from a prior open
// does not leak across nodes / sessions.
const inspection = ref<NodeStoredKey | null>(null);
const inspectionError = ref<string | null>(null);
const inspectionLoading = ref(false);

// Fire-and-forget the GET on dialog open. The
// dialog's spinner is driven by `inspectionLoading`;
// the result (or error) lands in the `inspection`
// / `inspectionError` refs. The pattern mirrors
// `getCredentialsByUser` in CredentialsView.vue —
// a one-shot fetch on dialog open, no polling,
// no refetch-on-error (the operator can close and
// re-open to retry).
watch(
  () => [props.open, props.node] as const,
  ([isOpen, node]) => {
    if (isOpen && node) {
      inspection.value = null;
      inspectionError.value = null;
      inspectionLoading.value = true;
      void loadStoredKey(node.id, node);
    }
  },
  { immediate: true },
);

async function loadStoredKey(id: UUID, node: Node): Promise<void> {
  try {
    const sk = await getStoredNodeKey(id);
    inspection.value = sk;
  } catch (error) {
    const apiErr = toApiError(error);
    inspectionError.value = apiErr.message;
    emit("failed", node, apiErr.message);
  } finally {
    inspectionLoading.value = false;
  }
}

function onOpenChange(value: boolean): void {
  emit("update:open", value);
}

function onClose(): void {
  // Closing the dialog flips `open` back to
  // false; the parent clears its `inspecting`
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
          <Eye class="h-4 w-4 inline-block mr-2 align-text-bottom" />
          {{ t("nodes.inspectTitle") }}
        </DialogTitle>
        <DialogDescription>{{ t("nodes.inspectDescription") }}</DialogDescription>
      </DialogHeader>
      <p class="nodes__provision-target">
        <strong>{{ node?.name }}</strong>
        ({{ node?.address }})
        — {{ node ? t(`nodes.states.${node.state}`) : "" }}
      </p>
      <!-- Loading state. The dialog opens
           immediately (so the user can see
           the target node); the GET fires
           on open. The spinner is the only
           content until the response lands. -->
      <div
        v-if="inspectionLoading"
        class="nodes__inspection-loading"
      >
        {{ t("nodes.inspectLoading") }}
      </div>
      <!-- "No stored key" state. The row
           exists but the ciphertext column
           is empty (`new` nodes, or legacy
           v0.3.0..v0.7.x nodes that have not
           been back-filled with the v0.8.3
           CLI). The dialog shows a hint about
           how to populate the column. -->
      <div
        v-else-if="inspection && !inspection.has_stored_key"
        class="nodes__inspection-empty"
      >
        <p>{{ t("nodes.inspectNoKey") }}</p>
        <p class="nodes__inspection-empty-hint">
          {{ t("nodes.inspectNoKeyHint") }}
        </p>
      </div>
      <!-- Error state. The toast was already
           shown by the parent handler; the
           dialog shows a brief inline error
           so the user knows the dialog
           content is in a failed state. -->
      <div
        v-else-if="inspectionError"
        class="nodes__inspection-error"
      >
        {{ inspectionError }}
      </div>
      <!-- Success state. The dialog surfaces
           the public-key line + fingerprint
           so the operator can copy the
           fingerprint (compare against
           `ssh-add -L` on the operator's
           local box after the re-provision's
           first contact). The styling reuses
           the rotation-result class names so
           the inspect success surface looks
           identical to the rotate-panel-key
           success surface. -->
      <div
        v-else-if="inspection && inspection.has_stored_key"
        class="nodes__rotation-result"
      >
        <h3 class="nodes__rotation-result-title">
          <KeyRound class="h-4 w-4 inline-block mr-2 align-text-bottom" />
          {{ t("nodes.inspectSurfaceTitle") }}
        </h3>
        <p class="nodes__rotation-result-help">
          {{ t("nodes.inspectSurfaceHelp") }}
        </p>
        <FormField
          name="inspect-public-key"
          :label="t('nodes.rotatePublicKeyLine')"
        >
          <template #default="{ id }">
            <Textarea
              :id="id"
              :model-value="inspection.public_key_line ?? ''"
              :rows="4"
              readonly
              spellcheck="false"
              @update:model-value="() => {}"
            />
          </template>
        </FormField>
        <FormField
          name="inspect-fingerprint"
          :label="t('nodes.rotateFingerprint')"
        >
          <template #default="{ id }">
            <Input
              :id="id"
              :model-value="inspection.fingerprint ?? ''"
              readonly
              @update:model-value="() => {}"
            />
          </template>
        </FormField>
        <FormField
          v-if="inspection.key_updated_at"
          name="inspect-key-updated-at"
          :label="t('nodes.inspectKeyUpdatedAt')"
        >
          <template #default="{ id }">
            <Input
              :id="id"
              :model-value="inspection.key_updated_at"
              readonly
              @update:model-value="() => {}"
            />
          </template>
        </FormField>
      </div>
      <DialogFooter>
        <Button
          type="button"
          variant="outline"
          @click="onClose"
        >
          {{ t("common.close") }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>

<style scoped>
/* v0.9.x: colocation with the dialog markup. The
   same `.nodes__inspection-*` style block lived
   in NodesView.vue before this PR (and was
   removed as part of the extraction). The four
   states (loading / empty / error / success)
   are styled here; the success state reuses the
   rotation-result rules below. */
.nodes__inspection-loading,
.nodes__inspection-empty,
.nodes__inspection-error {
  padding: 0.75rem 1rem;
  border-radius: 0.5rem;
  font-size: 0.9rem;
  margin: 0.5rem 0;
}

.nodes__inspection-loading {
  background: hsl(var(--muted));
  color: hsl(var(--muted-foreground));
  text-align: center;
}

.nodes__inspection-empty {
  background: hsl(var(--muted));
  color: hsl(var(--muted-foreground));
}

.nodes__inspection-empty-hint {
  font-size: 0.8rem;
  margin: 0.5rem 0 0;
  opacity: 0.8;
}

.nodes__inspection-error {
  background: hsl(var(--destructive) / 0.1);
  color: hsl(var(--destructive));
  font-family: monospace;
  font-size: 0.8rem;
}

/* v0.9.x: colocation with the dialog markup. The
   inspect success card reuses the rotation-result
   class names so the inspect + rotate success
   surfaces look identical. The same rules live
   in NodeRotateDialog; the original `.nodes__rotation-result*`
   block in NodesView.vue is kept for now (PR 6
   may drop the duplicate). */
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

/* v0.9.x: colocation with the dialog markup. The
   same `.nodes__provision-target` style still
   lives in NodesView.vue for any future per-action
   dialogs (and is duplicated in NodeProvisionDialog
   + NodeRotateDialog + NodeRefreshDialog for
   self-containment). */
.nodes__provision-target {
  margin: 0 0 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1px solid hsl(var(--border));
  border-radius: 0.375rem;
  background: hsl(var(--muted));
  font-size: 0.875rem;
}
</style>
